package com.markrai.scrumboy.widget;

/** Privacy-safe view model for the Dashboard widget. Hides titles when ownership is uncertain. */
public final class DashboardWidgetViewModel {
    public enum State {
        NO_SERVER,
        SIGNED_OUT,
        NOT_LOADED,
        EMPTY,
        HAS_ASSIGNED,
        POPULATED
    }

    public final State state;
    public final boolean discardSnapshot;
    public final DashboardWidgetSnapshot snapshot;

    DashboardWidgetViewModel(State state, boolean discardSnapshot, DashboardWidgetSnapshot snapshot) {
        this.state = state;
        this.discardSnapshot = discardSnapshot;
        this.snapshot = snapshot;
    }

    public static DashboardWidgetViewModel resolve(
        String selectedOrigin,
        boolean hasAuthenticatedSession,
        Long currentUserId,
        DashboardWidgetSnapshot snapshot
    ) {
        if (selectedOrigin == null || selectedOrigin.trim().isEmpty()) {
            return new DashboardWidgetViewModel(State.NO_SERVER, snapshot != null, null);
        }
        if (!hasAuthenticatedSession) {
            return new DashboardWidgetViewModel(State.SIGNED_OUT, snapshot != null, null);
        }
        if (currentUserId == null || currentUserId <= 0) {
            return new DashboardWidgetViewModel(State.NOT_LOADED, snapshot != null, null);
        }
        if (snapshot == null) {
            return new DashboardWidgetViewModel(State.NOT_LOADED, false, null);
        }
        if (!selectedOrigin.trim().equals(snapshot.ownerOrigin) || snapshot.userId != currentUserId) {
            return new DashboardWidgetViewModel(State.NOT_LOADED, true, null);
        }
        if (snapshot.assignedCount == 0) {
            return new DashboardWidgetViewModel(State.EMPTY, false, snapshot);
        }
        if (snapshot.items.isEmpty()) {
            return new DashboardWidgetViewModel(State.HAS_ASSIGNED, false, snapshot);
        }
        return new DashboardWidgetViewModel(State.POPULATED, false, snapshot);
    }

    public boolean showsWorkContent() {
        return state == State.EMPTY || state == State.HAS_ASSIGNED || state == State.POPULATED;
    }
}
