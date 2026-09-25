package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import org.junit.Test;

public class SelectedServerOriginTest {
    @Test
    public void readsTrimmedOriginAndTreatsBlankAsMissing() {
        SelectedServerOrigin.Store store = key -> {
            if (SelectedServerOrigin.KEY.equals(key)) return "  https://scrumboy.example  ";
            return null;
        };
        assertEquals("https://scrumboy.example", new SelectedServerOrigin(store).read());
        assertNull(new SelectedServerOrigin(key -> "").read());
        assertNull(new SelectedServerOrigin(key -> "   ").read());
        assertNull(new SelectedServerOrigin(key -> null).read());
    }
}
