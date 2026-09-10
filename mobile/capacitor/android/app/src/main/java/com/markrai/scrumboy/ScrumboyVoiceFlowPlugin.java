package com.markrai.scrumboy;

import android.content.pm.ApplicationInfo;
import android.util.Log;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import org.json.JSONException;
import org.json.JSONObject;

/** Opt-in web traces share the native ASR tag, in debuggable builds only. */
@CapacitorPlugin(name = "ScrumboyVoiceFlow")
public class ScrumboyVoiceFlowPlugin extends Plugin {
    static final String DRY_RUN_EVENT = "voiceCreateDryRunRequest";
    private static final Pattern DRY_RUN_REQUEST_ID = Pattern.compile("^[A-Za-z0-9_-]{1,64}$");

    static boolean allowsDryRunBridge(boolean debuggable) {
        return debuggable;
    }

    static boolean acceptsDryRunRequest(boolean debuggable, String requestId, String transcript, int timeoutMs) {
        return allowsDryRunBridge(debuggable)
            && requestId != null
            && DRY_RUN_REQUEST_ID.matcher(requestId).matches()
            && transcript != null
            && transcript.length() <= 8192
            && timeoutMs > 0
            && timeoutMs <= 120000;
    }

    private boolean isDebuggable() {
        return (getContext().getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
    }

    public void dispatchDryRunRequest(String requestId, String transcript, int timeoutMs) {
        if (!acceptsDryRunRequest(isDebuggable(), requestId, transcript, timeoutMs)) return;
        File result = new File(new File(getContext().getFilesDir(), "voice-create-dry-run"), requestId + ".json");
        if (result.exists() && !result.delete()) return;
        JSObject data = new JSObject();
        data.put("requestId", requestId);
        data.put("transcript", transcript);
        data.put("timeoutMs", timeoutMs);
        notifyListeners(DRY_RUN_EVENT, data, true);
    }

    private static boolean validDryRunResult(String resultJson) {
        if (resultJson == null || resultJson.length() > 262144) return false;
        try {
            JSONObject value = new JSONObject(resultJson);
            return value.optInt("version", -1) == 1
                && value.has("mutationExecuted")
                && value.get("mutationExecuted") instanceof Boolean
                && !value.getBoolean("mutationExecuted")
                && value.has("confirmationReady")
                && value.get("confirmationReady") instanceof Boolean
                && value.optInt("plannerCallCount", -1) >= 0
                && value.optInt("plannerCallCount", -1) <= 1;
        } catch (JSONException error) {
            return false;
        }
    }

    private void writeDryRunResult(String requestId, String resultJson) throws IOException {
        File directory = new File(getContext().getFilesDir(), "voice-create-dry-run");
        if (!directory.isDirectory() && !directory.mkdirs()) throw new IOException("result_directory_unavailable");
        File target = new File(directory, requestId + ".json");
        File temporary = new File(directory, requestId + ".tmp");
        try (FileOutputStream output = new FileOutputStream(temporary, false)) {
            output.write(resultJson.getBytes(StandardCharsets.UTF_8));
            output.getFD().sync();
        }
        if (target.exists() && !target.delete()) throw new IOException("stale_result_unavailable");
        if (!temporary.renameTo(target)) throw new IOException("result_publish_failed");
    }

    @PluginMethod
    public void completeDryRun(PluginCall call) {
        String requestId = call.getString("requestId");
        String resultJson = call.getString("resultJson");
        if (!allowsDryRunBridge(isDebuggable())) {
            call.reject("Voice Create dry-run is unavailable", "debug_only");
            return;
        }
        if (requestId == null || !DRY_RUN_REQUEST_ID.matcher(requestId).matches() || !validDryRunResult(resultJson)) {
            call.reject("Voice Create dry-run result was rejected", "invalid_dry_run_result");
            return;
        }
        try {
            writeDryRunResult(requestId, resultJson);
            call.resolve();
        } catch (IOException error) {
            call.reject("Voice Create dry-run result could not be stored", "dry_run_io");
        }
    }

    static boolean accepts(boolean debuggable, String line) {
        return debuggable && line != null && line.startsWith("VF ") && line.length() <= 16000;
    }

    static List<String> logLines(String line) {
        // Stay below Logcat's byte limit even for non-ASCII dictated text.
        if (line.length() <= 800) return List.of(line);
        Matcher match = Pattern.compile("\"op\":\"([^\"]+)\"").matcher(line);
        String op = match.find() ? match.group(1) : "unknown";
        List<String> chunks = new ArrayList<>();
        for (int start = 0; start < line.length();) {
            int end = Math.min(start + 800, line.length());
            if (end < line.length() && Character.isHighSurrogate(line.charAt(end - 1))) end--;
            chunks.add(line.substring(start, end));
            start = end;
        }
        List<String> lines = new ArrayList<>();
        for (int index = 0; index < chunks.size(); index++) {
            lines.add("VF op=" + op + " part=" + (index + 1) + "/" + chunks.size() + " " + chunks.get(index));
        }
        return lines;
    }

    @PluginMethod
    public void emit(PluginCall call) {
        String line = call.getString("line");
        if (accepts(isDebuggable(), line)) {
            for (String part : logLines(line)) Log.d("ScrumboyVoiceFlow", part);
        }
        call.resolve();
    }
}
