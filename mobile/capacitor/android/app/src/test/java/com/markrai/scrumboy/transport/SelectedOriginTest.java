package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;

import org.junit.Test;

public class SelectedOriginTest {
    @Test
    public void normalizesHttpsOrigin() throws Exception {
        assertEquals("https://example.com", SelectedOrigin.parse(" HTTPS://Example.COM:443/ ", false).value());
        assertEquals("https://example.com:8443", SelectedOrigin.parse("https://example.com:8443/", false).value());
    }

    @Test
    public void releaseRejectsHttpAndDebugAllowsIt() throws Exception {
        TransportException error = assertThrows(
            TransportException.class,
            () -> SelectedOrigin.parse("http://192.168.1.20:8080", false)
        );
        assertEquals("https_required", error.code());
        assertEquals("http://192.168.1.20:8080", SelectedOrigin.parse("http://192.168.1.20:8080", true).value());
    }

    @Test
    public void validatesRootRelativePaths() throws Exception {
        SelectedOrigin origin = SelectedOrigin.parse("https://example.com", false);
        assertEquals("https://example.com/api/version", origin.resolve("/api/version").toString());
        for (String invalid : new String[] { "api/version", "//evil.example/api", "https://evil.example/api", "/api\\version", "/api#fragment" }) {
            assertThrows(TransportException.class, () -> origin.resolve(invalid));
        }
    }

    @Test
    public void rejectsOriginPathsCredentialsQueriesAndFragments() {
        for (String invalid : new String[] {
            "https://example.com/base",
            "https://user:password@example.com",
            "https://example.com/?server=other",
            "https://example.com/#fragment"
        }) {
            assertThrows(TransportException.class, () -> SelectedOrigin.parse(invalid, false));
        }
    }
}
