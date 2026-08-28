package com.markrai.scrumboy.transport;

import android.content.SharedPreferences;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Iterator;
import java.util.List;
import okhttp3.Cookie;
import okhttp3.CookieJar;
import okhttp3.HttpUrl;
import okio.ByteString;

final class PersistentCookieJar implements CookieJar {
    static final String PREFERENCES_NAME = "scrumboy_transport_cookies_v1";
    private static final String STORAGE_KEY = "cookies";
    private static final String OWNER_ORIGIN_KEY = "owner_origin";

    interface Store {
        String load();
        String loadOwnerOrigin();
        void save(String ownerOrigin, String value);
    }

    private static final class SharedPreferencesStore implements Store {
        private final SharedPreferences preferences;

        SharedPreferencesStore(SharedPreferences preferences) {
            this.preferences = preferences;
        }

        @Override
        public String load() {
            return preferences.getString(STORAGE_KEY, "");
        }

        @Override
        public String loadOwnerOrigin() {
            return preferences.getString(OWNER_ORIGIN_KEY, null);
        }

        @Override
        public void save(String ownerOrigin, String value) {
            SharedPreferences.Editor editor = preferences.edit().putString(STORAGE_KEY, value);
            if (ownerOrigin == null) editor.remove(OWNER_ORIGIN_KEY); else editor.putString(OWNER_ORIGIN_KEY, ownerOrigin);
            editor.apply();
        }
    }

    private final Store store;
    private final List<Cookie> cookies = new ArrayList<>();
    private String ownerOrigin;
    private boolean retired;

    PersistentCookieJar(SharedPreferences preferences) {
        this(new SharedPreferencesStore(preferences));
    }

    PersistentCookieJar(Store store) {
        this.store = store;
        ownerOrigin = store.loadOwnerOrigin();
        cookies.addAll(decode(store.load()));
        removeExpired(System.currentTimeMillis());
    }

    synchronized String ownerOrigin() {
        return ownerOrigin;
    }

    synchronized boolean ensureOwner(String origin) {
        if (origin.equals(ownerOrigin)) return true;
        cookies.clear();
        ownerOrigin = origin;
        persist();
        return false;
    }

    @Override
    public synchronized void saveFromResponse(HttpUrl url, List<Cookie> responseCookies) {
        if (retired) return;
        long now = System.currentTimeMillis();
        removeExpired(now);
        for (Cookie cookie : responseCookies) {
            removeIdentity(cookie);
            if (cookie.expiresAt() > now) cookies.add(cookie);
        }
        persist();
    }

    @Override
    public synchronized List<Cookie> loadForRequest(HttpUrl url) {
        if (retired) return Collections.emptyList();
        boolean changed = removeExpired(System.currentTimeMillis());
        List<Cookie> result = new ArrayList<>();
        for (Cookie cookie : cookies) {
            if (cookie.matches(url)) result.add(cookie);
        }
        if (changed) persist();
        return result;
    }

    synchronized void clearAll() {
        cookies.clear();
        persist();
    }

    synchronized void retireAndClear() {
        retired = true;
        cookies.clear();
        persist();
    }

    synchronized void retireAndClearOwner() {
        retired = true;
        cookies.clear();
        ownerOrigin = null;
        persist();
    }

    private void removeIdentity(Cookie next) {
        cookies.removeIf(cookie -> cookie.name().equals(next.name()) && cookie.domain().equals(next.domain()) && cookie.path().equals(next.path()));
    }

    private boolean removeExpired(long now) {
        boolean changed = false;
        Iterator<Cookie> iterator = cookies.iterator();
        while (iterator.hasNext()) {
            if (iterator.next().expiresAt() <= now) {
                iterator.remove();
                changed = true;
            }
        }
        return changed;
    }

    private void persist() {
        StringBuilder output = new StringBuilder();
        for (Cookie cookie : cookies) {
            if (!cookie.persistent()) continue;
            if (output.length() > 0) output.append('\n');
            output.append(encode(cookie.name())).append('\t')
                .append(encode(cookie.value())).append('\t')
                .append(cookie.expiresAt()).append('\t')
                .append(encode(cookie.domain())).append('\t')
                .append(encode(cookie.path())).append('\t')
                .append(cookie.secure() ? '1' : '0').append('\t')
                .append(cookie.httpOnly() ? '1' : '0').append('\t')
                .append(cookie.hostOnly() ? '1' : '0');
        }
        store.save(ownerOrigin, output.toString());
    }

    private static List<Cookie> decode(String serialized) {
        List<Cookie> result = new ArrayList<>();
        if (serialized == null || serialized.isEmpty()) return result;
        for (String line : serialized.split("\\n")) {
            try {
                String[] fields = line.split("\\t", -1);
                if (fields.length != 8) continue;
                String domain = decodeField(fields[3]);
                Cookie.Builder builder = new Cookie.Builder()
                    .name(decodeField(fields[0]))
                    .value(decodeField(fields[1]))
                    .expiresAt(Long.parseLong(fields[2]))
                    .path(decodeField(fields[4]));
                if (fields[7].equals("1")) builder.hostOnlyDomain(domain); else builder.domain(domain);
                if (fields[5].equals("1")) builder.secure();
                if (fields[6].equals("1")) builder.httpOnly();
                result.add(builder.build());
            } catch (IllegalArgumentException ignored) {
                // Ignore malformed app-private persisted entries.
            }
        }
        return result;
    }

    private static String encode(String value) {
        return ByteString.encodeUtf8(value).base64Url().replace("=", "");
    }

    private static String decodeField(String value) {
        ByteString decoded = ByteString.decodeBase64(value);
        if (decoded == null) throw new IllegalArgumentException("Invalid cookie encoding");
        return decoded.utf8();
    }
}
