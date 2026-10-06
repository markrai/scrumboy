package com.markrai.scrumboy.speech

import java.util.Collections
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancel
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withContext
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Real Kotlin Job semantics for [MlKitAdvancedSpeechRuntime.RecognitionHandle]: the handle's
 * teardown future must settle even when the lazy job is dispatched and then cancelled before
 * its body runs, and must still mean stop -> close -> gate release when the body did run.
 */
class AdvancedRecognitionHandleTeardownTest {
    @Test
    fun cancellationAfterDispatchButBeforeBodyEntrySettlesTeardown() {
        val worker = Executors.newSingleThreadExecutor { runnable ->
            Thread(runnable, "advanced-single").apply { isDaemon = true }
        }
        val dispatcher = worker.asCoroutineDispatcher()
        val scope = CoroutineScope(SupervisorJob() + dispatcher)
        val occupied = CountDownLatch(1)
        val release = CountDownLatch(1)
        val bodyEntered = AtomicInteger()
        try {
            // Occupy the only dispatcher thread so the handle's job can be dispatched
            // but cannot enter its body before cancellation arrives.
            scope.launch {
                occupied.countDown()
                release.await()
            }
            assertTrue(occupied.await(5, TimeUnit.SECONDS))

            val handle = MlKitAdvancedSpeechRuntime.RecognitionHandle()
            val job = scope.launch(start = CoroutineStart.LAZY) {
                handle.markNativeStarted()
                bodyEntered.incrementAndGet()
                awaitCancellation()
            }
            handle.attachJob(job)
            val teardown = handle.teardownFuture()

            handle.start()
            handle.cancel()
            release.countDown()

            teardown.get(5, TimeUnit.SECONDS)
            assertTrue(teardown.isDone)
            assertTrue(job.isCancelled)
            assertTrue(job.isCompleted)
            assertEquals(0, bodyEntered.get())
            assertEquals(0, handle.nativeStartCount())
            assertFalse(handle.captureHandedToSdk())
        } finally {
            release.countDown()
            scope.cancel()
            dispatcher.close()
            worker.shutdownNow()
        }
    }

    @Test
    fun bodyEnteredTeardownStopsClosesAndReleasesTheGateBeforeSettling() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val gate = AdvancedRecognizerSessionGate()
        val events = Collections.synchronizedList(mutableListOf<String>())
        val entered = CountDownLatch(1)
        try {
            val handle = MlKitAdvancedSpeechRuntime.RecognitionHandle()
            val job = scope.launch(Dispatchers.IO, start = CoroutineStart.LAZY) {
                var sessionTeardown: CompletableFuture<Void>? = null
                try {
                    sessionTeardown = runInterruptible(Dispatchers.IO) { gate.acquire() }
                    handle.markNativeStarted()
                    entered.countDown()
                    awaitCancellation()
                } finally {
                    events.add("stop")
                    events.add("close")
                    sessionTeardown?.let {
                        gate.finish(it)
                        events.add("gate-release")
                    }
                    handle.completeTeardown()
                }
            }
            handle.attachJob(job)
            val teardown = handle.teardownFuture()

            handle.start()
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            assertTrue(gate.isBusy)

            handle.cancel()
            teardown.get(5, TimeUnit.SECONDS)

            assertEquals(listOf("stop", "close", "gate-release"), events.toList())
            assertFalse(gate.isBusy)
            assertEquals(1, handle.nativeStartCount())
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun cancellingASessionWaitingOnTheGateSettlesTeardownWithoutLeakingOwnership() {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val gate = AdvancedRecognizerSessionGate()
        val waiting = CountDownLatch(1)
        try {
            val sessionA = gate.acquire()
            val handle = MlKitAdvancedSpeechRuntime.RecognitionHandle()
            val job = scope.launch(Dispatchers.IO, start = CoroutineStart.LAZY) {
                var sessionTeardown: CompletableFuture<Void>? = null
                try {
                    waiting.countDown()
                    sessionTeardown = runInterruptible(Dispatchers.IO) { gate.acquire() }
                    handle.markNativeStarted()
                    awaitCancellation()
                } finally {
                    sessionTeardown?.let { gate.finish(it) }
                    handle.completeTeardown()
                }
            }
            handle.attachJob(job)
            val teardown = handle.teardownFuture()

            handle.start()
            assertTrue(waiting.await(5, TimeUnit.SECONDS))
            handle.cancel()

            teardown.get(5, TimeUnit.SECONDS)
            assertEquals(0, handle.nativeStartCount())
            assertEquals(1, gate.recognizerStartCount())
            assertTrue(gate.isBusy)

            gate.finish(sessionA)
            assertFalse(gate.isBusy)
        } finally {
            scope.cancel()
        }
    }

    /**
     * A successful FinalTextResponse now tears the Advanced session down by cancelling the
     * recognition job. `SpeechRecognizer.stopRecognition` is a suspend function, so in a
     * cancelled coroutine it fails fast at its suspension point unless the cleanup is
     * shielded. This models the production `finally` block and proves both halves: the
     * unshielded shape silently skips the native stop, the shielded shape preserves
     * stop -> close -> gate release.
     */
    @Test
    fun cancellationDrivenTeardownStillRunsTheSuspendingStopWhenShielded() {
        assertEquals(listOf("close", "gate-release"), teardownEventsForCancelledSession(shielded = false))
        assertEquals(listOf("stop", "close", "gate-release"), teardownEventsForCancelledSession(shielded = true))
    }

    /**
     * Runs one lazy Advanced session that is cancelled while active, mirroring
     * [MlKitAdvancedSpeechRuntime.prepareRecognition]'s teardown, and returns the native
     * cleanup steps that actually executed.
     */
    private fun teardownEventsForCancelledSession(shielded: Boolean): List<String> {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val gate = AdvancedRecognizerSessionGate()
        val events = Collections.synchronizedList(mutableListOf<String>())
        val entered = CountDownLatch(1)
        try {
            val handle = MlKitAdvancedSpeechRuntime.RecognitionHandle()
            val job = scope.launch(Dispatchers.IO, start = CoroutineStart.LAZY) {
                var sessionTeardown: CompletableFuture<Void>? = null
                var acquired = false
                try {
                    sessionTeardown = runInterruptible(Dispatchers.IO) { gate.acquire() }
                    acquired = true
                    handle.markNativeStarted()
                    entered.countDown()
                    awaitCancellation()
                } finally {
                    try {
                        if (acquired) {
                            if (shielded) {
                                withContext(NonCancellable) { stopThenClose(events) }
                            } else {
                                stopThenClose(events)
                            }
                        }
                    } finally {
                        sessionTeardown?.let {
                            gate.finish(it)
                            events.add("gate-release")
                        }
                        handle.completeTeardown()
                    }
                }
            }
            handle.attachJob(job)
            val teardown = handle.teardownFuture()

            handle.start()
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            assertTrue(gate.isBusy)

            // What a successful final now does to begin Advanced teardown.
            handle.cancel()
            teardown.get(5, TimeUnit.SECONDS)

            assertFalse("the gate must always be released", gate.isBusy)
            assertEquals(1, handle.nativeStartCount())
            return events.toList()
        } finally {
            scope.cancel()
        }
    }

    /** `stopRecognition` is suspending in alpha1; `close` is not. */
    private suspend fun stopThenClose(events: MutableList<String>) {
        try {
            yield()
            events.add("stop")
        } catch (_: Throwable) {
        } finally {
            events.add("close")
        }
    }
}
