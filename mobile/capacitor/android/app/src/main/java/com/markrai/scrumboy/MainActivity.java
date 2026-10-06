package com.markrai.scrumboy;

import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.os.Bundle;
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.PluginHandle;
import com.markrai.scrumboy.localai.ScrumboyLocalTextGenerationPlugin;
import com.markrai.scrumboy.speech.ScrumboySpeechInputPlugin;
import com.markrai.scrumboy.speech.ScrumboySpeechOutputPlugin;
import com.markrai.scrumboy.transport.ScrumboyTransportPlugin;
import com.markrai.scrumboy.widget.DashboardWidgetIntents;
import com.markrai.scrumboy.widget.DashboardWidgetPendingOpenPath;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

public class MainActivity extends BridgeActivity {
    static final String VOICE_CREATE_DRY_RUN_ACTION = "com.markrai.scrumboy.action.VOICE_CREATE_DRY_RUN";
    static final String VOICE_AGENT_EVAL_ACTION = "com.markrai.scrumboy.action.VOICE_AGENT_EVAL";

    @Override
    public void onCreate(Bundle savedInstanceState) {
        registerPlugin(ScrumboyVoiceFlowPlugin.class);
        registerPlugin(ScrumboyLocalTextGenerationPlugin.class);
        registerPlugin(ScrumboySpeechInputPlugin.class);
        registerPlugin(ScrumboySpeechOutputPlugin.class);
        registerPlugin(ScrumboyTransportPlugin.class);
        super.onCreate(savedInstanceState);
        dispatchWidgetOpen(getIntent());
        dispatchDryRunIntent(getIntent());
        dispatchAgentEvalIntent(getIntent());
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        dispatchWidgetOpen(intent);
        dispatchDryRunIntent(intent);
        dispatchAgentEvalIntent(intent);
    }

    private void dispatchWidgetOpen(Intent intent) {
        if (intent == null) return;
        String path = intent.getStringExtra(DashboardWidgetIntents.EXTRA_OPEN_PATH);
        if (path == null || path.isEmpty()) return;
        if (new DashboardWidgetPendingOpenPath(getApplicationContext()).offer(path) == null) return;
        intent.removeExtra(DashboardWidgetIntents.EXTRA_OPEN_PATH);
        tryNotifyWidgetOpenPath();
    }

    private void tryNotifyWidgetOpenPath() {
        try {
            if (bridge == null) return;
            PluginHandle handle = bridge.getPlugin("ScrumboyTransport");
            if (handle != null && handle.getInstance() instanceof ScrumboyTransportPlugin plugin) {
                plugin.emitPendingOpenPath();
            }
        } catch (RuntimeException ignored) {
            // Cold start may run before the transport plugin exists. The pending-path holder is authoritative.
        }
    }

    private boolean isDebuggable() {
        return (getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
    }

    private void dispatchDryRunIntent(Intent intent) {
        if (!isDebuggable() || intent == null || !VOICE_CREATE_DRY_RUN_ACTION.equals(intent.getAction())) return;
        String requestId = intent.getStringExtra("requestId");
        String transcriptBase64 = intent.getStringExtra("transcriptBase64");
        if (transcriptBase64 == null) return;
        final String transcript;
        try {
            transcript = new String(Base64.getDecoder().decode(transcriptBase64), StandardCharsets.UTF_8);
        } catch (IllegalArgumentException error) {
            return;
        }
        PluginHandle handle = bridge.getPlugin("ScrumboyVoiceFlow");
        if (handle != null && handle.getInstance() instanceof ScrumboyVoiceFlowPlugin plugin) {
            plugin.dispatchDryRunRequest(requestId, transcript, intent.getIntExtra("timeoutMs", 45_000));
        }
    }

    private void dispatchAgentEvalIntent(Intent intent) {
        if (!isDebuggable() || intent == null || !VOICE_AGENT_EVAL_ACTION.equals(intent.getAction())) return;
        String requestId = intent.getStringExtra("requestId");
        String transcriptBase64 = intent.getStringExtra("transcriptBase64");
        final String transcript;
        if (transcriptBase64 == null) {
            transcript = ScrumboyVoiceFlowPlugin.AGENT_EVAL_CORPUS;
        } else {
            try {
                transcript = new String(Base64.getDecoder().decode(transcriptBase64), StandardCharsets.UTF_8);
            } catch (IllegalArgumentException error) {
                return;
            }
        }
        PluginHandle handle = bridge.getPlugin("ScrumboyVoiceFlow");
        if (handle != null && handle.getInstance() instanceof ScrumboyVoiceFlowPlugin plugin) {
            plugin.dispatchAgentEvalRequest(requestId, transcript, intent.getIntExtra("timeoutMs", 600_000));
        }
    }
}
