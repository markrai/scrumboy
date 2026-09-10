package com.markrai.scrumboy;

import org.junit.Test;
import static org.junit.Assert.*;

public class ScrumboyVoiceFlowPluginTest {
    @Test public void dryRunBridgeIsDebugOnlyAndStrictlyBoundsRequests() {
        assertFalse(ScrumboyVoiceFlowPlugin.allowsDryRunBridge(false));
        assertTrue(ScrumboyVoiceFlowPlugin.allowsDryRunBridge(true));
        assertTrue(ScrumboyVoiceFlowPlugin.acceptsDryRunRequest(true, "case_42", "Create Fred", 45000));
        assertFalse(ScrumboyVoiceFlowPlugin.acceptsDryRunRequest(false, "case_42", "Create Fred", 45000));
        assertFalse(ScrumboyVoiceFlowPlugin.acceptsDryRunRequest(true, "../escape", "Create Fred", 45000));
        assertFalse(ScrumboyVoiceFlowPlugin.acceptsDryRunRequest(true, "case_42", null, 45000));
        assertFalse(ScrumboyVoiceFlowPlugin.acceptsDryRunRequest(true, "case_42", "Create Fred", 0));
    }

    @Test public void onlyAcceptsBoundedTraceLinesInDebugBuilds() {
        assertTrue(ScrumboyVoiceFlowPlugin.accepts(true, "VF {\"stage\":\"asr_final\"}"));
        assertFalse(ScrumboyVoiceFlowPlugin.accepts(false, "VF {}"));
        assertFalse(ScrumboyVoiceFlowPlugin.accepts(true, null));
        assertFalse(ScrumboyVoiceFlowPlugin.accepts(true, "unrelated"));
        assertFalse(ScrumboyVoiceFlowPlugin.accepts(true, "VF " + "x".repeat(16000)));
    }

    @Test public void longUnicodeTracesRemainCompleteAndCorrelated() {
        String line = "VF {\"op\":\"test-42\",\"transcript\":\"" + "🙂".repeat(1000) + "\"}";
        StringBuilder reconstructed = new StringBuilder();
        for (String part : ScrumboyVoiceFlowPlugin.logLines(line)) {
            assertTrue(part.startsWith("VF op=test-42 part="));
            assertTrue(part.getBytes(java.nio.charset.StandardCharsets.UTF_8).length < 4000);
            reconstructed.append(part.substring(part.indexOf(' ', 18) + 1));
        }
        assertEquals(line, reconstructed.toString());
    }
}
