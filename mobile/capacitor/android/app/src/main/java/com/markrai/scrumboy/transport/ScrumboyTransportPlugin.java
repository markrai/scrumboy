package com.markrai.scrumboy.transport;

import android.content.Context;
import android.content.SharedPreferences;
import android.content.pm.ApplicationInfo;
import android.net.Uri;
import android.util.Base64;
import com.getcapacitor.JSArray;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InterruptedIOException;
import java.net.ConnectException;
import java.net.SocketTimeoutException;
import java.net.UnknownHostException;
import java.util.ArrayList;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import javax.net.ssl.SSLException;
import okhttp3.CookieJar;
import okhttp3.Headers;
import okhttp3.HttpUrl;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.RequestBody;
import okhttp3.Response;
import okhttp3.ResponseBody;
import okhttp3.sse.EventSource;
import okhttp3.sse.EventSourceListener;
import okhttp3.sse.EventSources;
import okio.ByteString;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

@CapacitorPlugin(name = "ScrumboyTransport")
public final class ScrumboyTransportPlugin extends Plugin {
    private static final String EVENT_NAME = "scrumboyTransportEvent";
    private static final int COPY_BUFFER_SIZE = 16 * 1024;

    private final ExecutorService worker = Executors.newCachedThreadPool();
    private final NativeCallRegistry calls = new NativeCallRegistry();
    private final NativeSessionGeneration sessionGeneration = new NativeSessionGeneration();
    private final NativeSessionDelivery sessionDelivery = new NativeSessionDelivery(sessionGeneration);
    private final Map<String, EventSource> streams = new ConcurrentHashMap<>();
    private final Object configurationLock = new Object();

    private volatile SelectedOrigin selectedOrigin;
    private boolean sessionReady;
    private SharedPreferences cookiePreferences;
    private volatile PersistentCookieJar cookieJar;
    private volatile OkHttpClient sessionClient;
    private volatile NativeHttpExecutor sessionExecutor;
    private OkHttpClient probeClient;
    private NativeHttpExecutor probeExecutor;
    private NativeResourceStore resources;
    private Exception initializationError;

    @Override
    public void load() {
        try {
            cookiePreferences = getContext().getSharedPreferences(PersistentCookieJar.PREFERENCES_NAME, Context.MODE_PRIVATE);
            installSessionStack();
            probeClient = baseClient().newBuilder().cookieJar(CookieJar.NO_COOKIES).build();
            probeExecutor = new NativeHttpExecutor(probeClient);
            resources = new NativeResourceStore(getContext().getCacheDir());
        } catch (Exception error) {
            initializationError = error;
        }
    }

    private static OkHttpClient baseClient() {
        return new OkHttpClient.Builder().followRedirects(false).followSslRedirects(false).build();
    }

    @PluginMethod
    public void probeServer(PluginCall call) {
        final SelectedOrigin candidate;
        try {
            requireInitialized();
            candidate = SelectedOrigin.parse(call.getString("origin"), isDebuggable());
        } catch (Exception error) {
            reject(call, error);
            return;
        }
        worker.execute(() -> {
            NativeCallRegistry.Operation versionOperation = null;
            NativeCallRegistry.Operation authOperation = null;
            try {
                versionOperation = calls.begin("probe-version-" + UUID.randomUUID());
                JSONObject version = getProbeJSON(candidate, "/api/version", versionOperation);
                calls.complete(versionOperation);
                versionOperation = null;
                String serverVersion = version.optString("version", "").trim();
                if (serverVersion.isEmpty()) throw incompatible();

                authOperation = calls.begin("probe-auth-" + UUID.randomUUID());
                JSONObject auth = getProbeJSON(candidate, "/api/auth/status", authOperation);
                String mode = auth.optString("mode", "");
                if ((!mode.equals("full") && !mode.equals("anonymous")) || !auth.has("user") || !auth.has("bootstrapAvailable")) {
                    throw incompatible();
                }

                JSObject result = new JSObject();
                result.put("normalizedOrigin", candidate.value());
                result.put("version", serverVersion);
                result.put("authStatus", JSObject.fromJSONObject(auth));
                call.resolve(result);
            } catch (Exception error) {
                reject(call, error);
            } finally {
                if (versionOperation != null) calls.complete(versionOperation);
                if (authOperation != null) calls.complete(authOperation);
            }
        });
    }

