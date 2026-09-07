package com.markrai.scrumboy.speech;

import java.util.HashMap;
import java.util.HashSet;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

final class AdvancedSpeechProviderManager {
    private final AdvancedSpeechCapabilityStore store;
    private final AdvancedSpeechRuntime runtime;
    private final AdvancedSpeechClock clock;

    private final Set<String> reconciledLocales = new HashSet<>();
    private final Map<String, Long> preparationGenerationByLocale = new HashMap<>();
    private long nextPreparationGeneration = 1L;

    AdvancedSpeechProviderManager(
        AdvancedSpeechCapabilityStore store,
        AdvancedSpeechRuntime runtime,
        AdvancedSpeechClock clock
    ) {
        this.store = store;
        this.runtime = runtime;
        this.clock = clock;
    }

    synchronized AdvancedSpeechDecision resolve(String languageOrNull) {
        String localeTag = normalizeLocaleTag(languageOrNull);
        AdvancedSpeechCacheRecord cached = store.read(localeTag);
        long now = clock.nowMs();

        if (cached == null) {
            return probe(localeTag, "miss");
        }

        return switch (cached.state) {
            case SUPPORTED_READY -> {
                reconciledLocales.add(localeTag);
                yield decision(
                    SpeechInputProviderId.MLKIT_GENAI_ADVANCED,
                    AdvancedSpeechCacheState.SUPPORTED_READY,
                    localeTag,
                    "hit",
                    "cached",
                    false
                );
            }
            case SUPPORTED_PREPARING -> {
                reconcilePreparing(localeTag);
                AdvancedSpeechCacheRecord after = store.read(localeTag);
                AdvancedSpeechCacheState state = after == null
                    ? AdvancedSpeechCacheState.UNKNOWN
                    : after.state;
                SpeechInputProviderId provider = state == AdvancedSpeechCacheState.SUPPORTED_READY
                    ? SpeechInputProviderId.MLKIT_GENAI_ADVANCED
                    : SpeechInputProviderId.ANDROID_ON_DEVICE;
                yield decision(
                    provider,
                    state == AdvancedSpeechCacheState.UNKNOWN
                        ? AdvancedSpeechCacheState.SUPPORTED_PREPARING
                        : state,
                    localeTag,
                    "hit",
                    "cached",
                    false
                );
            }
            case UNAVAILABLE_CACHED -> {
                if (now < cached.nextProbeAfterEpochMs) {
                    yield decision(
                        SpeechInputProviderId.ANDROID_ON_DEVICE,
                        AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
                        localeTag,
                        "hit",
                        "cached",
                        false
                    );
                }
                yield probe(localeTag, "expired");
            }
            case UNKNOWN -> probe(localeTag, "miss");
        };
    }

    synchronized void demoteForCapabilityFailure(String localeTag) {
        invalidatePreparation(localeTag);
        store.write(AdvancedSpeechCacheRecord.unavailable(localeTag, clock.nowMs()));
        runtime.cancelPreparation(localeTag);
    }

    synchronized void clearCache() {
        store.clear();
        reconciledLocales.clear();
        preparationGenerationByLocale.clear();
        runtime.cancelAllPreparations();
    }

    synchronized void close() {
        runtime.close();
    }

    private AdvancedSpeechDecision probe(String localeTag, String cacheAccess) {
        final int status;
        try {
            status = runtime.probeAdvancedStatus(localeTag);
        } catch (Throwable error) {
            store.write(AdvancedSpeechCacheRecord.unavailable(localeTag, clock.nowMs()));
            return decision(
                SpeechInputProviderId.ANDROID_ON_DEVICE,
                AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
                localeTag,
                cacheAccess,
                "probed",
                true
            );
        }

        if (status == AdvancedSpeechRuntime.FEATURE_AVAILABLE) {
            store.write(AdvancedSpeechCacheRecord.ready(localeTag, clock.nowMs()));
            reconciledLocales.add(localeTag);
            return decision(
                SpeechInputProviderId.MLKIT_GENAI_ADVANCED,
                AdvancedSpeechCacheState.SUPPORTED_READY,
                localeTag,
                cacheAccess,
                "probed",
                true
            );
        }

        if (status == AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE
            || status == AdvancedSpeechRuntime.FEATURE_DOWNLOADING
        ) {
            store.write(AdvancedSpeechCacheRecord.preparing(localeTag, clock.nowMs()));
            reconciledLocales.add(localeTag);
            ensurePreparation(localeTag);
            return decision(
                SpeechInputProviderId.ANDROID_ON_DEVICE,
                AdvancedSpeechCacheState.SUPPORTED_PREPARING,
                localeTag,
                cacheAccess,
                "probed",
                true
            );
        }

        store.write(AdvancedSpeechCacheRecord.unavailable(localeTag, clock.nowMs()));
        return decision(
            SpeechInputProviderId.ANDROID_ON_DEVICE,
            AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
            localeTag,
            cacheAccess,
            "probed",
            true
        );
    }

