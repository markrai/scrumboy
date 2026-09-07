package com.markrai.scrumboy.speech;

import android.app.Activity;

final class SpeechInputActivityGuard {
    private SpeechInputActivityGuard() {}

    static boolean canDispatch(boolean destroyed, Activity activity) {
        return !destroyed
            && activity != null
            && !activity.isFinishing()
            && !activity.isDestroyed();
    }
}