    @PluginMethod
    public void configure(PluginCall call) {
        try {
            requireInitialized();
            SelectedOrigin next = SelectedOrigin.parse(call.getString("origin"), isDebuggable());
            boolean resetSession = Boolean.TRUE.equals(call.getBoolean("resetSession", false));
            synchronized (configurationLock) {
                SelectedOrigin current = selectedOrigin;
                boolean changed = current != null && !current.value().equals(next.value());
                if (changed && !resetSession) {
                    throw new TransportException("invalid_url", "Changing server requires an explicit session reset");
                }
                if (changed) clearSessionState(true);
                if (current == null) sessionGeneration.invalidate();
                cookieJar.ensureOwner(next.value());
                selectedOrigin = next;
                sessionReady = true;
            }
            call.resolve();
        } catch (Exception error) {
            reject(call, error);
        }
    }

    @PluginMethod
    public void request(PluginCall call) {
        final SelectedOrigin origin;
        final NativeHttpExecutor executor;
        final NativeCallRegistry.Operation operation;
        final Request request;
        final long generation;
        try {
            requireInitialized();
            synchronized (configurationLock) {
                origin = requireOrigin();
                executor = sessionExecutor;
                generation = sessionGeneration.capture();
                String requestId = requireString(call, "requestId");
                operation = calls.begin(requestId);
                try {
                    request = buildRequest(origin, call.getData());
                } catch (Exception error) {
                    calls.complete(operation);
                    throw error;
                }
            }
        } catch (Exception error) {
            reject(call, error);
            return;
        }
        worker.execute(() -> {
            try (Response response = executor.execute(request, origin, operation)) {
                byte[] bytes = response.body() == null ? new byte[0] : response.body().bytes();
                JSObject result = new JSObject();
                result.put("status", response.code());
                result.put("bodyBase64", Base64.encodeToString(bytes, Base64.NO_WRAP));
                resolveCurrent(generation, call, result);
            } catch (Exception error) {
                reject(call, error);
            } finally {
                calls.complete(operation);
            }
        });
    }

    @PluginMethod
    public void cancelRequest(PluginCall call) {
        calls.cancel(call.getString("requestId"));
        call.resolve();
    }

    @PluginMethod
    public void openEventStream(PluginCall call) {
        try {
            requireInitialized();
            synchronized (configurationLock) {
                SelectedOrigin origin = requireOrigin();
                long generation = sessionGeneration.capture();
                String streamId = requireString(call, "streamId");
                HttpUrl url = origin.resolve(requireString(call, "path"));
                Request request = new Request.Builder().url(url).header("Accept", "text/event-stream").get().build();
                EventSource eventSource = EventSources.createFactory(sessionClient)
                    .newEventSource(request, streamListener(streamId, generation));
                EventSource previous = streams.putIfAbsent(streamId, eventSource);
                if (previous != null) {
                    eventSource.cancel();
                    throw new TransportException("invalid_url", "Duplicate event stream ID");
                }
                call.resolve();
            }
        } catch (Exception error) {
            reject(call, error);
        }
    }

    @PluginMethod
    public void closeEventStream(PluginCall call) {
        closeStream(call.getString("streamId"));
        call.resolve();
    }

    @PluginMethod
    public void acquireResource(PluginCall call) {
        final SelectedOrigin origin;
        final NativeHttpExecutor executor;
        final NativeCallRegistry.Operation operation;
        final Request request;
        final long generation;
        try {
            requireInitialized();
            synchronized (configurationLock) {
                origin = requireOrigin();
                executor = sessionExecutor;
                generation = sessionGeneration.capture();
                operation = calls.begin(requireString(call, "requestId"));
                try {
                    request = new Request.Builder().url(origin.resolve(requireString(call, "path"))).get().build();
                } catch (Exception error) {
                    calls.complete(operation);
                    throw error;
                }
            }
        } catch (Exception error) {
            reject(call, error);
            return;
        }
        worker.execute(() -> acquireResource(call, origin, executor, operation, request, generation));
    }

    @PluginMethod
    public void releaseResource(PluginCall call) {
        if (resources != null) resources.release(call.getString("handle"));
        call.resolve();
    }