    private void reconcilePreparing(String localeTag) {
        if (reconciledLocales.contains(localeTag)) {
            ensurePreparation(localeTag);
            return;
        }
        reconciledLocales.add(localeTag);
        try {
            int status = runtime.probeAdvancedStatus(localeTag);
            if (status == AdvancedSpeechRuntime.FEATURE_AVAILABLE) {
                store.write(AdvancedSpeechCacheRecord.ready(localeTag, clock.nowMs()));
                invalidatePreparation(localeTag);
                runtime.cancelPreparation(localeTag);
                return;
            }
            if (
                status == AdvancedSpeechRuntime.FEATURE_DOWNLOADABLE
                || status == AdvancedSpeechRuntime.FEATURE_DOWNLOADING
            ) {
                store.write(AdvancedSpeechCacheRecord.preparing(localeTag, clock.nowMs()));
                ensurePreparation(localeTag);
                return;
            }
            store.write(AdvancedSpeechCacheRecord.unavailable(localeTag, clock.nowMs()));
            invalidatePreparation(localeTag);
            runtime.cancelPreparation(localeTag);
        } catch (Throwable error) {
            store.write(AdvancedSpeechCacheRecord.unavailable(localeTag, clock.nowMs()));
            invalidatePreparation(localeTag);
            runtime.cancelPreparation(localeTag);
        }
    }

    private void ensurePreparation(String localeTag) {
        if (runtime.isPreparationActive(localeTag)) return;
        long generation = nextPreparationGeneration++;
        preparationGenerationByLocale.put(localeTag, generation);
        runtime.startPreparation(localeTag, generation, new AdvancedSpeechRuntime.PreparationListener() {
            @Override
            public void onCompleted(String completedLocale, long completedGeneration) {
                synchronized (AdvancedSpeechProviderManager.this) {
                    if (!ownsPreparation(completedLocale, completedGeneration)) return;
                    AdvancedSpeechCacheRecord current = store.read(completedLocale);
                    if (current == null || current.state != AdvancedSpeechCacheState.SUPPORTED_PREPARING) {
                        return;
                    }
                    store.write(AdvancedSpeechCacheRecord.ready(completedLocale, clock.nowMs()));
                    preparationGenerationByLocale.remove(completedLocale);
                    reconciledLocales.add(completedLocale);
                }
            }

            @Override
            public void onFailed(String failedLocale, long failedGeneration, Throwable error) {
                synchronized (AdvancedSpeechProviderManager.this) {
                    if (!ownsPreparation(failedLocale, failedGeneration)) return;
                    AdvancedSpeechCacheRecord current = store.read(failedLocale);
                    if (current == null || current.state != AdvancedSpeechCacheState.SUPPORTED_PREPARING) {
                        return;
                    }
                    store.write(AdvancedSpeechCacheRecord.unavailable(failedLocale, clock.nowMs()));
                    preparationGenerationByLocale.remove(failedLocale);
                }
            }
        });
    }

    private boolean ownsPreparation(String localeTag, long generation) {
        Long owned = preparationGenerationByLocale.get(localeTag);
        return owned != null && owned == generation;
    }

    private void invalidatePreparation(String localeTag) {
        preparationGenerationByLocale.remove(localeTag);
    }

    private static AdvancedSpeechDecision decision(
        SpeechInputProviderId providerId,
        AdvancedSpeechCacheState state,
        String localeTag,
        String cacheAccess,
        String statusSource,
        boolean probed
    ) {
        SpeechInputProviderId selected = state == AdvancedSpeechCacheState.SUPPORTED_READY
            ? SpeechInputProviderId.MLKIT_GENAI_ADVANCED
            : SpeechInputProviderId.ANDROID_ON_DEVICE;
        return new AdvancedSpeechDecision(
            selected,
            state,
            localeTag,
            cacheAccess,
            statusSource,
            probed
        );
    }

    static String normalizeLocaleTag(String languageOrNull) {
        if (languageOrNull == null || languageOrNull.isBlank()) {
            return Locale.US.toLanguageTag();
        }
        try {
            Locale locale = Locale.forLanguageTag(languageOrNull);
            String tag = locale.toLanguageTag();
            if (tag.isEmpty() || "und".equalsIgnoreCase(tag)) return Locale.US.toLanguageTag();
            return tag;
        } catch (RuntimeException error) {
            return Locale.US.toLanguageTag();
        }
    }
}
