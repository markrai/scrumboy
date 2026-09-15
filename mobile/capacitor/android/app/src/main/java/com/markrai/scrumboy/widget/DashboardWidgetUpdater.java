package com.markrai.scrumboy.widget;

import android.appwidget.AppWidgetManager;
import android.content.ComponentName;
import android.content.Context;

public final class DashboardWidgetUpdater {
    private DashboardWidgetUpdater() {}

    public static void notifyAll(Context context) {
        AppWidgetManager manager = AppWidgetManager.getInstance(context);
        ComponentName component = new ComponentName(context, DashboardWidgetProvider.class);
        int[] ids = manager.getAppWidgetIds(component);
        if (ids == null || ids.length == 0) return;
        DashboardWidgetProvider.updateAppWidgets(context, manager, ids);
    }
}
