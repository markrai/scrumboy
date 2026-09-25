package com.markrai.scrumboy.widget;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.StandardCopyOption;

/**
 * Persists a sanitized Dashboard widget snapshot and an independently stored current-user id.
 * Snapshot JSON lives in an app-private file so an unbounded assigned-work collection
 * is not forced through SharedPreferences. Snapshot userId must be checked against
 * {@link #currentUserId()}, never against itself.
 */
public final class DashboardWidgetSnapshotStore {
    static final String PREFERENCES_NAME = "scrumboy_dashboard_widget_v1";
    static final String SNAPSHOT_KEY = "snapshot";
    static final String CURRENT_USER_ID_KEY = "current_user_id";
    static final String SNAPSHOT_FILE_NAME = "scrumboy_dashboard_widget_snapshot.json";

    interface Store {
        String getString(String key, String fallback);
        long getLong(String key, long fallback);
        boolean contains(String key);
        void putString(String key, String value);
        void putLong(String key, long value);
        void remove(String key);
        String readSnapshotJson();
        boolean writeSnapshotJson(String json);
        void deleteSnapshotJson();
    }

    private static final class SharedPreferencesMetadata implements Store {
        private final SharedPreferences preferences;
        private final File snapshotFile;

        SharedPreferencesMetadata(SharedPreferences preferences, File snapshotFile) {
            this.preferences = preferences;
            this.snapshotFile = snapshotFile;
            if (preferences.contains(SNAPSHOT_KEY)) {
                preferences.edit().remove(SNAPSHOT_KEY).apply();
            }
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

        @Override
        public String readSnapshotJson() {
            if (snapshotFile == null || !snapshotFile.isFile()) return null;
            try {
                return new String(Files.readAllBytes(snapshotFile.toPath()), StandardCharsets.UTF_8);
            } catch (IOException error) {
                return null;
            }
        }

        @Override
        public boolean writeSnapshotJson(String json) {
            if (snapshotFile == null || json == null) return false;
            File tmp = new File(snapshotFile.getAbsolutePath() + ".tmp");
            try {
                File parent = snapshotFile.getParentFile();
                if (parent != null) Files.createDirectories(parent.toPath());
                Files.write(tmp.toPath(), json.getBytes(StandardCharsets.UTF_8));
                try {
                    Files.move(
                        tmp.toPath(),
                        snapshotFile.toPath(),
                        StandardCopyOption.ATOMIC_MOVE,
                        StandardCopyOption.REPLACE_EXISTING
                    );
                } catch (AtomicMoveNotSupportedException error) {
                    Files.move(tmp.toPath(), snapshotFile.toPath(), StandardCopyOption.REPLACE_EXISTING);
                }
                return true;
            } catch (IOException error) {
                try {
                    Files.deleteIfExists(tmp.toPath());
                } catch (IOException ignored) {
                    // Previous complete snapshot file is left in place.
                }
                return false;
            }
        }

        @Override
        public void deleteSnapshotJson() {
            if (snapshotFile == null) return;
            try {
                Files.deleteIfExists(snapshotFile.toPath());
                Files.deleteIfExists(new File(snapshotFile.getAbsolutePath() + ".tmp").toPath());
            } catch (IOException ignored) {
                // Best-effort delete; metadata clear still proceeds.
            }
        }
    }

    private final Store store;

    public DashboardWidgetSnapshotStore(Context context) {
        this(new SharedPreferencesMetadata(
            context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE),
            new File(context.getFilesDir(), SNAPSHOT_FILE_NAME)
        ));
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
            return store.writeSnapshotJson(sanitized.toJson());
        } catch (Exception error) {
            return false;
        }
    }

    public synchronized DashboardWidgetSnapshot loadSnapshot() {
        return DashboardWidgetSnapshot.parse(store.readSnapshotJson());
    }

    public synchronized void clearSnapshot() {
        store.deleteSnapshotJson();
        store.remove(SNAPSHOT_KEY);
    }

    public synchronized void clearAll() {
        clearSnapshot();
        store.remove(CURRENT_USER_ID_KEY);
        clearPendingOpenPath();
    }

    private void clearPendingOpenPath() {
        store.remove(DashboardWidgetPendingOpenPath.KEY);
    }
}
