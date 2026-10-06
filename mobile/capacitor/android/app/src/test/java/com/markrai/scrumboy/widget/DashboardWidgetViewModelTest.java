package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.List;
import org.junit.Test;

public class DashboardWidgetViewModelTest {
    private static DashboardWidgetSnapshot snapshot(String origin, long userId, boolean withItem) {
        List<DashboardWidgetSnapshot.Item> items = withItem
            ? List.of(new DashboardWidgetSnapshot.Item(9, "Private title", "Alpha", "alpha", "Doing", null, null, null))
            : List.of();
        return new DashboardWidgetSnapshot(origin, userId, 10, withItem ? 1 : 0, withItem ? 1 : 0, items);
    }

    @Test
    public void missingSelectedOriginIsConnectServerAndHidesTitles() {
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            null,
            true,
            1L,
            snapshot("https://old.example", 1, true)
        );
        assertEquals(DashboardWidgetViewModel.State.NO_SERVER, model.state);
        assertTrue(model.discardSnapshot);
        assertFalse(model.showsWorkContent());
        assertNull(model.snapshot);
    }

    @Test
    public void originWithoutAuthenticatedSessionIsSignedOutAndHidesTitles() {
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            false,
            1L,
            snapshot("https://scrumboy.example", 1, true)
        );
        assertEquals(DashboardWidgetViewModel.State.SIGNED_OUT, model.state);
        assertTrue(model.discardSnapshot);
        assertNull(model.snapshot);
    }

    @Test
    public void originMismatchHidesPreviousServerWork() {
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://new.example",
            true,
            1L,
            snapshot("https://old.example", 1, true)
        );
        assertEquals(DashboardWidgetViewModel.State.NOT_LOADED, model.state);
        assertTrue(model.discardSnapshot);
        assertNull(model.snapshot);
    }

    @Test
    public void snapshotCannotValidateUserAgainstItself() {
        DashboardWidgetSnapshot otherUser = snapshot("https://scrumboy.example", 99, true);
        DashboardWidgetViewModel missingCurrentUser = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            null,
            otherUser
        );
        assertEquals(DashboardWidgetViewModel.State.NOT_LOADED, missingCurrentUser.state);
        assertTrue(missingCurrentUser.discardSnapshot);
        assertNull(missingCurrentUser.snapshot);

        DashboardWidgetViewModel mismatched = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            1L,
            otherUser
        );
        assertEquals(DashboardWidgetViewModel.State.NOT_LOADED, mismatched.state);
        assertTrue(mismatched.discardSnapshot);
        assertNull(mismatched.snapshot);
    }

    @Test
    public void matchingOwnershipShowsEmptyOrPopulatedWork() {
        DashboardWidgetViewModel empty = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            snapshot("https://scrumboy.example", 3, false)
        );
        assertEquals(DashboardWidgetViewModel.State.EMPTY, empty.state);
        assertTrue(empty.showsWorkContent());

        DashboardWidgetViewModel populated = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            snapshot("https://scrumboy.example", 3, true)
        );
        assertEquals(DashboardWidgetViewModel.State.POPULATED, populated.state);
        assertEquals("Private title", populated.snapshot.items.get(0).title);
    }

    @Test
    public void assignedCountWithoutRenderableRowsIsNotEmptyWork() {
        DashboardWidgetSnapshot snapshot = new DashboardWidgetSnapshot(
            "https://scrumboy.example",
            3,
            10,
            4,
            2,
            List.of()
        );
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            snapshot
        );
        assertEquals(DashboardWidgetViewModel.State.HAS_ASSIGNED, model.state);
        assertTrue(model.showsWorkContent());
        assertEquals(4, model.snapshot.assignedCount);
        assertEquals(2, model.snapshot.wipCount);
        assertTrue(model.snapshot.items.isEmpty());
        assertFalse(model.discardSnapshot);
    }

    @Test
    public void missingSnapshotIsNotEmptyWork() {
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            "https://scrumboy.example",
            true,
            3L,
            null
        );
        assertEquals(DashboardWidgetViewModel.State.NOT_LOADED, model.state);
        assertFalse(model.discardSnapshot);
    }
}
