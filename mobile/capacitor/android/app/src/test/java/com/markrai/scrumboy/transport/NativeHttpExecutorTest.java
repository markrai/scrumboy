package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import java.io.IOException;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.Response;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import org.junit.Test;

public class NativeHttpExecutorTest {
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
    public void followsSameOriginRedirectAndPreservesHttpStatus() throws Exception {
        try (MockWebServer server = new MockWebServer()) {
            server.enqueue(new MockResponse().setResponseCode(302).addHeader("Location", "/final"));
            server.enqueue(new MockResponse().setResponseCode(418).setBody("teapot"));
            server.start();
            SelectedOrigin origin = SelectedOrigin.parse(server.url("/").toString(), true);
            NativeCallRegistry registry = new NativeCallRegistry();
            NativeCallRegistry.Operation operation = registry.begin("redirect");
            Request request = new Request.Builder().url(origin.resolve("/start")).get().build();

            try (Response response = new NativeHttpExecutor(new OkHttpClient()).execute(request, origin, operation)) {
                assertEquals(418, response.code());
                assertEquals("teapot", response.body().string());
            } finally {
                registry.complete(operation);
            }
            assertEquals("/start", server.takeRequest().getPath());
            assertEquals("/final", server.takeRequest().getPath());
        }
    }

    @Test
    public void rejectsCrossOriginRedirectBeforeSendingCookiesOrBody() throws Exception {
        try (MockWebServer first = new MockWebServer(); MockWebServer other = new MockWebServer()) {
            first.start();
            other.start();
            first.enqueue(new MockResponse().setResponseCode(302).addHeader("Location", other.url("/stolen")));
            SelectedOrigin origin = SelectedOrigin.parse(first.url("/").toString(), true);
            NativeCallRegistry registry = new NativeCallRegistry();
            NativeCallRegistry.Operation operation = registry.begin("cross-origin");
            Request request = new Request.Builder().url(origin.resolve("/start")).header("Cookie", "secret=value").get().build();

            TransportException error;
            try {
                error = assertThrows(
                    TransportException.class,
                    () -> new NativeHttpExecutor(new OkHttpClient()).execute(request, origin, operation)
                );
            } finally {
                registry.complete(operation);
            }
            assertEquals("cross_origin_redirect", error.code());
            assertEquals(0, other.getRequestCount());
        }
    }

    @Test
    public void receivesPersistsAndSendsCookiesThroughTheSharedOkHttpStack() throws Exception {
        try (MockWebServer server = new MockWebServer()) {
            server.enqueue(new MockResponse()
                .setResponseCode(204)
                .addHeader("Set-Cookie", "scrumboy_session=native-secret; Path=/; Max-Age=3600; HttpOnly"));
            server.enqueue(new MockResponse().setResponseCode(200).setBody("ok"));
            server.start();
            MemoryStore store = new MemoryStore();
            PersistentCookieJar jar = new PersistentCookieJar(store);
            jar.ensureOwner(server.url("/").toString().replaceAll("/$", ""));
            OkHttpClient client = new OkHttpClient.Builder().cookieJar(jar).build();
            NativeHttpExecutor executor = new NativeHttpExecutor(client);
            SelectedOrigin origin = SelectedOrigin.parse(server.url("/").toString(), true);
            NativeCallRegistry registry = new NativeCallRegistry();

            NativeCallRegistry.Operation login = registry.begin("login");
            try (Response response = executor.execute(
                new Request.Builder().url(origin.resolve("/api/auth/login")).get().build(),
                origin,
                login
            )) {
                assertEquals(204, response.code());
            } finally {
                registry.complete(login);
            }
            assertTrue(!store.value.isEmpty());

            PersistentCookieJar restartedJar = new PersistentCookieJar(store);
            NativeHttpExecutor restarted = new NativeHttpExecutor(new OkHttpClient.Builder().cookieJar(restartedJar).build());
            NativeCallRegistry.Operation boards = registry.begin("boards");
            try (Response response = restarted.execute(
                new Request.Builder().url(origin.resolve("/api/boards")).get().build(),
                origin,
                boards
            )) {
                assertEquals(200, response.code());
            } finally {
                registry.complete(boards);
            }
            server.takeRequest();
            assertTrue(server.takeRequest().getHeader("Cookie").contains("scrumboy_session=native-secret"));
        }
    }

    @Test
    public void cancellationCancelsTheActiveOkHttpCall() throws Exception {
        try (MockWebServer server = new MockWebServer()) {
            server.enqueue(new MockResponse().setHeadersDelay(10, TimeUnit.SECONDS).setBody("late"));
            server.start();
            SelectedOrigin origin = SelectedOrigin.parse(server.url("/").toString(), true);
            NativeCallRegistry registry = new NativeCallRegistry();
            NativeCallRegistry.Operation operation = registry.begin("cancel-me");
            Request request = new Request.Builder().url(origin.resolve("/slow")).get().build();
            ExecutorService executor = Executors.newSingleThreadExecutor();
            Future<Response> result = executor.submit(() -> new NativeHttpExecutor(new OkHttpClient()).execute(request, origin, operation));

            assertTrue(server.takeRequest(5, TimeUnit.SECONDS) != null);
            registry.cancel("cancel-me");
            ExecutionException error = assertThrows(ExecutionException.class, () -> result.get(5, TimeUnit.SECONDS));
            assertTrue(error.getCause() instanceof IOException);
            registry.complete(operation);
            executor.shutdownNow();
        }
    }
}
