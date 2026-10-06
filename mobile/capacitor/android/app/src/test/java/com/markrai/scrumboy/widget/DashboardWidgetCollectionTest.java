package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import org.junit.Test;

public class DashboardWidgetCollectionTest {
    private static DashboardWidgetSnapshot snapshot(int count) {
        List<DashboardWidgetSnapshot.Item> items = new ArrayList<>();
        for (int i = 1; i <= count; i++) {
            items.add(new DashboardWidgetSnapshot.Item(
                i,
                "Todo " + i,
                "Alpha",
                "alpha",
                "Doing",
                "#60a5fa",
                null,
                i % 2 == 0 ? "Sprint 1" : null
            ));
        }
        return new DashboardWidgetSnapshot("https://scrumboy.example", 3, 10, count, 1, items);
    }

    @Test
    public void populatedExposesTheFullSnapshotInOrder() {
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            snapshot(25)
        );
        List<DashboardWidgetSnapshot.Item> rows = DashboardWidgetCollection.rows(model);
        assertEquals(25, rows.size());
        assertEquals(1, rows.get(0).localId);
        assertEquals(25, rows.get(24).localId);
        assertEquals("/alpha/t/12", DashboardWidgetCollection.itemOpenPath(rows.get(11)));
        assertEquals(
            "/alpha/t/12",
            DashboardWidgetIntents.extraOpenPath(DashboardWidgetCollection.itemOpenPath(rows.get(11)))
        );
    }

    @Test
    public void privateAndEmptyStatesExposeZeroCollectionRows() {
        DashboardWidgetSnapshot owned = snapshot(4);
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            null, true, 3L, owned
        )).isEmpty());
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            "https://scrumboy.example", false, 3L, owned
        )).isEmpty());
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            "https://scrumboy.example", true, null, owned
        )).isEmpty());
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            "https://scrumboy.example", true, 3L, null
        )).isEmpty());
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            new DashboardWidgetSnapshot("https://scrumboy.example", 3, 10, 0, 0, List.of())
        )).isEmpty());
        assertTrue(DashboardWidgetCollection.rows(DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            new DashboardWidgetSnapshot("https://scrumboy.example", 3, 10, 4, 2, List.of())
        )).isEmpty());
    }
}
