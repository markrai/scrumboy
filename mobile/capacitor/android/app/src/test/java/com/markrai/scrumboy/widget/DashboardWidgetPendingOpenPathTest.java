package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import org.junit.Test;

public class DashboardWidgetPendingOpenPathTest {
    @Test
    public void storesSanitizedPathWithoutRequiringAPlugin() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetPendingOpenPath holder = new DashboardWidgetPendingOpenPath(memory);
        assertEquals("/dashboard", holder.offer("/dashboard"));
        assertEquals("/dashboard", holder.peek());
        assertEquals("/dashboard", holder.consume());
        assertNull(holder.peek());
        assertNull(holder.consume());
    }

    @Test
    public void rejectsUnsafePaths() {
        MemoryWidgetStore memory = new MemoryWidgetStore();
        DashboardWidgetPendingOpenPath holder = new DashboardWidgetPendingOpenPath(memory);
        assertNull(holder.offer("https://evil.example/dashboard"));
        assertNull(holder.offer("com.markrai.scrumboy://oidc/callback"));
        assertNull(holder.peek());
    }
}
