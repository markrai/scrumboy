package com.markrai.scrumboy.speech

import com.google.mlkit.genai.common.DownloadStatus
import com.google.mlkit.genai.common.FeatureStatus
import com.google.mlkit.genai.common.GenAiException
import com.google.mlkit.genai.common.audio.AudioSource
import com.google.mlkit.genai.speechrecognition.SpeechRecognition
import com.google.mlkit.genai.speechrecognition.SpeechRecognizer
import com.google.mlkit.genai.speechrecognition.SpeechRecognizerOptions
import com.google.mlkit.genai.speechrecognition.SpeechRecognizerResponse
import com.google.mlkit.genai.speechrecognition.speechRecognizerOptions
import com.google.mlkit.genai.speechrecognition.speechRecognizerRequest
import java.util.Locale
import java.util.concurrent.CompletableFuture
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.runInterruptible

/**
 * Alpha1 ML Kit GenAI speech recognition exposes suspend/Flow APIs only.
 * This narrow Kotlin bridge is the smallest officially supported surface for Java callers.
 *
 * Capture boundary: alpha1 starts the session when the Flow from
 * [SpeechRecognizer.startRecognition] is collected. There is no mic-acquired callback.
 * After [startRecognition] is invoked with [AudioSource.fromMic], same-turn platform
 * fallback is forbidden because capture may have begun.
 *
 * Teardown: [RecognitionHandle.cancel] / [RecognitionHandle.stop] never use Main-thread
 * runBlocking. Native stop/close run once on IO; teardownFuture completes only after
 * stopRecognition, close, and [AdvancedRecognizerSessionGate] release. prepareRecognition
 * returns a lazy handle and never starts the coroutine. The caller publishes the handle,
 * attaches exact cancellation, registers the cross-provider barrier, then start().
 */
