package com.markrai.scrumboy.widget;

import android.content.Context;
import android.graphics.Color;
import android.widget.RemoteViews;
import com.markrai.scrumboy.R;
import com.markrai.scrumboy.transport.SelectedServerOrigin;
import com.markrai.scrumboy.transport.TransportAuthenticatedSession;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

final class DashboardWidgetRemoteViewsFactory implements android.widget.RemoteViewsService.RemoteViewsFactory {
    private final Context context;
    private List<DashboardWidgetSnapshot.Item> rows = Collections.emptyList();

    DashboardWidgetRemoteViewsFactory(Context context) {
        this.context = context.getApplicationContext();
    }

    @Override
    public void onCreate() {
        reload();
    }

    @Override
    public void onDataSetChanged() {
        reload();
    }

    @Override
    public void onDestroy() {
        rows = Collections.emptyList();
    }

    @Override
    public int getCount() {
        return rows.size();
    }

    @Override
    public RemoteViews getViewAt(int position) {
        if (position < 0 || position >= rows.size()) {
            return new RemoteViews(context.getPackageName(), R.layout.widget_dashboard_item);
        }
        DashboardWidgetSnapshot.Item item = rows.get(position);
        RemoteViews views = new RemoteViews(context.getPackageName(), R.layout.widget_dashboard_item);
        String title = context.getString(R.string.widget_dashboard_item_title, item.localId, item.title);
        views.setTextViewText(R.id.widget_dashboard_row_title, title);
        views.setTextViewText(R.id.widget_dashboard_row_meta, metaLabel(item));
        views.setTextViewText(R.id.widget_dashboard_row_status, item.statusName);
        int accent = parseAccent(item.statusColor, context.getColor(R.color.widget_dashboard_accent_fallback));
        views.setInt(R.id.widget_dashboard_row_accent, "setBackgroundColor", accent);
        views.setContentDescription(
            R.id.widget_dashboard_row,
            context.getString(
                R.string.widget_dashboard_item_description,
                item.localId,
                item.title,
                item.statusName,
                item.projectName
            )
        );
        views.setOnClickFillInIntent(
            R.id.widget_dashboard_row,
            DashboardWidgetIntents.fillIn(DashboardWidgetCollection.itemOpenPath(item))
        );
        return views;
    }

    @Override
    public RemoteViews getLoadingView() {
        return null;
    }

    @Override
    public int getViewTypeCount() {
        return 1;
    }

    @Override
    public long getItemId(int position) {
        return position;
    }

    @Override
    public boolean hasStableIds() {
        return false;
    }

    private void reload() {
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
        rows = new ArrayList<>(DashboardWidgetCollection.rows(model));
    }

    private static String metaLabel(DashboardWidgetSnapshot.Item item) {
        if (item.sprintName == null || item.sprintName.isEmpty()) return item.projectName;
        return item.projectName + " · " + item.sprintName;
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
}
