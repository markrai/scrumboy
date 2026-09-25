package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import org.junit.Test;

public class DashboardWidgetSnapshotStoreTest {
    private static DashboardWidgetSnapshot snapshot(String origin, long userId, String title) {
        return new DashboardWidgetSnapshot(
            origin,
            userId,
            1_700_000_000_000L,
            2,
            1,
            List.of(new DashboardWidgetSnapshot.Item(12, title, "Alpha", "alpha", "In progress", "#60a5fa", 3L, "Sprint 1"))
        );
    }

    @Test
    public void serializesAndDeserializesSanitizedSnapshot() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(7);
        assertTrue(store.saveSnapshot(snapshot("https://scrumboy.example", 7, "Fix login")));
        DashboardWidgetSnapshot loaded = store.loadSnapshot();
        assertNotNull(loaded);
        assertEquals("https://scrumboy.example", loaded.ownerOrigin);
        assertEquals(7, loaded.userId);
        assertEquals(2, loaded.assignedCount);
        assertEquals(1, loaded.wipCount);
        assertEquals(1, loaded.items.size());
        assertEquals(12, loaded.items.get(0).localId);
        assertEquals("Fix login", loaded.items.get(0).title);
        assertEquals("alpha", loaded.items.get(0).projectSlug);
        assertEquals("#60a5fa", loaded.items.get(0).statusColor);
        assertEquals(Long.valueOf(3L), loaded.items.get(0).estimationPoints);
        assertEquals("Sprint 1", loaded.items.get(0).sprintName);
    }

    @Test
    public void rejectsUnknownSchemaAndCorruptJson() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        memory.putString(DashboardWidgetSnapshotStore.SNAPSHOT_KEY, "{\"schemaVersion\":99,\"ownerOrigin\":\"https://x\",\"userId\":1,\"fetchedAtMs\":1,\"assignedCount\":0,\"wipCount\":0,\"items\":[]}");
        assertNull(new DashboardWidgetSnapshotStore(memory).loadSnapshot());
        memory.putString(DashboardWidgetSnapshotStore.SNAPSHOT_KEY, "{not-json");
        assertNull(new DashboardWidgetSnapshotStore(memory).loadSnapshot());
    }

    @Test
    public void truncatesTitlesButNotAssignedItemCount() {
        StringBuilder title = new StringBuilder();
        for (int i = 0; i < 200; i++) title.append('a');
        List<DashboardWidgetSnapshot.Item> items = new ArrayList<>();
        for (int i = 1; i <= 25; i++) {
            items.add(item(i, i == 1 ? title.toString() : "t" + i));
        }
        DashboardWidgetSnapshot snapshot = DashboardWidgetSnapshot.sanitize(
            new DashboardWidgetSnapshot("https://scrumboy.example", 1, 10, 25, 5, items)
        );
        assertNotNull(snapshot);
        assertEquals(25, snapshot.items.size());
        assertEquals(DashboardWidgetSnapshot.MAX_TITLE_CHARS, snapshot.items.get(0).title.length());
        assertEquals(25, snapshot.items.get(24).localId);
    }

    @Test
    public void storesTwentyFiveAssignedTodosInOrder() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(1);
        List<DashboardWidgetSnapshot.Item> items = new ArrayList<>();
        for (int i = 1; i <= 25; i++) items.add(item(i, "Todo " + i));
        DashboardWidgetSnapshot snapshot = new DashboardWidgetSnapshot(
            "https://scrumboy.example",
            1,
            10,
            25,
            5,
            items
        );
        assertTrue(store.saveSnapshot(snapshot));
        DashboardWidgetSnapshot loaded = store.loadSnapshot();
        assertNotNull(loaded);
        assertEquals(25, loaded.items.size());
        assertEquals(1, loaded.items.get(0).localId);
        assertEquals("Todo 25", loaded.items.get(24).title);
    }

    @Test
    public void failedSaveKeepsPreviousCompleteSnapshot() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(1);
        assertTrue(store.saveSnapshot(snapshot("https://scrumboy.example", 1, "Complete")));
        memory.failNextSnapshotWrite = true;
        List<DashboardWidgetSnapshot.Item> items = new ArrayList<>();
        for (int i = 1; i <= 25; i++) items.add(item(i, "Partial " + i));
        assertFalse(store.saveSnapshot(new DashboardWidgetSnapshot(
            "https://scrumboy.example",
            1,
            11,
            25,
            5,
            items
        )));
        assertEquals("Complete", store.loadSnapshot().items.get(0).title);
        assertEquals(1, store.loadSnapshot().items.size());
    }

    @Test
    public void saveRequiresIndependentCurrentUserMatch() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        assertFalse(store.saveSnapshot(snapshot("https://scrumboy.example", 7, "Secret")));
        store.setCurrentUserId(8);
        assertFalse(store.saveSnapshot(snapshot("https://scrumboy.example", 7, "Secret")));
        assertNull(store.loadSnapshot());
        assertTrue(store.saveSnapshot(snapshot("https://scrumboy.example", 8, "Ok")));
        assertEquals("Ok", store.loadSnapshot().items.get(0).title);
    }

    @Test
    public void changingCurrentUserClearsPreviousSnapshot() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(1);
        assertTrue(store.saveSnapshot(snapshot("https://scrumboy.example", 1, "User one")));
        store.setCurrentUserId(2);
        assertNull(store.loadSnapshot());
        assertEquals(Long.valueOf(2L), store.currentUserId());
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            store.currentUserId(),
            store.loadSnapshot()
        )).isEmpty());
    }

    @Test
    public void clearAllRemovesSnapshotAndCurrentUser() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(memory);
        store.setCurrentUserId(1);
        store.saveSnapshot(snapshot("https://scrumboy.example", 1, "Keep"));
        store.clearAll();
        assertNull(store.loadSnapshot());
        assertNull(store.currentUserId());
    }

    @Test
    public void dropsUnsafeStatusColors() {
        DashboardWidgetSnapshot snapshot = DashboardWidgetSnapshot.sanitize(snapshot("https://scrumboy.example", 1, "X"));
        assertEquals("#60a5fa", snapshot.items.get(0).statusColor);
        DashboardWidgetSnapshot unsafe = DashboardWidgetSnapshot.sanitize(
            new DashboardWidgetSnapshot(
                "https://scrumboy.example",
                1,
                10,
                1,
                1,
                List.of(new DashboardWidgetSnapshot.Item(1, "X", "A", "a", "Doing", "red", null, null))
            )
        );
        assertNull(unsafe.items.get(0).statusColor);
    }

    private static DashboardWidgetSnapshot.Item item(long id, String title) {
        return new DashboardWidgetSnapshot.Item(id, title, "P", "p", "Doing", null, null, null);
    }
}
