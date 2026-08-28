package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.TimeUnit;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.Response;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.junit.Test;

public class NativeRequestBodiesTest {
    @Test
    public void multipartPreservesOrderNamesFilenameMimeAndBytes() throws Exception {
        try (MockWebServer server = new MockWebServer()) {
            server.enqueue(new MockResponse().setResponseCode(204));
            server.start();
            Request request = new Request.Builder()
                .url(server.url("/upload"))
                .post(NativeRequestBodies.multipart(List.of(
                    NativeRequestBodies.Field.text("revision", "42"),
                    NativeRequestBodies.Field.file(
                        "wallpaper",
                        "wall paper.png",
                        "image/png",
                        "binary-image".getBytes(StandardCharsets.UTF_8)
                    )
                )))
                .build();

            try (Response response = new OkHttpClient().newCall(request).execute()) {
                assertEquals(204, response.code());
            }
            RecordedRequest recorded = server.takeRequest(5, TimeUnit.SECONDS);
            String body = recorded.getBody().readUtf8();
            assertTrue(body.indexOf("name=\"revision\"") < body.indexOf("name=\"wallpaper\""));
            assertTrue(body.contains("42"));
            assertTrue(body.contains("filename=\"wall paper.png\""));
            assertTrue(body.contains("Content-Type: image/png"));
            assertTrue(body.contains("binary-image"));
        }
    }
}
