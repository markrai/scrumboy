package com.markrai.scrumboy.speech;

import android.content.Context;
import android.content.SharedPreferences;

final class SharedPreferencesAdvancedSpeechStore implements AdvancedSpeechPreferenceStore {
    static final String PREFS_NAME = "scrumboy_advanced_speech";

    private final SharedPreferences preferences;

    SharedPreferencesAdvancedSpeechStore(Context context) {
        this.preferences = context.getApplicationContext()
            .getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
    }

    @Override
    public String get(String key, String defaultValue) {
        return preferences.getString(key, defaultValue);
    }

    @Override
    public void put(String key, String value) {
        preferences.edit().putString(key, value).apply();
    }

    @Override
    public void remove(String key) {
        preferences.edit().remove(key).apply();
    }

    @Override
    public void clear() {
        preferences.edit().clear().apply();
    }
}
