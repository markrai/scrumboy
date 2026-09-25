package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class AdvancedSpeechProviderManagerTest {
    private static final class FakeClock implements AdvancedSpeechClock {
        long now = 1_000L;

        @Override
        public long nowMs() {
            return now;
        }
    }

    private static final class FakeRuntime implements AdvancedSpeechRuntime {
        final AtomicInteger probeCount = new AtomicInteger();
        final AtomicInteger preparationStarts = new AtomicInteger();
        final List<String> preparationLocales = new ArrayList<>();
        final List<Long> preparationGenerations = new ArrayList<>();
        int nextStatus = FEATURE_UNAVAILABLE;
        Throwable probeError;
        final Map<String, Boolean> activeByLocale = new java.util.HashMap<>();
        final Map<String, PreparationListener> listenersByLocale = new java.util.HashMap<>();
        final Map<String, Long> generationByLocale = new java.util.HashMap<>();
        PreparationListener activeListener;
        String activeLocale;
        long activeGeneration;
        CountDownLatch probeStarted;
        CountDownLatch probeRelease;

        @Override
        public int probeAdvancedStatus(String localeTag) throws Exception {
            probeCount.incrementAndGet();
            if (probeStarted != null) probeStarted.countDown();
            if (probeRelease != null) probeRelease.await(5, TimeUnit.SECONDS);
            if (probeError != null) {
                if (probeError instanceof Exception exception) throw exception;
                throw new Exception(probeError);
            }
            return nextStatus;
        }

        @Override
        public void startPreparation(String localeTag, long generation, PreparationListener listener) {
            if (isPreparationActive(localeTag)) return;
            preparationStarts.incrementAndGet();
            preparationLocales.add(localeTag);
            preparationGenerations.add(generation);
            activeByLocale.put(localeTag, true);
            listenersByLocale.put(localeTag, listener);
            generationByLocale.put(localeTag, generation);
            activeListener = listener;
            activeLocale = localeTag;
            activeGeneration = generation;
        }

        @Override
        public boolean isPreparationActive(String localeTag) {
            return Boolean.TRUE.equals(activeByLocale.get(localeTag));
        }

        @Override
        public void cancelPreparation(String localeTag) {
            activeByLocale.put(localeTag, false);
        }

        @Override
        public void cancelAllPreparations() {
            activeByLocale.replaceAll((locale, active) -> false);
        }

        @Override
        public void close() {}

        void completePreparation() {
            fireCompleted(activeGeneration);
        }

        void fireCompleted(long generation) {
            PreparationListener listener = activeListener;
            String locale = activeLocale;
            if (generation == activeGeneration && locale != null) {
                activeByLocale.put(locale, false);
            }
            if (listener != null) listener.onCompleted(locale, generation);
        }

        void fireFailed(long generation, Throwable error) {
            PreparationListener listener = activeListener;
            String locale = activeLocale;
            if (generation == activeGeneration && locale != null) {
                activeByLocale.put(locale, false);
            }
            if (listener != null) listener.onFailed(locale, generation, error);
        }
    }

    @Test
    public void availableProbeCachesReadyAndAvoidsRepeatProbes() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_AVAILABLE;
        FakeClock clock = new FakeClock();
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new InMemoryAdvancedSpeechPreferenceStore()),
            runtime,
            clock
        );

        AdvancedSpeechDecision first = manager.resolve("en-US");
        AdvancedSpeechDecision second = manager.resolve("en-US");
        AdvancedSpeechDecision third = manager.resolve("en-US");

        assertEquals(SpeechInputProviderId.MLKIT_GENAI_ADVANCED, first.providerId);
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_READY, first.state);
        assertEquals("miss", first.cacheAccess);
        assertEquals("probed", first.statusSource);
        assertEquals("hit", second.cacheAccess);
        assertEquals("cached", second.statusSource);
        assertEquals("hit", third.cacheAccess);
        assertEquals(1, runtime.probeCount.get());
        assertEquals(0, runtime.preparationStarts.get());
    }

    @Test
    public void downloadableStartsOnePreparationAndUsesFallbackUntilReady() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE;
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(
            new InMemoryAdvancedSpeechPreferenceStore()
        );
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            store,
            runtime,
            new FakeClock()
        );

        AdvancedSpeechDecision first = manager.resolve("en-US");
        AdvancedSpeechDecision second = manager.resolve("en-US");
        AdvancedSpeechDecision third = manager.resolve("en-US");

        assertEquals(SpeechInputProviderId.ANDROID_ON_DEVICE, first.providerId);
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_PREPARING, first.state);
        assertEquals(1, runtime.probeCount.get());
        assertEquals(1, runtime.preparationStarts.get());
        assertEquals(SpeechInputProviderId.ANDROID_ON_DEVICE, second.providerId);
        assertEquals(SpeechInputProviderId.ANDROID_ON_DEVICE, third.providerId);
        assertEquals(1, runtime.preparationStarts.get());

        runtime.completePreparation();
        AdvancedSpeechDecision after = manager.resolve("en-US");
        assertTrue(after.useAdvanced());
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_READY, store.read("en-US").state);
        assertEquals(1, runtime.probeCount.get());
    }

    @Test
    public void downloadingIsTreatedAsSupportedPreparingWithoutUnsupportedHardwareMark() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_DOWNLOADING;
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new InMemoryAdvancedSpeechPreferenceStore()),
            runtime,
            new FakeClock()
        );

        AdvancedSpeechDecision decision = manager.resolve("en-US");
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_PREPARING, decision.state);
        assertEquals(SpeechInputProviderId.ANDROID_ON_DEVICE, decision.providerId);
        assertEquals(1, runtime.probeCount.get());
        manager.resolve("en-US");
        assertEquals(1, runtime.probeCount.get());
    }

    @Test
    public void unavailableUsesSixHourCooldownBeforeReprobe() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_UNAVAILABLE;
        FakeClock clock = new FakeClock();
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new InMemoryAdvancedSpeechPreferenceStore()),
            runtime,
            clock
        );

        AdvancedSpeechDecision first = manager.resolve("en-US");
        manager.resolve("en-US");
        manager.resolve("en-US");
        assertEquals(AdvancedSpeechCacheState.UNAVAILABLE_CACHED, first.state);
        assertEquals(1, runtime.probeCount.get());

        clock.now = 1_000L + AdvancedSpeechCacheRecord.NEGATIVE_REPROBE_INTERVAL_MS + 1;
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_AVAILABLE;
        AdvancedSpeechDecision afterCooldown = manager.resolve("en-US");
        assertEquals(2, runtime.probeCount.get());
        assertTrue(afterCooldown.useAdvanced());
        assertEquals("expired", afterCooldown.cacheAccess);
        assertEquals(6L * 60L * 60L * 1000L, AdvancedSpeechCacheRecord.NEGATIVE_REPROBE_INTERVAL_MS);
    }

    @Test
    public void probeExceptionCachesUnavailableWithoutCrashing() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.probeError = new IllegalStateException("aicore setup");
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new InMemoryAdvancedSpeechPreferenceStore()),
            runtime,
            new FakeClock()
        );

        AdvancedSpeechDecision decision = manager.resolve("en-US");
        assertEquals(AdvancedSpeechCacheState.UNAVAILABLE_CACHED, decision.state);
        assertEquals(SpeechInputProviderId.ANDROID_ON_DEVICE, decision.providerId);
        manager.resolve("en-US");
        assertEquals(1, runtime.probeCount.get());
    }

    @Test
    public void positiveCacheSurvivesManagerRecreationWithoutProbe() {
        InMemoryAdvancedSpeechPreferenceStore prefs = new InMemoryAdvancedSpeechPreferenceStore();
        FakeRuntime firstRuntime = new FakeRuntime();
        firstRuntime.nextStatus = AdvancedSpeechRuntime.FEATURE_AVAILABLE;
        AdvancedSpeechProviderManager first = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(prefs),
            firstRuntime,
            new FakeClock()
        );
        assertTrue(first.resolve("en-US").useAdvanced());
        assertEquals(1, firstRuntime.probeCount.get());

        FakeRuntime secondRuntime = new FakeRuntime();
        AdvancedSpeechProviderManager second = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(prefs),
            secondRuntime,
            new FakeClock()
        );
        AdvancedSpeechDecision restored = second.resolve("en-US");
        assertTrue(restored.useAdvanced());
        assertEquals("hit", restored.cacheAccess);
        assertEquals("cached", restored.statusSource);
        assertEquals(0, secondRuntime.probeCount.get());
    }

    @Test
    public void localeChangeDoesNotReusePriorProof() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_AVAILABLE;
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new InMemoryAdvancedSpeechPreferenceStore()),
            runtime,
            new FakeClock()
        );

        assertTrue(manager.resolve("en-US").useAdvanced());
        assertEquals(1, runtime.probeCount.get());
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_UNAVAILABLE;
        AdvancedSpeechDecision other = manager.resolve("fr-FR");
        assertFalse(other.useAdvanced());
        assertEquals(2, runtime.probeCount.get());
        assertEquals("miss", other.cacheAccess);
    }

    @Test
    public void stalePreparationSuccessAfterDemotionCannotRestoreReady() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE;
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(
            new InMemoryAdvancedSpeechPreferenceStore()
        );
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            store,
            runtime,
            new FakeClock()
        );

        manager.resolve("en-US");
        long generation = runtime.activeGeneration;
        AdvancedSpeechRuntime.PreparationListener stale = runtime.activeListener;
        manager.demoteForCapabilityFailure("en-US");
        assertEquals(AdvancedSpeechCacheState.UNAVAILABLE_CACHED, store.read("en-US").state);

        stale.onCompleted("en-US", generation);
        assertEquals(AdvancedSpeechCacheState.UNAVAILABLE_CACHED, store.read("en-US").state);
        assertFalse(manager.resolve("en-US").useAdvanced());
    }

    @Test
    public void stalePreparationCallbacksAfterClearCannotRepopulateCache() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE;
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(
            new InMemoryAdvancedSpeechPreferenceStore()
        );
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            store,
            runtime,
            new FakeClock()
        );

        manager.resolve("en-US");
        long generation = runtime.activeGeneration;
        AdvancedSpeechRuntime.PreparationListener stale = runtime.activeListener;
        assertNotNull(store.read("en-US"));

        manager.clearCache();
        assertNull(store.read("en-US"));

        stale.onCompleted("en-US", generation);
        assertNull(store.read("en-US"));
        stale.onFailed("en-US", generation, new IllegalStateException("download failed"));
        assertNull(store.read("en-US"));
    }

    @Test
    public void replacementPreparationIgnoresOldGenerationAndDoesNotStartAThirdJob() {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE;
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(
            new InMemoryAdvancedSpeechPreferenceStore()
        );
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            store,
            runtime,
            new FakeClock()
        );

        manager.resolve("en-US");
        long firstGeneration = runtime.activeGeneration;
        AdvancedSpeechRuntime.PreparationListener firstListener = runtime.activeListener;
        runtime.cancelPreparation("en-US");
        manager.resolve("en-US");
        long secondGeneration = runtime.activeGeneration;
        assertEquals(2, runtime.preparationStarts.get());
        assertTrue(secondGeneration > firstGeneration);

        firstListener.onCompleted("en-US", firstGeneration);
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_PREPARING, store.read("en-US").state);

        manager.resolve("en-US");
        assertEquals(2, runtime.preparationStarts.get());

        runtime.completePreparation();
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_READY, store.read("en-US").state);
        assertEquals(secondGeneration, runtime.preparationGenerations.get(1).longValue());
    }

    @Test
    public void twoPreparingLocalesEachReconcileOnceAfterProcessRecreation() {
        InMemoryAdvancedSpeechPreferenceStore prefs = new InMemoryAdvancedSpeechPreferenceStore();
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(prefs);
        store.write(AdvancedSpeechCacheRecord.preparing("en-US", 1_000L));
        store.write(AdvancedSpeechCacheRecord.preparing("fr-FR", 1_000L));

        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE;
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            store,
            runtime,
            new FakeClock()
        );

        manager.resolve("en-US");
        manager.resolve("fr-FR");
        manager.resolve("en-US");
        manager.resolve("fr-FR");

        assertEquals(2, runtime.probeCount.get());
        assertEquals(2, runtime.preparationStarts.get());
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_PREPARING, store.read("en-US").state);
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_PREPARING, store.read("fr-FR").state);
    }

    @Test
    public void concurrentStatusAndListenShareOneProbeForTheSameLocaleEpoch() throws Exception {
        FakeRuntime runtime = new FakeRuntime();
        runtime.nextStatus = AdvancedSpeechRuntime.FEATURE_AVAILABLE;
        runtime.probeStarted = new CountDownLatch(1);
        runtime.probeRelease = new CountDownLatch(1);
        AdvancedSpeechProviderManager manager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new InMemoryAdvancedSpeechPreferenceStore()),
            runtime,
            new FakeClock()
        );
        ExecutorService pool = Executors.newFixedThreadPool(2);
        try {
            pool.submit(() -> manager.resolve("en-US"));
            assertTrue(runtime.probeStarted.await(5, TimeUnit.SECONDS));
            pool.submit(() -> manager.resolve("en-US"));
            runtime.probeRelease.countDown();
            pool.shutdown();
            assertTrue(pool.awaitTermination(5, TimeUnit.SECONDS));
        } finally {
            runtime.probeRelease.countDown();
            pool.shutdownNow();
        }
        assertEquals(1, runtime.probeCount.get());
    }
}
