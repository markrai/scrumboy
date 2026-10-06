package com.markrai.scrumboy.speech;

/**
 * Runtime seam for ML Kit GenAI Advanced speech capability/preparation.
 * Device model identity is intentionally absent from this contract.
 */
interface AdvancedSpeechRuntime {
    int FEATURE_AVAILABLE = 1;
    int FEATURE_DOWNLOADABLE = 2;
    int FEATURE_DOWNLOADING = 3;
    int FEATURE_UNAVAILABLE = 4;

    int probeAdvancedStatus(String localeTag) throws Exception;

    void startPreparation(String localeTag, long generation, PreparationListener listener);

    boolean isPreparationActive(String localeTag);

    void cancelPreparation(String localeTag);

    void cancelAllPreparations();

    void close();

    interface PreparationListener {
        void onCompleted(String localeTag, long generation);

        void onFailed(String localeTag, long generation, Throwable error);
    }
}