    @PluginMethod
    public void logout(PluginCall call) {
        final SelectedOrigin origin;
        final NativeHttpExecutor executor;
        final long logoutGeneration;
        try {
            requireInitialized();
            synchronized (configurationLock) {
                origin = selectedOrigin;
                executor = sessionExecutor;
                sessionReady = false;
                logoutGeneration = invalidateSessionActivity();
            }
        } catch (Exception error) {
            reject(call, error);
            return;
        }
        worker.execute(() -> {
            if (origin != null) {
                NativeCallRegistry.Operation operation = null;
                try {
                    operation = calls.begin("logout-" + UUID.randomUUID());
                    Request request = new Request.Builder()
                        .url(origin.resolve("/api/auth/logout"))
                        .header("X-Scrumboy", "1")
                        .post(RequestBody.create(new byte[0]))
                        .build();
                    try (Response ignored = executor.execute(request, origin, operation)) {
                        // Logout is best effort. Cookie cleanup below is authoritative locally.
                    }
                } catch (Exception ignored) {
                    // A network failure must not retain the local authenticated session.
                } finally {
                    if (operation != null) calls.complete(operation);
                }
            }
            synchronized (configurationLock) {
                sessionGeneration.runIfCurrent(logoutGeneration, () -> {
                    rotateSessionStack(false);
                    sessionReady = selectedOrigin != null;
                });
            }
            call.resolve();
        });
    }

    @PluginMethod
    public void resetForServerChange(PluginCall call) {
        try {
            requireInitialized();
            synchronized (configurationLock) {
                clearSessionState(true);
                selectedOrigin = null;
                sessionReady = false;
            }
            call.resolve();
        } catch (Exception error) {
            reject(call, error);
        }
    }

    private JSONObject getProbeJSON(SelectedOrigin origin, String path, NativeCallRegistry.Operation operation)
        throws Exception {
        Request request = new Request.Builder().url(origin.resolve(path)).get().build();
        try (Response response = probeExecutor.execute(request, origin, operation)) {
            if (!response.isSuccessful()) {
                throw new TransportException("server_error", "Server compatibility endpoint returned an error");
            }
            ResponseBody body = response.body();
            if (body == null) throw incompatible();
            try {
                return new JSONObject(body.string());
            } catch (JSONException error) {
                throw new TransportException("incompatible_server", "Server returned incompatible JSON", error);
            }
        }
    }

    private Request buildRequest(SelectedOrigin origin, JSObject options) throws Exception {
        String path = requiredString(options, "path");
        String method = options.optString("method", "GET").trim().toUpperCase(Locale.ROOT);
        if (!method.matches("[A-Z]+")) throw new TransportException("invalid_url", "Invalid HTTP method");

        Headers.Builder headers = new Headers.Builder();
        JSObject inputHeaders = options.getJSObject("headers");
        if (inputHeaders != null) {
            Iterator<String> names = inputHeaders.keys();
            while (names.hasNext()) {
                String name = names.next();
                if (isForbiddenHeader(name)) throw new TransportException("invalid_url", "Forbidden request header");
                Object value = inputHeaders.opt(name);
                if (!(value instanceof String)) throw new TransportException("invalid_url", "Request header values must be strings");
                headers.add(name, (String) value);
            }
        }

        RequestBody body = buildRequestBody(options.getJSObject("body"), inputHeaders);
        if (body == null && requiresRequestBody(method)) body = RequestBody.create(new byte[0]);
        if (body != null && !permitsRequestBody(method)) {
            throw new TransportException("invalid_url", "HTTP method does not permit a request body");
        }
        return new Request.Builder().url(origin.resolve(path)).headers(headers.build()).method(method, body).build();
    }

