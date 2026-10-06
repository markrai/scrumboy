package com.markrai.scrumboy.speech;

interface AdvancedSpeechPreferenceStore {
    String get(String key, String defaultValue);

    void put(String key, String value);

    void remove(String key);

    void clear();
}
