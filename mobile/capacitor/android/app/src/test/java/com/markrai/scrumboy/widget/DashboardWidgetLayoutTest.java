package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;

import org.junit.Test;

public class DashboardWidgetLayoutTest {
    @Test
    public void compactWhenWidthOrHeightCannotFitACollection() {
        assertEquals(DashboardWidgetLayout.Kind.COMPACT, DashboardWidgetLayout.fromSize(110, 110).kind);
        assertEquals(DashboardWidgetLayout.Kind.COMPACT, DashboardWidgetLayout.fromSize(250, 80).kind);
        assertEquals(DashboardWidgetLayout.Kind.COMPACT, DashboardWidgetLayout.fromSize(120, 400).kind);
    }

    @Test
    public void mediumWhenWideAndTallEnoughForAScrollableCollection() {
        assertEquals(DashboardWidgetLayout.Kind.MEDIUM, DashboardWidgetLayout.fromSize(250, 110).kind);
        assertEquals(DashboardWidgetLayout.Kind.MEDIUM, DashboardWidgetLayout.fromSize(250, 180).kind);
    }
}
