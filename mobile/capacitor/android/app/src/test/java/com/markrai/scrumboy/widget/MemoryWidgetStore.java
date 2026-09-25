package com.markrai.scrumboy.widget;

import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;

final class MemoryWidgetStore implements DashboardWidgetSnapshotStore.Store, DashboardWidgetPendingOpenPath.Store {
    private final Map<String, String> strings = new HashMap<>();
    private final Map<String, Long> longs = new HashMap<>();
    private final Set<String> present = new HashSet<>();
    boolean failNextSnapshotWrite;

    @Override
    public String getString(String key, String fallback) {
        return strings.containsKey(key) ? strings.get(key) : fallback;
    }

    @Override
    public long getLong(String key, long fallback) {
        return longs.containsKey(key) ? longs.get(key) : fallback;
    }

    @Override
    public boolean contains(String key) {
        return present.contains(key);
    }

    @Override
    public void putString(String key, String value) {
        strings.put(key, value);
        longs.remove(key);
        present.add(key);
    }

    @Override
    public void putLong(String key, long value) {
        longs.put(key, value);
        strings.remove(key);
        present.add(key);
    }

    @Override
    public void remove(String key) {
        strings.remove(key);
        longs.remove(key);
        present.remove(key);
    }

    @Override
    public String get(String key) {
        return strings.get(key);
    }

    @Override
    public void put(String key, String value) {
        putString(key, value);
    }

    @Override
    public String readSnapshotJson() {
        return getString(DashboardWidgetSnapshotStore.SNAPSHOT_KEY, null);
    }

    @Override
    public boolean writeSnapshotJson(String json) {
        if (failNextSnapshotWrite) {
            failNextSnapshotWrite = false;
            return false;
        }
        putString(DashboardWidgetSnapshotStore.SNAPSHOT_KEY, json);
        return true;
    }

    @Override
    public void deleteSnapshotJson() {
        remove(DashboardWidgetSnapshotStore.SNAPSHOT_KEY);
    }
}
