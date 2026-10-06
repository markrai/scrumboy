package com.markrai.scrumboy.widget;

import android.content.Intent;
import android.widget.RemoteViewsService;

public final class DashboardWidgetRemoteViewsService extends RemoteViewsService {
    @Override
    public RemoteViewsFactory onGetViewFactory(Intent intent) {
        return new DashboardWidgetRemoteViewsFactory(getApplicationContext());
    }
}
