package com.markrai.scrumboy.speech;

import java.util.HashMap;
import java.util.Map;

final class InMemoryAdvancedSpeechPreferenceStore implements AdvancedSpeechPreferenceStore {
    private final Map<String, String> values = new HashMap<>();

    @Override
    public String get(String key, String defaultValue) {
        return values.getOrDefault(key, defaultValue);
    }

    @Override
    public void put(String key, String value) {
        values.put(key, value);
    }

    @Override
    public void remove(String key) {
        values.remove(key);
    }

    @Override
    public void clear() {
        values.clear();
    }
}
