package com.markrai.scrumboy.widget;

import java.util.regex.Pattern;

/**
 * Sanitizes internal widget navigation to existing SPA routes.
 * Accepts {@code /dashboard} and {@code /{slug}/t/{localId}} only.
 */
public final class DashboardWidgetOpenPath {
    public static final String DASHBOARD = "/dashboard";
    private static final Pattern TODO_PATH =
        Pattern.compile("^/([a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)/t/([1-9]\\d*)$");

    private DashboardWidgetOpenPath() {}

    public static String dashboard() {
        return DASHBOARD;
    }

    public static String todo(String projectSlug, long localId) {
        if (projectSlug == null || localId <= 0) return null;
        return sanitize("/" + projectSlug.trim() + "/t/" + localId);
    }

    public static String sanitize(String raw) {
        if (raw == null) return null;
        String value = raw.trim();
        if (value.isEmpty()) return null;
        if (value.contains("\\") || value.contains("://") || value.startsWith("//") || value.contains("..")) {
            return null;
        }
        if (value.contains("?") || value.contains("#")) return null;
        if (value.startsWith("com.markrai.scrumboy:")) return null;
        if (value.contains("/oidc/") || value.contains("/callback")) return null;
        if (value.startsWith("/auth/")) return null;
        if (value.endsWith("/") && value.length() > 1) {
            value = value.substring(0, value.length() - 1);
        }
        if (DASHBOARD.equals(value)) return DASHBOARD;
        if (TODO_PATH.matcher(value).matches() && !value.contains("--")) return value;
        return null;
    }
}
