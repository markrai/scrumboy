package com.markrai.scrumboy.widget;

/** Chooses compact vs medium widget chrome from launcher size options. */
public final class DashboardWidgetLayout {
    public enum Kind {
        COMPACT,
        MEDIUM
    }

    /** Below this width, todo rows cannot be shown reliably. */
    static final int MEDIUM_MIN_WIDTH_DP = 180;
    /** Normal 4x2 height: header + two guaranteed rows. */
    static final int TWO_ROW_MIN_HEIGHT_DP = 110;
    /** Taller widgets may show a third row. */
    static final int THREE_ROW_MIN_HEIGHT_DP = 180;

    public final Kind kind;
    public final int visibleRows;

    DashboardWidgetLayout(Kind kind, int visibleRows) {
        this.kind = kind;
        this.visibleRows = visibleRows;
    }

    public static DashboardWidgetLayout fromSize(int minWidthDp, int minHeightDp) {
        boolean wideEnough = minWidthDp >= MEDIUM_MIN_WIDTH_DP;
        boolean tallEnoughForTwoRows = minHeightDp >= TWO_ROW_MIN_HEIGHT_DP;
        if (!wideEnough || !tallEnoughForTwoRows) {
            return new DashboardWidgetLayout(Kind.COMPACT, 0);
        }
        int rows = minHeightDp >= THREE_ROW_MIN_HEIGHT_DP ? 3 : 2;
        return new DashboardWidgetLayout(Kind.MEDIUM, rows);
    }
}
