package com.markrai.scrumboy.widget;

import java.util.Collections;
import java.util.List;

/** Privacy-safe collection rows for the Dashboard widget ListView. */
public final class DashboardWidgetCollection {
    private DashboardWidgetCollection() {}

    public static List<DashboardWidgetSnapshot.Item> rows(DashboardWidgetViewModel model) {
        if (model == null
            || model.state != DashboardWidgetViewModel.State.POPULATED
            || model.snapshot == null) {
            return Collections.emptyList();
        }
        return model.snapshot.items;
    }

    public static String itemOpenPath(DashboardWidgetSnapshot.Item item) {
        if (item == null) return DashboardWidgetOpenPath.DASHBOARD;
        String path = DashboardWidgetOpenPath.todo(item.projectSlug, item.localId);
        return path != null ? path : DashboardWidgetOpenPath.DASHBOARD;
    }
}
