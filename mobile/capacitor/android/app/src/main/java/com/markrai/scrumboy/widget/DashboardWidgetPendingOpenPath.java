package com.markrai.scrumboy.widget;

import android.content.Context;
import android.content.SharedPreferences;

/**
 * Process- and plugin-independent holder for a sanitized widget open path.
 * MainActivity may write before Capacitor plugins exist; JS consumes after bootstrap.
 */
public final class DashboardWidgetPendingOpenPath {
    static final String KEY = "pending_open_path";

    interface Store {
        String get(String key);
        void put(String key, String value);
        void remove(String key);
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

        @Override
        public void put(String key, String value) {
            preferences.edit().putString(key, value).apply();
        }

        @Override
        public void remove(String key) {
            preferences.edit().remove(key).apply();
        }
    }

    private final Store store;

    public DashboardWidgetPendingOpenPath(Context context) {
        this(new SharedPreferencesStore(
            context.getSharedPreferences(DashboardWidgetSnapshotStore.PREFERENCES_NAME, Context.MODE_PRIVATE)
        ));
    }

    DashboardWidgetPendingOpenPath(Store store) {
        this.store = store;
    }

    public String offer(String raw) {
        String sanitized = DashboardWidgetOpenPath.sanitize(raw);
        if (sanitized == null) return null;
        store.put(KEY, sanitized);
        return sanitized;
    }

    public String peek() {
        return DashboardWidgetOpenPath.sanitize(store.get(KEY));
    }

    public String consume() {
        String sanitized = peek();
        clear();
        return sanitized;
    }

    public void clear() {
        store.remove(KEY);
    }
}