    private RequestBody buildRequestBody(JSObject body, JSObject inputHeaders) throws Exception {
        if (body == null) return null;
        String kind = body.optString("kind", "");
        if (kind.equals("text")) {
            String data = requiredString(body, "data");
            String contentType = headerValue(inputHeaders, "content-type");
            return NativeRequestBodies.text(data, contentType);
        }
        if (kind.equals("multipart")) {
            JSONArray fields = body.optJSONArray("fields");
            if (fields == null) throw new TransportException("invalid_url", "Multipart fields are required");
            List<NativeRequestBodies.Field> nativeFields = new ArrayList<>();
            for (int index = 0; index < fields.length(); index += 1) {
                JSONObject field = fields.optJSONObject(index);
                if (field == null) throw new TransportException("invalid_url", "Invalid multipart field");
                String fieldKind = field.optString("kind", "");
                String name = requiredString(field, "name");
                if (fieldKind.equals("text")) {
                    nativeFields.add(NativeRequestBodies.Field.text(name, requiredString(field, "value")));
                } else if (fieldKind.equals("file")) {
                    String filename = requiredString(field, "filename");
                    String contentType = requiredString(field, "contentType");
                    byte[] bytes;
                    ByteString decoded = ByteString.decodeBase64(requiredString(field, "dataBase64"));
                    if (decoded == null) throw new TransportException("invalid_url", "Invalid multipart file encoding");
                    bytes = decoded.toByteArray();
                    nativeFields.add(NativeRequestBodies.Field.file(name, filename, contentType, bytes));
                } else {
                    throw new TransportException("invalid_url", "Invalid multipart field kind");
                }
            }
            return NativeRequestBodies.multipart(nativeFields);
        }
        throw new TransportException("invalid_url", "Unsupported request body kind");
    }

    private void acquireResource(
        PluginCall call,
        SelectedOrigin origin,
        NativeHttpExecutor executor,
        NativeCallRegistry.Operation operation,
        Request request,
        long generation
    ) {
        NativeResourceStore.PendingResource pending = null;
        try {
            pending = resources.create();
            try (Response response = executor.execute(request, origin, operation)) {
                if (!response.isSuccessful() || response.body() == null) {
                    throw new TransportException("server_error", "Server resource request failed");
                }
                try (
                    ResponseBody body = response.body();
                    FileOutputStream output = new FileOutputStream(pending.partial())
                ) {
                    byte[] buffer = new byte[COPY_BUFFER_SIZE];
                    for (int read; (read = body.byteStream().read(buffer)) >= 0; ) {
                        if (operation.cancelled()) throw new InterruptedIOException("cancelled");
                        if (read > 0) output.write(buffer, 0, read);
                    }
                }
            }
            if (operation.cancelled()) throw new InterruptedIOException("cancelled");
            File completed = resources.commit(pending);
            JSObject result = new JSObject();
            result.put("handle", pending.handle());
            result.put("fileUri", Uri.fromFile(completed).toString());
            resolveCurrent(generation, call, result);
            pending = null;
        } catch (Exception error) {
            reject(call, error);
        } finally {
            if (pending != null) resources.cancel(pending);
            calls.complete(operation);
        }
    }

    private EventSourceListener streamListener(String streamId, long generation) {
        return new EventSourceListener() {
            @Override
            public void onOpen(EventSource eventSource, Response response) {
                if (streams.get(streamId) == eventSource) emitStreamEvent(generation, streamId, "open", null);
            }

            @Override
            public void onEvent(EventSource eventSource, String id, String type, String data) {
                if (streams.get(streamId) == eventSource) emitStreamEvent(generation, streamId, "message", data);
            }

            @Override
            public void onClosed(EventSource eventSource) {
                streams.remove(streamId, eventSource);
            }

            @Override
            public void onFailure(EventSource eventSource, Throwable error, Response response) {
                if (streams.remove(streamId, eventSource)) {
                    emitStreamEvent(generation, streamId, "error", errorCode(error));
                }
                if (response != null) response.close();
            }
        };
    }

    private void emitStreamEvent(long generation, String streamId, String kind, String value) {
        sessionDelivery.emitCurrent(generation, () -> {
            JSObject event = new JSObject();
            event.put("streamId", streamId);
            event.put("kind", kind);
            if (value != null) {
                if (kind.equals("message")) event.put("data", value); else event.put("code", value);
            }
            notifyListeners(EVENT_NAME, event);
        });
    }

    private void clearSessionState(boolean clearCookieOwner) {
        invalidateSessionActivity();
        if (cookieJar != null) rotateSessionStack(clearCookieOwner);
    }

    private long invalidateSessionActivity() {
        long generation = sessionGeneration.invalidate();
        closeAllStreams();
        calls.cancelAll();
        if (resources != null) resources.releaseAll();
        return generation;
    }

