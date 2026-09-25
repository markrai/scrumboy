package com.markrai.scrumboy.transport;

import java.util.Locale;
import okhttp3.HttpUrl;

final class SelectedOrigin {
    private final HttpUrl baseUrl;
    private final String value;

    private SelectedOrigin(HttpUrl baseUrl) {
        this.baseUrl = baseUrl;
        String normalized = baseUrl.toString();
        this.value = normalized.endsWith("/") ? normalized.substring(0, normalized.length() - 1) : normalized;
    }

    static SelectedOrigin parse(String candidate, boolean allowHttp) throws TransportException {
        if (candidate == null || candidate.trim().isEmpty()) {
            throw new TransportException("invalid_url", "A server origin is required");
        }
        final HttpUrl parsed;
        try {
            parsed = HttpUrl.get(candidate.trim());
        } catch (IllegalArgumentException error) {
            throw new TransportException("invalid_url", "Invalid server origin", error);
        }
        String scheme = parsed.scheme().toLowerCase(Locale.ROOT);
        if (!scheme.equals("https") && !scheme.equals("http")) {
            throw new TransportException("invalid_url", "Only HTTP(S) server origins are supported");
        }
        if (scheme.equals("http") && !allowHttp) {
            throw new TransportException("https_required", "HTTPS is required");
        }
        if (parsed.host().isEmpty() || !parsed.username().isEmpty() || !parsed.password().isEmpty()) {
            throw new TransportException("invalid_url", "Server origin must contain a host and no user information");
        }
        if (!parsed.encodedPath().equals("/") || parsed.query() != null || parsed.fragment() != null) {
            throw new TransportException("invalid_url", "Server origin cannot contain a path, query, or fragment");
        }
        HttpUrl base = parsed.newBuilder().username("").password("").encodedPath("/").query(null).fragment(null).build();
        return new SelectedOrigin(base);
    }

    HttpUrl resolve(String path) throws TransportException {
        validatePath(path);
        HttpUrl result = baseUrl.resolve(path);
        if (result == null || !sameOrigin(result)) {
            throw new TransportException("invalid_url", "Request path escaped the selected origin");
        }
        return result;
    }

    static void validatePath(String path) throws TransportException {
        if (path == null || !path.startsWith("/") || path.startsWith("//") || path.indexOf('\\') >= 0 || path.indexOf('#') >= 0) {
            throw new TransportException("invalid_url", "Request path must be root-relative");
        }
    }

    boolean sameOrigin(HttpUrl url) {
        return baseUrl.scheme().equals(url.scheme()) && baseUrl.host().equals(url.host()) && baseUrl.port() == url.port();
    }

    String value() {
        return value;
    }

    HttpUrl baseUrl() {
        return baseUrl;
    }
}
