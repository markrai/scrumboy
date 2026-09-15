package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.List;
import org.junit.Test;

public class DashboardWidgetPrivacyLifecycleTest {
    private static DashboardWidgetSnapshot snapshot(String origin, long userId) {
        return new DashboardWidgetSnapshot(
            origin,
            userId,
            99,
            1,
            1,
            List.of(new DashboardWidgetSnapshot.Item(4, "Secret title", "Alpha", "alpha", "Doing", null, null, null))
        );
    }

    @Test
    public void logoutClearsSnapshotAndIndependentUserId() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(11);
        assertTrue(store.saveSnapshot(snapshot("https://scrumboy.example", 11)));
        store.clearAll();
        assertNull(store.loadSnapshot());
        assertNull(store.currentUserId());
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            false,
            store.currentUserId(),
            store.loadSnapshot()
        );
        assertEquals(DashboardWidgetViewModel.State.SIGNED_OUT, model.state);
        assertNull(model.snapshot);
    }

    @Test
    public void serverChangeLeavesNoPreviousOriginTitles() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(11);
        assertTrue(store.saveSnapshot(snapshot("https://old.example", 11)));
        store.clearAll();
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://new.example",
            true,
            store.currentUserId(),
            snapshot("https://old.example", 11)
        );
        assertEquals(DashboardWidgetViewModel.State.NOT_LOADED, model.state);
        assertTrue(model.discardSnapshot);
        assertNull(model.snapshot);
    }

    @Test
    public void clearAllRemovesPendingOpenPathFromPreviousSession() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        DashboardWidgetPendingOpenPath pending = new DashboardWidgetPendingOpenPath(memory);
        store.setCurrentUserId(11);
        assertTrue(store.saveSnapshot(snapshot("https://scrumboy.example", 11)));
        assertEquals("/alpha/t/12", pending.offer("/alpha/t/12"));
        store.clearAll();
        assertNull(store.loadSnapshot());
        assertNull(store.currentUserId());
        assertNull(pending.peek());
    }
}
