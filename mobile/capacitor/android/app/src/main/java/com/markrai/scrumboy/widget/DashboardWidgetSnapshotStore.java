package com.markrai.scrumboy.widget;

import android.content.Context;
import android.content.SharedPreferences;

/**
 * Persists a sanitized Dashboard widget snapshot and an independently stored current-user id.
 * Snapshot userId must be checked against {@link #currentUserId()}, never against itself.
 */
public final class DashboardWidgetSnapshotStore {
    static final String PREFERENCES_NAME = "scrumboy_dashboard_widget_v1";
    static final String SNAPSHOT_KEY = "snapshot";
    static final String CURRENT_USER_ID_KEY = "current_user_id";

    interface Store {
        String getString(String key, String fallback);
        long getLong(String key, long fallback);
        boolean contains(String key);
        void putString(String key, String value);
        void putLong(String key, long value);
        void remove(String key);
    }

    private static final class SharedPreferencesStore implements Store {
        private final SharedPreferences preferences;

        SharedPreferencesStore(SharedPreferences preferences) {
            this.preferences = preferences;
        }

        @Override
        public String getString(String key, String fallback) {
            return preferences.getString(key, fallback);
        }

        @Override
        public long getLong(String key, long fallback) {
            return preferences.getLong(key, fallback);
        }

        @Override
        public boolean contains(String key) {
            return preferences.contains(key);
        }

        @Override
        public void putString(String key, String value) {
            preferences.edit().putString(key, value).apply();
        }

        @Override
        public void putLong(String key, long value) {
            preferences.edit().putLong(key, value).apply();
        }

        @Override
        public void remove(String key) {
            preferences.edit().remove(key).apply();
        }
    }

    private final Store store;

    public DashboardWidgetSnapshotStore(Context context) {
        this(new SharedPreferencesStore(context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)));
    }

    DashboardWidgetSnapshotStore(Store store) {
        this.store = store;
    }

    public synchronized Long currentUserId() {
        if (!store.contains(CURRENT_USER_ID_KEY)) return null;
        long value = store.getLong(CURRENT_USER_ID_KEY, 0);
        return value > 0 ? value : null;
    }

    public synchronized void setCurrentUserId(long userId) {
        Long previous = currentUserId();
        if (userId <= 0) {
            clearAll();
            return;
        }
        if (previous != null && previous != userId) {
            clearSnapshot();
            clearPendingOpenPath();
        }
        store.putLong(CURRENT_USER_ID_KEY, userId);
    }

    public synchronized boolean saveSnapshot(DashboardWidgetSnapshot snapshot) {
        DashboardWidgetSnapshot sanitized = DashboardWidgetSnapshot.sanitize(snapshot);
        Long currentUserId = currentUserId();
        if (sanitized == null || currentUserId == null || sanitized.userId != currentUserId) {
            return false;
        }
        try {
            store.putString(SNAPSHOT_KEY, sanitized.toJson());
            return true;
        } catch (Exception error) {
            return false;
        }
    }

    public synchronized DashboardWidgetSnapshot loadSnapshot() {
        return DashboardWidgetSnapshot.parse(store.getString(SNAPSHOT_KEY, null));
    }

    public synchronized void clearSnapshot() {
        store.remove(SNAPSHOT_KEY);
    }

    public synchronized void clearAll() {
        store.remove(SNAPSHOT_KEY);
        store.remove(CURRENT_USER_ID_KEY);
        clearPendingOpenPath();
    }

    private void clearPendingOpenPath() {
        store.remove(DashboardWidgetPendingOpenPath.KEY);
    }
}
