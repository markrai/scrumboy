package com.markrai.scrumboy.transport;

import android.content.Context;
import android.content.SharedPreferences;

/**
 * Whether the selected origin currently has a persisted {@link ScrumboySessionCookie}.
 * Unrelated persistent cookies do not count as an authenticated session.
 */
public final class TransportAuthenticatedSession {
    private TransportAuthenticatedSession() {}

    public static boolean isPresent(Context context, String selectedOrigin) {
        SharedPreferences preferences = context.getSharedPreferences(
            PersistentCookieJar.PREFERENCES_NAME,
            Context.MODE_PRIVATE
        );
        return new PersistentCookieJar(preferences).hasAuthenticatedSessionCookie(selectedOrigin);
    }
}