internal class MlKitAdvancedSpeechRuntime(
    private val sessionGate: AdvancedRecognizerSessionGate = AdvancedRecognizerSessionGate(),
) : AdvancedSpeechRuntime {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val preparationJobs = PreparationJobTable<Job>()
    private val closed = AtomicBoolean(false)

    @Throws(Exception::class)
    override fun probeAdvancedStatus(localeTag: String): Int {
        ensureOpen()
        val locale = Locale.forLanguageTag(localeTag)
        return runBlocking(Dispatchers.IO) {
            val recognizer = createClient(locale)
            try {
                mapFeatureStatus(recognizer.checkStatus())
            } finally {
                recognizer.close()
            }
        }
    }

    override fun startPreparation(
        localeTag: String,
        generation: Long,
        listener: AdvancedSpeechRuntime.PreparationListener,
    ) {
        ensureOpen()
        val existing = preparationJobs.get(localeTag)
        if (existing != null && existing.generation == generation && existing.job.isActive) return
        val locale = Locale.forLanguageTag(localeTag)
        val job = scope.launch(Dispatchers.IO, start = CoroutineStart.LAZY) {
            val recognizer = createClient(locale)
            try {
                recognizer.download()
                    .catch { error ->
                        if (preparationJobs.removeExact(localeTag, generation)) {
                            listener.onFailed(localeTag, generation, error)
                        }
                    }
                    .collect { status ->
                        when (status) {
                            is DownloadStatus.DownloadCompleted -> {
                                if (preparationJobs.removeExact(localeTag, generation)) {
                                    listener.onCompleted(localeTag, generation)
                                }
                            }
                            is DownloadStatus.DownloadFailed -> {
                                if (preparationJobs.removeExact(localeTag, generation)) {
                                    listener.onFailed(localeTag, generation, status.e)
                                }
                            }
                            else -> Unit
                        }
                    }
            } finally {
                recognizer.close()
            }
        }
        val previous = preparationJobs.install(localeTag, generation, job)
        previous?.job?.cancel()
        job.start()
    }

    override fun isPreparationActive(localeTag: String): Boolean {
        return preparationJobs.isActive(localeTag) { it.isActive }
    }

    override fun cancelPreparation(localeTag: String) {
        preparationJobs.removeAny(localeTag)?.job?.cancel()
    }

    override fun cancelAllPreparations() {
        preparationJobs.removeAll().forEach { it.job.cancel() }
    }

    override fun close() {
        if (!closed.compareAndSet(false, true)) return
        cancelAllPreparations()
        scope.cancel()
    }

    fun prepareRecognition(
        localeTag: String,
        callbacks: RecognitionCallbacks,
    ): RecognitionHandle {
        ensureOpen()
        val locale = Locale.forLanguageTag(localeTag)
        val handle = RecognitionHandle()
        val job = scope.launch(Dispatchers.IO, start = CoroutineStart.LAZY) {
            var sessionTeardown: CompletableFuture<Void>? = null
            var recognizer: SpeechRecognizer? = null
            var captureHandedToSdk = false
            try {
                sessionTeardown = runInterruptible(Dispatchers.IO) {
                    sessionGate.acquire()
                }
                handle.markNativeStarted()
                recognizer = createClient(locale)
                handle.recognizer = recognizer
                val request = speechRecognizerRequest {
                    audioSource = AudioSource.fromMic()
                }
                // Invoking startRecognition with fromMic may start capture on collect.
                val flow = recognizer.startRecognition(request)
                captureHandedToSdk = true
                handle.captureHandedToSdk.set(true)
                callbacks.onListening()
                flow
                    .catch { error ->
                        callbacks.onFailure(error as? GenAiException, error, true)
                    }
                    .collect { response ->
                        when (response) {
                            is SpeechRecognizerResponse.PartialTextResponse ->
                                callbacks.onPartial(response.text)
                            is SpeechRecognizerResponse.FinalTextResponse ->
                                callbacks.onFinal(response.text)
                            is SpeechRecognizerResponse.CompletedResponse ->
                                callbacks.onCompleted()
                            is SpeechRecognizerResponse.ErrorResponse ->
                                callbacks.onFailure(response.e, response.e, true)
                        }
                    }
            } catch (error: Throwable) {
                if (error is kotlinx.coroutines.CancellationException) throw error
                callbacks.onFailure(error as? GenAiException, error, captureHandedToSdk)
            } finally {
                try {
                    val target = recognizer
                    if (target != null) {
                        try {
                            target.stopRecognition()
                        } catch (_: Throwable) {
                        } finally {
                            try {
                                target.close()
                            } catch (_: Throwable) {
                            }
                        }
                    }
                } finally {
                    // teardownFuture completes only after stop, close, and gate release.
                    sessionTeardown?.let { sessionGate.finish(it) }
                    handle.completeTeardown()
                }
            }
        }
        handle.attachJob(job)
        return handle
    }

    class RecognitionHandle : AdvancedRecognitionOwnership.Session {
        private lateinit var job: Job
        internal var recognizer: SpeechRecognizer? = null
        val captureHandedToSdk = AtomicBoolean(false)
        private val started = AtomicBoolean(false)
        private val teardownStarted = AtomicBoolean(false)
        private val teardown = CompletableDeferred<Unit>()
        private val nativeStartCount = AtomicInteger(0)

        fun captureHandedToSdk(): Boolean = captureHandedToSdk.get()

        /** Counts coroutine bodies that reached Advanced session ownership. */
        fun nativeStartCount(): Int = nativeStartCount.get()

        /**
         * Installs the lazy recognition job and its teardown backstop. Job completion
         * always settles [teardownFuture], including cancellation after dispatch but
         * before the coroutine body (and therefore its finally block) is entered.
         * When the body does run, its finally performs stop, close, and gate release
         * before the job completes, so ordering is preserved.
         */
        internal fun attachJob(job: Job) {
            this.job = job
            job.invokeOnCompletion { completeTeardown() }
        }

        internal fun markNativeStarted() {
            nativeStartCount.incrementAndGet()
        }

        /**
         * Starts the recognition coroutine. No-op and settles teardown if already
         * cancelled or previously started. prepareRecognition never launches the session.
         */
        override fun start() {
            if (!started.compareAndSet(false, true)) return
            if (!this::job.isInitialized || job.isCancelled || job.isCompleted) {
                completeTeardown()
                return
            }
            job.start()
        }

        /**
         * Marks the operation cancelled immediately. Native stop/close continues on the
         * recognition coroutine and settles [teardownFuture]. Does not runBlocking.
         * Cancellation before start settles teardown at once; cancellation after start is
         * settled by the job-completion backstop once native cleanup has run.
         */
        override fun cancel() {
            if (!this::job.isInitialized) {
                completeTeardown()
                return
            }
            job.cancel()
        }

        fun stop() {
            cancel()
        }

        override fun teardownFuture(): CompletableFuture<Void> {
            val future = CompletableFuture<Void>()
            teardown.invokeOnCompletion { error ->
                if (error != null && error !is kotlinx.coroutines.CancellationException) {
                    future.completeExceptionally(error)
                } else {
                    future.complete(null)
                }
            }
            return future
        }

        internal fun completeTeardown() {
            if (!teardownStarted.compareAndSet(false, true)) return
            teardown.complete(Unit)
        }
    }

    interface RecognitionCallbacks {
        fun onListening()
        fun onPartial(text: String)
        fun onFinal(text: String)
        fun onCompleted()
        fun onFailure(genAi: GenAiException?, error: Throwable, captureHandedToSdk: Boolean)
    }

    private fun createClient(locale: Locale): SpeechRecognizer {
        val options = speechRecognizerOptions {
            this.locale = locale
            preferredMode = SpeechRecognizerOptions.Mode.MODE_ADVANCED
        }
        return SpeechRecognition.getClient(options)
    }

    private fun ensureOpen() {
        check(!closed.get()) { "advanced speech runtime closed" }
    }

    companion object {
        @JvmStatic
        fun mapFeatureStatus(status: Int): Int = when (status) {
            FeatureStatus.AVAILABLE -> AdvancedSpeechRuntime.FEATURE_AVAILABLE
            FeatureStatus.DOWNLOADABLE -> AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE
            FeatureStatus.DOWNLOADING -> AdvancedSpeechRuntime.FEATURE_DOWNLOADING
            else -> AdvancedSpeechRuntime.FEATURE_UNAVAILABLE
        }

        @JvmStatic
        fun isCapabilityFailure(exception: GenAiException?): Boolean {
            return AdvancedSpeechErrorMapper.isCapabilityLevel(exception)
        }
    }
}
