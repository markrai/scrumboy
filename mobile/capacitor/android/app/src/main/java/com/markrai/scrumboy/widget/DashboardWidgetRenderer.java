package com.markrai.scrumboy.widget;

import android.appwidget.AppWidgetManager;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.text.format.DateUtils;
import android.view.View;
import android.widget.RemoteViews;
import com.markrai.scrumboy.R;
import com.markrai.scrumboy.transport.SelectedServerOrigin;
import com.markrai.scrumboy.transport.TransportAuthenticatedSession;

public final class DashboardWidgetRenderer {
    private DashboardWidgetRenderer() {}

    static RemoteViews render(Context context, int appWidgetId, Bundle options) {
        DashboardWidgetSnapshotStore store = new DashboardWidgetSnapshotStore(context);
        String selectedOrigin = new SelectedServerOrigin(context).read();
        boolean signedIn = TransportAuthenticatedSession.isPresent(context, selectedOrigin);
        DashboardWidgetSnapshot snapshot = store.loadSnapshot();
        DashboardWidgetViewModel model = DashboardWidgetViewModel.resolve(
            selectedOrigin,
            signedIn,
            store.currentUserId(),
            snapshot
        );
        if (model.discardSnapshot) {
            store.clearSnapshot();
        }

        int minWidth = optionDp(options, AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH, 110);
        int minHeight = optionDp(options, AppWidgetManager.OPTION_APPWIDGET_MIN_HEIGHT, 110);
        DashboardWidgetLayout layout = DashboardWidgetLayout.fromSize(minWidth, minHeight);
        if (layout.kind == DashboardWidgetLayout.Kind.COMPACT) {
            return renderCompact(context, model);
        }
        return renderMedium(context, appWidgetId, model);
    }

    private static RemoteViews renderCompact(Context context, DashboardWidgetViewModel model) {
        RemoteViews views = new RemoteViews(context.getPackageName(), R.layout.widget_dashboard_compact);
        views.setOnClickPendingIntent(
            R.id.widget_dashboard_root,
            DashboardWidgetIntents.open(context, DashboardWidgetOpenPath.DASHBOARD, 1)
        );
        views.setTextViewText(R.id.widget_dashboard_brand, context.getString(R.string.widget_dashboard_brand));
        views.setTextViewText(R.id.widget_dashboard_title, context.getString(R.string.widget_dashboard_title));
        String message = compactMessage(context, model);
        views.setTextViewText(R.id.widget_dashboard_body, message);
        views.setTextViewText(R.id.widget_dashboard_updated, updatedLabel(context, model));
        views.setContentDescription(R.id.widget_dashboard_root, compactDescription(context, model, message));
        return views;
    }

    private static RemoteViews renderMedium(Context context, int appWidgetId, DashboardWidgetViewModel model) {
        RemoteViews views = new RemoteViews(context.getPackageName(), R.layout.widget_dashboard_medium);
        views.setOnClickPendingIntent(
            R.id.widget_dashboard_header,
            DashboardWidgetIntents.open(context, DashboardWidgetOpenPath.DASHBOARD, 1)
        );
        views.setOnClickPendingIntent(
            R.id.widget_dashboard_message,
            DashboardWidgetIntents.open(context, DashboardWidgetOpenPath.DASHBOARD, 1)
        );
        views.setTextViewText(R.id.widget_dashboard_counts, countsLabel(context, model));
        views.setTextViewText(R.id.widget_dashboard_message, mediumMessage(context, model));
        boolean populated = model.state == DashboardWidgetViewModel.State.POPULATED;
        views.setViewVisibility(R.id.widget_dashboard_message, populated ? View.GONE : View.VISIBLE);
        views.setViewVisibility(R.id.widget_dashboard_list, populated ? View.VISIBLE : View.GONE);
        Intent serviceIntent = new Intent(context, DashboardWidgetRemoteViewsService.class);
        serviceIntent.putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, appWidgetId);
        serviceIntent.setData(Uri.parse(serviceIntent.toUri(Intent.URI_INTENT_SCHEME)));
        views.setRemoteAdapter(R.id.widget_dashboard_list, serviceIntent);
        views.setPendingIntentTemplate(R.id.widget_dashboard_list, DashboardWidgetIntents.collectionTemplate(context));
        views.setEmptyView(R.id.widget_dashboard_list, R.id.widget_dashboard_message);
        views.setTextViewText(R.id.widget_dashboard_updated, updatedLabel(context, model));
        views.setContentDescription(R.id.widget_dashboard_root, mediumDescription(context, model));
        return views;
    }

    private static String compactMessage(Context context, DashboardWidgetViewModel model) {
        switch (model.state) {
            case NO_SERVER:
                return context.getString(R.string.widget_dashboard_connect_server);
            case SIGNED_OUT:
                return context.getString(R.string.widget_dashboard_signed_out);
            case NOT_LOADED:
                return context.getString(R.string.widget_dashboard_not_loaded);
            case EMPTY:
                return context.getString(R.string.widget_dashboard_empty);
            case HAS_ASSIGNED:
            case POPULATED:
                return countsLabel(context, model);
            default:
                return context.getString(R.string.widget_dashboard_not_loaded);
        }
    }

    private static String mediumMessage(Context context, DashboardWidgetViewModel model) {
        switch (model.state) {
            case NO_SERVER:
                return context.getString(R.string.widget_dashboard_connect_server);
            case SIGNED_OUT:
                return context.getString(R.string.widget_dashboard_signed_out);
            case NOT_LOADED:
                return context.getString(R.string.widget_dashboard_not_loaded);
            case EMPTY:
                return context.getString(R.string.widget_dashboard_empty);
            case HAS_ASSIGNED:
                return context.getString(R.string.widget_dashboard_view_work);
            default:
                return "";
        }
    }

    private static String countsLabel(Context context, DashboardWidgetViewModel model) {
        if (!model.showsWorkContent() || model.snapshot == null) {
            return "";
        }
        return context.getString(
            R.string.widget_dashboard_counts,
            model.snapshot.assignedCount,
            model.snapshot.wipCount
        );
    }

    private static String updatedLabel(Context context, DashboardWidgetViewModel model) {
        if (!model.showsWorkContent() || model.snapshot == null) return "";
        CharSequence relative = DateUtils.getRelativeTimeSpanString(
            model.snapshot.fetchedAtMs,
            System.currentTimeMillis(),
            DateUtils.MINUTE_IN_MILLIS,
            DateUtils.FORMAT_ABBREV_RELATIVE
        );
        return context.getString(R.string.widget_dashboard_updated, relative);
    }

    private static String compactDescription(Context context, DashboardWidgetViewModel model, String body) {
        return context.getString(R.string.widget_dashboard_full_title) + ". " + body;
    }

    private static String mediumDescription(Context context, DashboardWidgetViewModel model) {
        String counts = countsLabel(context, model);
        String message = mediumMessage(context, model);
        if (!counts.isEmpty()) {
            return context.getString(R.string.widget_dashboard_full_title) + ". " + counts;
        }
        return context.getString(R.string.widget_dashboard_full_title) + ". " + message;
    }

    private static int optionDp(Bundle options, String key, int fallback) {
        if (options == null) return fallback;
        return Math.max(0, options.getInt(key, fallback));
    }
}
