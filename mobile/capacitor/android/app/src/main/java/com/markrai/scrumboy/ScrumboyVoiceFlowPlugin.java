package com.markrai.scrumboy;

import android.content.pm.ApplicationInfo;
import android.util.Log;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Opt-in web traces share the native ASR tag, in debuggable builds only. */
@CapacitorPlugin(name = "ScrumboyVoiceFlow")
public class ScrumboyVoiceFlowPlugin extends Plugin {
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
        boolean debuggable = (getContext().getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
        if (accepts(debuggable, line)) {
            for (String part : logLines(line)) Log.d("ScrumboyVoiceFlow", part);
        }
        call.resolve();
    }
}
