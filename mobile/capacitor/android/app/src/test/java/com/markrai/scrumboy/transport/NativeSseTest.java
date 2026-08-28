package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import okhttp3.Cookie;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.Response;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import okhttp3.sse.EventSource;
import okhttp3.sse.EventSourceListener;
import okhttp3.sse.EventSources;
import org.junit.Test;

public class NativeSseTest {
    private static final class MemoryStore implements PersistentCookieJar.Store {
        String value = "";
        String ownerOrigin;
        public String load() { return value; }
        public String loadOwnerOrigin() { return ownerOrigin; }
        public void save(String nextOwnerOrigin, String next) {
            ownerOrigin = nextOwnerOrigin;
            value = next;
        }
    }

    @Test
    public void sseSharesCookieJarAndReportsOpenMessageAndClose() throws Exception {
        try (MockWebServer server = new MockWebServer()) {
            server.enqueue(
                new MockResponse()
                    .setResponseCode(200)
                    .addHeader("Content-Type", "text/event-stream")
                    .setBody("data: hello-native-sse\n\n")
            );
            server.start();
            PersistentCookieJar jar = new PersistentCookieJar(new MemoryStore());
            jar.ensureOwner(server.url("/").toString().replaceAll("/$", ""));
            jar.saveFromResponse(server.url("/"), List.of(
                new Cookie.Builder()
                    .name("scrumboy_session")
                    .value("native-session")
                    .hostOnlyDomain(server.url("/").host())
                    .path("/")
                    .expiresAt(System.currentTimeMillis() + 60_000)
                    .build()
            ));
            OkHttpClient client = new OkHttpClient.Builder().cookieJar(jar).build();
            CountDownLatch open = new CountDownLatch(1);
            CountDownLatch message = new CountDownLatch(1);
            CountDownLatch closed = new CountDownLatch(1);
            AtomicReference<String> data = new AtomicReference<>();
            Request request = new Request.Builder().url(server.url("/api/me/realtime")).build();

            EventSources.createFactory(client).newEventSource(request, new EventSourceListener() {
                @Override public void onOpen(EventSource source, Response response) { open.countDown(); }
                @Override public void onEvent(EventSource source, String id, String type, String value) {
                    data.set(value);
                    message.countDown();
                }
                @Override public void onClosed(EventSource source) { closed.countDown(); }
            });

            assertTrue(open.await(5, TimeUnit.SECONDS));
            assertTrue(message.await(5, TimeUnit.SECONDS));
            assertTrue(closed.await(5, TimeUnit.SECONDS));
            assertEquals("hello-native-sse", data.get());
            RecordedRequest recorded = server.takeRequest(5, TimeUnit.SECONDS);
            assertTrue(recorded.getHeader("Cookie").contains("scrumboy_session=native-session"));
        }
    }
}
