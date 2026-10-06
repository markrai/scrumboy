package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.assertThrows;

import org.junit.Test;

public class SpeechOutputOperationRegistryTest {
    @Test
    public void bindsOneOperationToItsExactCallDeliveryIdentity() throws Exception {
        SpeechOutputOperationRegistry registry = new SpeechOutputOperationRegistry();
        SpeechOutputOperationRegistry.Operation first = registry.begin("speech-output-1", "callback-41");

        assertEquals("speech-output-1", first.operationId);
        assertEquals("callback-41", first.deliveryId);
        assertSame(first, registry.claim("speech-output-1"));
        assertNull(registry.claim("speech-output-1"));

        SpeechOutputOperationRegistry.Operation second = registry.begin("speech-output-2", "callback-42");
        assertNull(registry.claim("speech-output-1"));
        assertSame(second, registry.claim("speech-output-2"));
        assertEquals("callback-42", second.deliveryId);
    }

    @Test
    public void permitsOnlyOneActiveUtterance() throws Exception {
        SpeechOutputOperationRegistry registry = new SpeechOutputOperationRegistry();
        SpeechOutputOperationRegistry.Operation first = registry.begin("speech-output-1", "callback-1");

        SpeechOutputException busy = assertThrows(
            SpeechOutputException.class,
            () -> registry.begin("speech-output-2", "callback-2")
        );

        assertEquals("busy", busy.code());
        assertEquals(1, registry.activeCount());
        assertFalse(registry.isActive(new SpeechOutputOperationRegistry.Operation("speech-output-1", "callback-1")));
        assertSame(first, registry.cancel("speech-output-1"));
    }

    @Test
    public void staleNamedCancellationCannotCancelANewerUtterance() throws Exception {
        SpeechOutputOperationRegistry registry = new SpeechOutputOperationRegistry();
        SpeechOutputOperationRegistry.Operation first = registry.begin("speech-output-1", "callback-1");
        assertSame(first, registry.claim("speech-output-1"));
        SpeechOutputOperationRegistry.Operation second = registry.begin("speech-output-2", "callback-2");

        assertNull(registry.cancel("speech-output-1"));
        assertNull(registry.cancel(null));
        assertEquals(1, registry.activeCount());
        assertSame(second, registry.cancel("speech-output-2"));
    }

    @Test
    public void invalidationClaimsWhicheverOperationIsActive() throws Exception {
        SpeechOutputOperationRegistry registry = new SpeechOutputOperationRegistry();
        SpeechOutputOperationRegistry.Operation active = registry.begin("speech-output-1", "callback-1");

        assertSame(active, registry.invalidate());
        assertEquals(0, registry.activeCount());
        assertNull(registry.invalidate());
    }

    @Test
    public void validatesOperationAndDeliveryIds() {
        SpeechOutputOperationRegistry registry = new SpeechOutputOperationRegistry();

        assertEquals("invalid_request", assertThrows(
            SpeechOutputException.class,
            () -> registry.begin(null, "callback-1")
        ).code());
        assertEquals("invalid_request", assertThrows(
            SpeechOutputException.class,
            () -> registry.begin("x".repeat(129), "callback-1")
        ).code());
        assertEquals("invalid_request", assertThrows(
            SpeechOutputException.class,
            () -> registry.begin("speech output 1", "callback-1")
        ).code());
        assertEquals("invalid_request", assertThrows(
            SpeechOutputException.class,
            () -> registry.begin("speech-output-1", "")
        ).code());
        assertEquals("invalid_request", assertThrows(
            SpeechOutputException.class,
            () -> registry.begin("speech-output-1", "x".repeat(257))
        ).code());
    }
}
