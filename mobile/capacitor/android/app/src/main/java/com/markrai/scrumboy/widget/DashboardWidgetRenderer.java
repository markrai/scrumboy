package com.markrai.scrumboy.widget;

import android.appwidget.AppWidgetManager;
import android.content.Context;
import android.graphics.Color;
import android.os.Bundle;
import android.text.format.DateUtils;
import android.view.View;
import android.widget.RemoteViews;
import com.markrai.scrumboy.R;
import com.markrai.scrumboy.transport.SelectedServerOrigin;
import com.markrai.scrumboy.transport.TransportAuthenticatedSession;
import java.util.Collections;
import java.util.List;

public final class DashboardWidgetRenderer {
    private static final int[] ROW_IDS = {
        R.id.widget_dashboard_row_1,
        R.id.widget_dashboard_row_2,
        R.id.widget_dashboard_row_3,
    };
    private static final int[] TITLE_IDS = {
        R.id.widget_dashboard_row_1_title,
        R.id.widget_dashboard_row_2_title,
        R.id.widget_dashboard_row_3_title,
    };
    private static final int[] META_IDS = {
        R.id.widget_dashboard_row_1_meta,
        R.id.widget_dashboard_row_2_meta,
        R.id.widget_dashboard_row_3_meta,
    };
    private static final int[] STATUS_IDS = {
        R.id.widget_dashboard_row_1_status,
        R.id.widget_dashboard_row_2_status,
        R.id.widget_dashboard_row_3_status,
    };
    private static final int[] ACCENT_IDS = {
        R.id.widget_dashboard_row_1_accent,
        R.id.widget_dashboard_row_2_accent,
        R.id.widget_dashboard_row_3_accent,
    };

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
        return renderMedium(context, model, layout.visibleRows);
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

    private static RemoteViews renderMedium(Context context, DashboardWidgetViewModel model, int visibleRows) {
        RemoteViews views = new RemoteViews(context.getPackageName(), R.layout.widget_dashboard_medium);
        views.setOnClickPendingIntent(
            R.id.widget_dashboard_header,
            DashboardWidgetIntents.open(context, DashboardWidgetOpenPath.DASHBOARD, 1)
        );
        views.setOnClickPendingIntent(
            R.id.widget_dashboard_message,
            DashboardWidgetIntents.open(context, DashboardWidgetOpenPath.DASHBOARD, 1)
        );
        views.setTextViewText(R.id.widget_dashboard_title, context.getString(R.string.widget_dashboard_full_title));
        views.setTextViewText(R.id.widget_dashboard_counts, countsLabel(context, model));
        views.setTextViewText(R.id.widget_dashboard_message, mediumMessage(context, model));
        boolean showMessage = model.state != DashboardWidgetViewModel.State.POPULATED;
        views.setViewVisibility(R.id.widget_dashboard_message, showMessage ? View.VISIBLE : View.GONE);
        bindRows(context, views, model, visibleRows);
        views.setTextViewText(R.id.widget_dashboard_updated, updatedLabel(context, model));
        views.setContentDescription(R.id.widget_dashboard_root, mediumDescription(context, model));
        return views;
    }

    private static void bindRows(Context context, RemoteViews views, DashboardWidgetViewModel model, int visibleRows) {
        List<DashboardWidgetSnapshot.Item> items =
            model.state == DashboardWidgetViewModel.State.POPULATED && model.snapshot != null
                ? model.snapshot.items
                : Collections.emptyList();
        int show = Math.min(visibleRows, ROW_IDS.length);
        for (int i = 0; i < ROW_IDS.length; i++) {
            if (i >= show || i >= items.size()) {
                views.setViewVisibility(ROW_IDS[i], View.GONE);
                continue;
            }
            DashboardWidgetSnapshot.Item item = items.get(i);
            views.setViewVisibility(ROW_IDS[i], View.VISIBLE);
            String title = context.getString(R.string.widget_dashboard_item_title, item.localId, item.title);
            views.setTextViewText(TITLE_IDS[i], title);
            views.setTextViewText(META_IDS[i], metaLabel(item));
            views.setTextViewText(STATUS_IDS[i], item.statusName);
            int accent = parseAccent(item.statusColor, context.getColor(R.color.widget_dashboard_accent_fallback));
            views.setInt(ACCENT_IDS[i], "setBackgroundColor", accent);
            String path = DashboardWidgetOpenPath.todo(item.projectSlug, item.localId);
            views.setOnClickPendingIntent(ROW_IDS[i], DashboardWidgetIntents.open(context, path, 100 + i));
            views.setContentDescription(
                ROW_IDS[i],
                context.getString(
                    R.string.widget_dashboard_item_description,
                    item.localId,
                    item.title,
                    item.statusName,
                    item.projectName
                )
            );
        }
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

    private static String metaLabel(DashboardWidgetSnapshot.Item item) {
        if (item.sprintName == null || item.sprintName.isEmpty()) return item.projectName;
        return item.projectName + " · " + item.sprintName;
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

    private static int parseAccent(String color, int fallback) {
        String sanitized = DashboardWidgetSnapshot.sanitizeHexColor(color);
        if (sanitized == null) return fallback;
        try {
            return Color.parseColor(sanitized);
        } catch (IllegalArgumentException error) {
            return fallback;
        }
    }

    private static int optionDp(Bundle options, String key, int fallback) {
        if (options == null) return fallback;
        return Math.max(0, options.getInt(key, fallback));
    }
}
