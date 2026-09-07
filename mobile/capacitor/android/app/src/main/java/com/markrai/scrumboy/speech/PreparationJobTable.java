package com.markrai.scrumboy.speech;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Locale-keyed job table that removes only the exact generation that owns the slot.
 */
final class PreparationJobTable<T> {
    static final class Entry<T> {
        final long generation;
        final T job;

        Entry(long generation, T job) {
            this.generation = generation;
            this.job = job;
        }
    }

    private final ConcurrentHashMap<String, Entry<T>> jobs = new ConcurrentHashMap<>();

    Entry<T> install(String localeTag, long generation, T job) {
        return jobs.put(localeTag, new Entry<>(generation, job));
    }

    boolean removeExact(String localeTag, long generation) {
        AtomicBoolean removed = new AtomicBoolean(false);
        jobs.compute(localeTag, (key, current) -> {
            if (current != null && current.generation == generation) {
                removed.set(true);
                return null;
            }
            return current;
        });
        return removed.get();
    }

    Entry<T> removeAny(String localeTag) {
        return jobs.remove(localeTag);
    }

    List<Entry<T>> removeAll() {
        List<Entry<T>> drained = new ArrayList<>(jobs.values());
        jobs.clear();
        return drained;
    }

    Entry<T> get(String localeTag) {
        return jobs.get(localeTag);
    }

    boolean isActive(String localeTag, java.util.function.Predicate<T> active) {
        Entry<T> entry = jobs.get(localeTag);
        return entry != null && active.test(entry.job);
    }
}
