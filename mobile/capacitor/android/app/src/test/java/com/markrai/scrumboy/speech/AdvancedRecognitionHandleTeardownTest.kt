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
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
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
}
