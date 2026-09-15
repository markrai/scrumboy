package com.markrai.scrumboy.transport;

import android.content.Context;
import android.content.SharedPreferences;

/**
 * Authoritative selected Scrumboy server origin as stored by Capacitor Preferences.
 * Cookie-jar owner origin is a separate concept and must not be used as "server configured".
 */
public final class SelectedServerOrigin {
    public static final String PREFERENCES_NAME = "CapacitorStorage";
    public static final String KEY = "scrumboy.server.origin.v1";

    interface Store {
        String get(String key);
    }

    private static final class SharedPreferencesStore implements Store {
        private final SharedPreferences preferences;

        SharedPreferencesStore(SharedPreferences preferences) {
            this.preferences = preferences;
        }

        @Override
        public String get(String key) {
            return preferences.getString(key, null);
        }
    }

    private final Store store;

    public SelectedServerOrigin(Context context) {
        this(new SharedPreferencesStore(context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)));
    }

    SelectedServerOrigin(Store store) {
        this.store = store;
    }

    public String read() {
        String value = store.get(KEY);
        if (value == null) return null;
        String trimmed = value.trim();
        return trimmed.isEmpty() ? null : trimmed;
    }
}
