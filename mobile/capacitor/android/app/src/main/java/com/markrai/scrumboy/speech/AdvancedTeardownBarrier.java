package com.markrai.scrumboy.speech;

import java.util.ArrayList;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;

/**
 * Cross-provider barrier: every platform SpeechRecognizer acquisition waits for
 * every still-outstanding Advanced native teardown, not only the newest one.
 *
 * Each registered future is
 * {@link MlKitAdvancedSpeechRuntime.RecognitionHandle#teardownFuture()}, which settles
 * only after stopRecognition, close, and Advanced session-gate release.
 *
 * Registration is identity-keyed: a newer session cannot hide an older unresolved
 * session, and an older owner cannot remove a different owner. Settled owners are
 * removed so the outstanding set stays bounded by live native teardown work.
 */
final class AdvancedTeardownBarrier {
    private final Object lock = new Object();
    private final Map<Object, CompletableFuture<Void>> outstanding = new IdentityHashMap<>();

    void register(Object owner, CompletableFuture<Void> teardown) {
        if (owner == null || teardown == null) return;
        synchronized (lock) {
            outstanding.put(owner, teardown);
        }
        teardown.whenComplete((ignored, error) -> clearExact(owner));
    }

    /**
     * Snapshot of all outstanding teardowns. Waits for settlement rather than success:
     * an exceptionally settled teardown neither releases the barrier early nor blocks it
     * forever while another session is still closing.
     */
    CompletableFuture<Void> currentBarrier() {
        final List<CompletableFuture<Void>> snapshot;
        synchronized (lock) {
            if (outstanding.isEmpty()) return CompletableFuture.completedFuture(null);
            snapshot = new ArrayList<>(outstanding.values());
        }
        CompletableFuture<?>[] settlements = new CompletableFuture<?>[snapshot.size()];
        for (int index = 0; index < snapshot.size(); index += 1) {
            settlements[index] = snapshot.get(index).handle((ignored, error) -> null);
        }
        return CompletableFuture.allOf(settlements);
    }

    void clearExact(Object owner) {
        if (owner == null) return;
        synchronized (lock) {
            outstanding.remove(owner);
        }
    }

    int outstandingCount() {
        synchronized (lock) {
            return outstanding.size();
        }
    }
}
