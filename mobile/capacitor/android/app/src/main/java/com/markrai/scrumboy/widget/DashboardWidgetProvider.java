package com.markrai.scrumboy.widget;

import android.appwidget.AppWidgetManager;
import android.appwidget.AppWidgetProvider;
import android.content.Context;
import android.os.Bundle;
import com.markrai.scrumboy.R;

public final class DashboardWidgetProvider extends AppWidgetProvider {
    @Override
    public void onUpdate(Context context, AppWidgetManager appWidgetManager, int[] appWidgetIds) {
        updateAppWidgets(context, appWidgetManager, appWidgetIds);
        appWidgetManager.notifyAppWidgetViewDataChanged(appWidgetIds, R.id.widget_dashboard_list);
    }

    @Override
    public void onAppWidgetOptionsChanged(
        Context context,
        AppWidgetManager appWidgetManager,
        int appWidgetId,
        Bundle newOptions
    ) {
        appWidgetManager.updateAppWidget(appWidgetId, DashboardWidgetRenderer.render(context, appWidgetId, newOptions));
        appWidgetManager.notifyAppWidgetViewDataChanged(appWidgetId, R.id.widget_dashboard_list);
    }

    static void updateAppWidgets(Context context, AppWidgetManager appWidgetManager, int[] appWidgetIds) {
        for (int appWidgetId : appWidgetIds) {
            Bundle options = appWidgetManager.getAppWidgetOptions(appWidgetId);
            appWidgetManager.updateAppWidget(appWidgetId, DashboardWidgetRenderer.render(context, appWidgetId, options));
        }
    }
}
