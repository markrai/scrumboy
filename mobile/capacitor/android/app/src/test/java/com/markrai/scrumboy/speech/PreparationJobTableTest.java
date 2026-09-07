package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public class PreparationJobTableTest {
    @Test
    public void oldGenerationCannotRemoveAReplacementJob() {
        PreparationJobTable<String> table = new PreparationJobTable<>();
        table.install("en-US", 1L, "job-a");
        table.install("en-US", 2L, "job-b");

        assertFalse(table.removeExact("en-US", 1L));
        assertTrue(table.isActive("en-US", job -> "job-b".equals(job)));
        assertEquals("job-b", table.get("en-US").job);
        assertTrue(table.removeExact("en-US", 2L));
        assertFalse(table.isActive("en-US", job -> true));
    }
}
