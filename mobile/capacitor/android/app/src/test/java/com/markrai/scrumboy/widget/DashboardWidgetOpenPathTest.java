package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import org.junit.Test;

public class DashboardWidgetOpenPathTest {
    @Test
    public void acceptsDashboardAndTodoRoutes() {
        assertEquals("/dashboard", DashboardWidgetOpenPath.sanitize("/dashboard"));
        assertEquals("/dashboard", DashboardWidgetOpenPath.sanitize("/dashboard/"));
        assertEquals("/alpha/t/12", DashboardWidgetOpenPath.sanitize("/alpha/t/12"));
        assertEquals("/a1-b2/t/1", DashboardWidgetOpenPath.todo("a1-b2", 1));
    }

    @Test
    public void rejectsSchemesHostsAndOidcCallback() {
        assertNull(DashboardWidgetOpenPath.sanitize("https://evil.example/dashboard"));
        assertNull(DashboardWidgetOpenPath.sanitize("//evil.example/dashboard"));
        assertNull(DashboardWidgetOpenPath.sanitize("com.markrai.scrumboy://oidc/callback"));
        assertNull(DashboardWidgetOpenPath.sanitize("/oidc/callback"));
        assertNull(DashboardWidgetOpenPath.sanitize("/dashboard?next=https://evil.example"));
        assertNull(DashboardWidgetOpenPath.sanitize("/alpha/t/12#frag"));
        assertNull(DashboardWidgetOpenPath.sanitize("/auth/reset-password"));
        assertNull(DashboardWidgetOpenPath.sanitize("/../dashboard"));
        assertNull(DashboardWidgetOpenPath.sanitize("/ALPHA/t/1"));
        assertNull(DashboardWidgetOpenPath.sanitize("/alpha/t/0"));
        assertNull(DashboardWidgetOpenPath.sanitize("/bad--slug/t/1"));
        assertNull(DashboardWidgetOpenPath.sanitize("dashboard"));
        assertNull(DashboardWidgetOpenPath.todo("", 1));
        assertNull(DashboardWidgetOpenPath.todo("alpha", 0));
    }
}
