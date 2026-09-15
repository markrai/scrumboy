package com.markrai.scrumboy.widget;

import static org.junit.Assert.assertEquals;

import org.junit.Test;

public class DashboardWidgetLayoutTest {
    @Test
    public void compactWhenWidthOrHeightCannotFitTwoRows() {
        assertEquals(DashboardWidgetLayout.Kind.COMPACT, DashboardWidgetLayout.fromSize(110, 110).kind);
        assertEquals(0, DashboardWidgetLayout.fromSize(110, 110).visibleRows);
        assertEquals(DashboardWidgetLayout.Kind.COMPACT, DashboardWidgetLayout.fromSize(250, 80).kind);
        assertEquals(DashboardWidgetLayout.Kind.COMPACT, DashboardWidgetLayout.fromSize(120, 400).kind);
    }

    @Test
    public void mediumGuaranteesTwoRowsAndAddsThirdOnlyWhenTallEnough() {
        DashboardWidgetLayout two = DashboardWidgetLayout.fromSize(250, 110);
        assertEquals(DashboardWidgetLayout.Kind.MEDIUM, two.kind);
        assertEquals(2, two.visibleRows);

        DashboardWidgetLayout three = DashboardWidgetLayout.fromSize(250, 180);
        assertEquals(DashboardWidgetLayout.Kind.MEDIUM, three.kind);
        assertEquals(3, three.visibleRows);
    }
}