    private void installSessionStack() {
        cookieJar = new PersistentCookieJar(cookiePreferences);
        sessionClient = baseClient().newBuilder().cookieJar(cookieJar).build();
        sessionExecutor = new NativeHttpExecutor(sessionClient);
    }

    private void rotateSessionStack(boolean clearOwner) {
        if (clearOwner) cookieJar.retireAndClearOwner(); else cookieJar.retireAndClear();
        installSessionStack();
    }

    private void closeAllStreams() {
        for (String streamId : streams.keySet()) closeStream(streamId);
    }

    private void closeStream(String streamId) {
        if (streamId == null) return;
        EventSource stream = streams.remove(streamId);
        if (stream != null) stream.cancel();
    }

    private void requireInitialized() throws TransportException {
        if (initializationError != null || cookieJar == null || resources == null) {
            throw new TransportException("connect_failure", "Native transport could not initialize", initializationError);
        }
    }

    private SelectedOrigin requireOrigin() throws TransportException {
        SelectedOrigin origin = selectedOrigin;
        if (origin == null || !sessionReady) {
            throw new TransportException("invalid_url", "No Scrumboy server is configured");
        }
        return origin;
    }

    private void resolveCurrent(long generation, PluginCall call, JSObject result) throws InterruptedIOException {
        sessionDelivery.resolveCurrent(generation, () -> call.resolve(result));
    }

    private boolean isDebuggable() {
        return (getContext().getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
    }

    private static String requireString(PluginCall call, String key) throws TransportException {
        String value = call.getString(key);
        if (value == null || value.isEmpty()) throw new TransportException("invalid_url", "Missing required option");
        return value;
    }

    private static String requiredString(JSONObject object, String key) throws TransportException {
        Object value = object.opt(key);
        if (!(value instanceof String)) throw new TransportException("invalid_url", "Missing required option");
        return (String) value;
    }

    private static boolean isForbiddenHeader(String name) {
        String normalized = name.toLowerCase(Locale.ROOT);
        return normalized.equals("cookie") || normalized.equals("host") || normalized.equals("set-cookie");
    }

    private static boolean requiresRequestBody(String method) {
        return method.equals("POST") || method.equals("PUT") || method.equals("PATCH") ||
            method.equals("PROPPATCH") || method.equals("REPORT");
    }

    private static boolean permitsRequestBody(String method) {
        return !method.equals("GET") && !method.equals("HEAD");
    }

    private static String headerValue(JSObject headers, String target) {
        if (headers == null) return null;
        Iterator<String> names = headers.keys();
        while (names.hasNext()) {
            String name = names.next();
            if (name.equalsIgnoreCase(target)) return headers.optString(name, null);
        }
        return null;
    }

    private static TransportException incompatible() {
        return new TransportException("incompatible_server", "Server is not compatible with Scrumboy mobile");
    }

    private static void reject(PluginCall call, Throwable error) {
        String code = errorCode(error);
        call.reject(publicMessage(code), code);
    }

    private static String errorCode(Throwable error) {
        for (Throwable current = error; current != null; current = current.getCause()) {
            if (current instanceof TransportException) return ((TransportException) current).code();
            if (current instanceof UnknownHostException) return "dns_failure";
            if (current instanceof SocketTimeoutException) return "timeout";
            if (current instanceof SSLException) return "tls_failure";
            if (current instanceof ConnectException) return "connect_failure";
            if (current instanceof InterruptedIOException) return "cancelled";
        }
        return "connect_failure";
    }

    private static String publicMessage(String code) {
        switch (code) {
            case "invalid_url": return "Invalid server request";
            case "https_required": return "HTTPS is required";
            case "dns_failure": return "Server name could not be resolved";
            case "timeout": return "Server request timed out";
            case "tls_failure": return "Server certificate could not be verified";
            case "cross_origin_redirect": return "Cross-origin redirect rejected";
            case "server_error": return "Server returned an error";
            case "incompatible_server": return "Incompatible Scrumboy server";
            case "cancelled": return "Request cancelled";
            default: return "Could not connect to the server";
        }
    }

    @Override
    protected void handleOnDestroy() {
        synchronized (configurationLock) {
            sessionReady = false;
            invalidateSessionActivity();
        }
        worker.shutdownNow();
    }
}
