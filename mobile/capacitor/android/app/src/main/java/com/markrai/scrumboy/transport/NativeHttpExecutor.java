package com.markrai.scrumboy.transport;

import java.io.IOException;
import java.io.InterruptedIOException;
import okhttp3.Call;
import okhttp3.HttpUrl;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.Response;

final class NativeHttpExecutor {
    private static final int MAX_REDIRECTS = 5;
    private final OkHttpClient client;

    NativeHttpExecutor(OkHttpClient client) {
        this.client = client.newBuilder().followRedirects(false).followSslRedirects(false).build();
    }

    Response execute(Request initial, SelectedOrigin origin, NativeCallRegistry.Operation operation)
        throws IOException, TransportException {
        Request request = initial;
        for (int redirects = 0; ; redirects += 1) {
            if (operation.cancelled()) throw cancelled();
            Call call = client.newCall(request);
            if (!operation.attach(call)) throw cancelled();
            Response response = call.execute();
            if (!isRedirect(response.code())) return response;
            String location = response.header("Location");
            if (location == null) return response;
            if (redirects >= MAX_REDIRECTS) {
                response.close();
                throw new TransportException("server_error", "Too many redirects");
            }
            HttpUrl next = response.request().url().resolve(location);
            if (next == null || !origin.sameOrigin(next)) {
                response.close();
                throw new TransportException("cross_origin_redirect", "Cross-origin redirect rejected");
            }
            Request.Builder builder = response.request().newBuilder().url(next);
            if (switchesToGet(response.code(), response.request().method())) {
                builder.method("GET", null)
                    .removeHeader("Content-Length")
                    .removeHeader("Content-Type")
                    .removeHeader("Transfer-Encoding");
            }
            response.close();
            request = builder.build();
        }
    }

    private static boolean isRedirect(int code) {
        return code == 301 || code == 302 || code == 303 || code == 307 || code == 308;
    }

    private static boolean switchesToGet(int code, String method) {
        if (method.equals("GET") || method.equals("HEAD")) return false;
        return code == 301 || code == 302 || code == 303;
    }

    private static InterruptedIOException cancelled() {
        return new InterruptedIOException("cancelled");
    }
}
