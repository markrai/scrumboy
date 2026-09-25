package com.markrai.scrumboy.widget;

/** Chooses compact vs medium widget chrome from launcher size options. */
public final class DashboardWidgetLayout {
    public enum Kind {
        COMPACT,
        MEDIUM
    }

    /** Below this width, a todo collection cannot be shown reliably. */
    static final int MEDIUM_MIN_WIDTH_DP = 180;
    /** Below this height, compact count-only chrome is used. */
    static final int MEDIUM_MIN_HEIGHT_DP = 110;

    public final Kind kind;

    DashboardWidgetLayout(Kind kind) {
        this.kind = kind;
    }

    public static DashboardWidgetLayout fromSize(int minWidthDp, int minHeightDp) {
        if (minWidthDp >= MEDIUM_MIN_WIDTH_DP && minHeightDp >= MEDIUM_MIN_HEIGHT_DP) {
            return new DashboardWidgetLayout(Kind.MEDIUM);
        }
        return new DashboardWidgetLayout(Kind.COMPACT);
    }
}
