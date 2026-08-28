package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.List;
import okhttp3.Cookie;
import okhttp3.HttpUrl;
import org.junit.Test;

public class PersistentCookieJarTest {
    private static final class MemoryStore implements PersistentCookieJar.Store {
        private String value = "";
        private String ownerOrigin;

        @Override
        public String load() { return value; }

        @Override
        public String loadOwnerOrigin() { return ownerOrigin; }

        @Override
        public void save(String nextOwnerOrigin, String next) {
            ownerOrigin = nextOwnerOrigin;
            value = next;
        }
    }

    @Test
    public void receivesPersistsAndSendsCookieOnNextRequest() {
        MemoryStore store = new MemoryStore();
        HttpUrl url = HttpUrl.get("https://scrumboy.example/api/auth/login");
        Cookie cookie = new Cookie.Builder()
            .name("scrumboy_session")
            .value("secret-session")
            .hostOnlyDomain("scrumboy.example")
            .path("/")
            .httpOnly()
            .secure()
            .expiresAt(System.currentTimeMillis() + 60_000)
            .build();

        PersistentCookieJar first = new PersistentCookieJar(store);
        first.ensureOwner("https://scrumboy.example");
        first.saveFromResponse(url, List.of(cookie));
        assertFalse(store.value.isEmpty());

        PersistentCookieJar restarted = new PersistentCookieJar(store);
        assertTrue(restarted.ensureOwner("https://scrumboy.example"));
        List<Cookie> sent = restarted.loadForRequest(HttpUrl.get("https://scrumboy.example/api/boards"));
        assertEquals(1, sent.size());
        assertEquals("scrumboy_session", sent.get(0).name());
        assertEquals("secret-session", sent.get(0).value());
    }

    @Test
    public void excludesExpiredAndSecureCookiesWhereTheyDoNotMatch() {
        MemoryStore store = new MemoryStore();
        PersistentCookieJar jar = new PersistentCookieJar(store);
        HttpUrl https = HttpUrl.get("https://scrumboy.example/");
        jar.saveFromResponse(https, List.of(
            new Cookie.Builder()
                .name("expired")
                .value("gone")
                .hostOnlyDomain("scrumboy.example")
                .path("/")
                .expiresAt(System.currentTimeMillis() - 1_000)
                .build(),
            new Cookie.Builder()
                .name("secure")
                .value("only-https")
                .hostOnlyDomain("scrumboy.example")
                .path("/")
                .secure()
                .expiresAt(System.currentTimeMillis() + 60_000)
                .build()
        ));

        assertEquals(1, jar.loadForRequest(https).size());
        assertTrue(jar.loadForRequest(HttpUrl.get("http://scrumboy.example/")).isEmpty());
    }

    @Test
    public void processRestartClearsSameHostCookiesWhenSelectedPortChangedBeforeConfigure() {
        MemoryStore store = new MemoryStore();
        String oldOrigin = "https://same-host.example:8443";
        String nextOrigin = "https://same-host.example:9443";
        HttpUrl oldUrl = HttpUrl.get(oldOrigin + "/api/auth/login");
        PersistentCookieJar beforeProcessDeath = new PersistentCookieJar(store);
        beforeProcessDeath.ensureOwner(oldOrigin);
        beforeProcessDeath.saveFromResponse(oldUrl, List.of(
            new Cookie.Builder()
                .name("scrumboy_session")
                .value("old-port-secret")
                .hostOnlyDomain("same-host.example")
                .path("/")
                .expiresAt(System.currentTimeMillis() + 60_000)
                .build()
        ));

        // Simulate Preferences already containing the new origin when the native
        // plugin starts again and configures its independently persisted jar.
        PersistentCookieJar restarted = new PersistentCookieJar(store);
        assertEquals(oldOrigin, restarted.ownerOrigin());
        assertFalse(restarted.ensureOwner(nextOrigin));
        assertEquals(nextOrigin, restarted.ownerOrigin());
        assertTrue(restarted.loadForRequest(HttpUrl.get(nextOrigin + "/api/boards")).isEmpty());

        PersistentCookieJar restartedAgain = new PersistentCookieJar(store);
        assertTrue(restartedAgain.ensureOwner(nextOrigin));
        assertTrue(restartedAgain.loadForRequest(HttpUrl.get(nextOrigin + "/api/boards")).isEmpty());
    }

    @Test
    public void logoutClearsCookiesButRetainsOwnerAndServerResetClearsBoth() {
        MemoryStore store = new MemoryStore();
        PersistentCookieJar jar = new PersistentCookieJar(store);
        HttpUrl url = HttpUrl.get("https://old.example/");
        jar.ensureOwner("https://old.example");
        jar.saveFromResponse(url, List.of(
            new Cookie.Builder()
                .name("scrumboy_session")
                .value("old-secret")
                .hostOnlyDomain("old.example")
                .path("/")
                .expiresAt(System.currentTimeMillis() + 60_000)
                .build()
        ));

        jar.retireAndClear();
        assertTrue(jar.loadForRequest(url).isEmpty());
        PersistentCookieJar afterLogout = new PersistentCookieJar(store);
        assertEquals("https://old.example", afterLogout.ownerOrigin());
        assertTrue(afterLogout.loadForRequest(url).isEmpty());
        assertEquals("", store.value);

        afterLogout.retireAndClearOwner();
        PersistentCookieJar afterServerReset = new PersistentCookieJar(store);
        assertNull(afterServerReset.ownerOrigin());
        assertTrue(afterServerReset.loadForRequest(url).isEmpty());
    }

    @Test
    public void retiredJarCannotRestoreCookiesFromALateOldResponse() {
        MemoryStore store = new MemoryStore();
        PersistentCookieJar oldJar = new PersistentCookieJar(store);
        HttpUrl oldUrl = HttpUrl.get("https://old.example/");
        oldJar.ensureOwner("https://old.example");
        oldJar.retireAndClearOwner();

        oldJar.saveFromResponse(oldUrl, List.of(
            new Cookie.Builder()
                .name("scrumboy_session")
                .value("late-old-secret")
                .hostOnlyDomain("old.example")
                .path("/")
                .expiresAt(System.currentTimeMillis() + 60_000)
                .build()
        ));

        assertEquals("", store.value);
        assertNull(store.ownerOrigin);
        assertTrue(oldJar.loadForRequest(oldUrl).isEmpty());
        assertTrue(new PersistentCookieJar(store).loadForRequest(oldUrl).isEmpty());
    }
}
