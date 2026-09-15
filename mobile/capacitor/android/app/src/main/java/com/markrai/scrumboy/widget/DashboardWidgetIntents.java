package com.markrai.scrumboy.widget;

import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import com.markrai.scrumboy.MainActivity;

public final class DashboardWidgetIntents {
    public static final String EXTRA_OPEN_PATH = "com.markrai.scrumboy.extra.OPEN_PATH";
    static final String ACTION_OPEN = "com.markrai.scrumboy.action.WIDGET_OPEN";

    private DashboardWidgetIntents() {}

    static PendingIntent open(Context context, String path, int requestCode) {
        String sanitized = DashboardWidgetOpenPath.sanitize(path);
        if (sanitized == null) sanitized = DashboardWidgetOpenPath.DASHBOARD;
        Intent intent = new Intent(context, MainActivity.class);
        intent.setAction(ACTION_OPEN);
        intent.putExtra(EXTRA_OPEN_PATH, sanitized);
        intent.setData(Uri.parse("scrumboy-internal://widget" + sanitized));
        intent.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        int flags = PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE;
        return PendingIntent.getActivity(context, requestCode, intent, flags);
    }
}
