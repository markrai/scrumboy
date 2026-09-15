package com.markrai.scrumboy.widget;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.regex.Pattern;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/** Sanitized Dashboard widget snapshot. Schema version 1. */
public final class DashboardWidgetSnapshot {
    public static final int SCHEMA_VERSION = 1;
    public static final int MAX_ITEMS = 4;
    public static final int MAX_TITLE_CHARS = 120;
    public static final int MAX_NAME_CHARS = 80;
    private static final Pattern HEX_COLOR = Pattern.compile("^#[0-9a-fA-F]{6}$");

    public final String ownerOrigin;
    public final long userId;
    public final long fetchedAtMs;
    public final int assignedCount;
    public final int wipCount;
    public final List<Item> items;

    public DashboardWidgetSnapshot(
        String ownerOrigin,
        long userId,
        long fetchedAtMs,
        int assignedCount,
        int wipCount,
        List<Item> items
    ) {
        this.ownerOrigin = ownerOrigin;
        this.userId = userId;
        this.fetchedAtMs = fetchedAtMs;
        this.assignedCount = assignedCount;
        this.wipCount = wipCount;
        this.items = Collections.unmodifiableList(new ArrayList<>(items));
    }

    public static final class Item {
        public final long localId;
        public final String title;
        public final String projectName;
        public final String projectSlug;
        public final String statusName;
        public final String statusColor;
        public final Long estimationPoints;
        public final String sprintName;

        public Item(
            long localId,
            String title,
            String projectName,
            String projectSlug,
            String statusName,
            String statusColor,
            Long estimationPoints,
            String sprintName
        ) {
            this.localId = localId;
            this.title = title;
            this.projectName = projectName;
            this.projectSlug = projectSlug;
            this.statusName = statusName;
            this.statusColor = statusColor;
            this.estimationPoints = estimationPoints;
            this.sprintName = sprintName;
        }
    }

    public String toJson() throws JSONException {
        JSONObject root = new JSONObject();
        root.put("schemaVersion", SCHEMA_VERSION);
        root.put("ownerOrigin", ownerOrigin);
        root.put("userId", userId);
        root.put("fetchedAtMs", fetchedAtMs);
        root.put("assignedCount", assignedCount);
        root.put("wipCount", wipCount);
        JSONArray itemsJson = new JSONArray();
        for (Item item : items) {
            JSONObject row = new JSONObject();
            row.put("localId", item.localId);
            row.put("title", item.title);
            row.put("projectName", item.projectName);
            row.put("projectSlug", item.projectSlug);
            row.put("statusName", item.statusName);
            if (item.statusColor != null) row.put("statusColor", item.statusColor);
            if (item.estimationPoints != null) row.put("estimationPoints", item.estimationPoints);
            if (item.sprintName != null) row.put("sprintName", item.sprintName);
            itemsJson.put(row);
        }
        root.put("items", itemsJson);
        return root.toString();
    }

    public static DashboardWidgetSnapshot parse(String json) {
        if (json == null || json.trim().isEmpty()) return null;
        try {
            JSONObject root = new JSONObject(json);
            if (root.optInt("schemaVersion", -1) != SCHEMA_VERSION) return null;
            String ownerOrigin = root.optString("ownerOrigin", "").trim();
            if (ownerOrigin.isEmpty()) return null;
            if (!root.has("userId") || !root.has("fetchedAtMs")) return null;
            long userId = root.getLong("userId");
            long fetchedAtMs = root.getLong("fetchedAtMs");
            if (userId <= 0 || fetchedAtMs <= 0) return null;
            int assignedCount = Math.max(0, root.optInt("assignedCount", 0));
            int wipCount = Math.max(0, root.optInt("wipCount", 0));
            JSONArray itemsJson = root.optJSONArray("items");
            if (itemsJson == null) return null;
            List<Item> items = new ArrayList<>();
            int limit = Math.min(MAX_ITEMS, itemsJson.length());
            for (int i = 0; i < limit; i++) {
                JSONObject row = itemsJson.optJSONObject(i);
                Item item = parseItem(row);
                if (item != null) items.add(item);
            }
            return new DashboardWidgetSnapshot(ownerOrigin, userId, fetchedAtMs, assignedCount, wipCount, items);
        } catch (JSONException error) {
            return null;
        }
    }

    public static DashboardWidgetSnapshot fromPublishPayload(JSONObject payload, String ownerOrigin) {
        if (payload == null || ownerOrigin == null || ownerOrigin.trim().isEmpty()) return null;
        try {
            JSONObject copy = new JSONObject(payload.toString());
            copy.put("schemaVersion", SCHEMA_VERSION);
            copy.put("ownerOrigin", ownerOrigin.trim());
            return parse(copy.toString());
        } catch (JSONException error) {
            return null;
        }
    }

    public static DashboardWidgetSnapshot sanitize(DashboardWidgetSnapshot snapshot) {
        if (snapshot == null) return null;
        List<Item> items = new ArrayList<>();
        int limit = Math.min(MAX_ITEMS, snapshot.items.size());
        for (int i = 0; i < limit; i++) {
            Item item = sanitizeItem(snapshot.items.get(i));
            if (item != null) items.add(item);
        }
        String origin = snapshot.ownerOrigin == null ? "" : snapshot.ownerOrigin.trim();
        if (origin.isEmpty() || snapshot.userId <= 0 || snapshot.fetchedAtMs <= 0) return null;
        return new DashboardWidgetSnapshot(
            origin,
            snapshot.userId,
            snapshot.fetchedAtMs,
            Math.max(0, snapshot.assignedCount),
            Math.max(0, snapshot.wipCount),
            items
        );
    }

    private static Item parseItem(JSONObject row) {
        if (row == null) return null;
        long localId = row.optLong("localId", 0);
        if (localId <= 0) return null;
        String title = clip(row.optString("title", ""), MAX_TITLE_CHARS);
        String projectName = clip(row.optString("projectName", ""), MAX_NAME_CHARS);
        String projectSlug = clip(row.optString("projectSlug", ""), MAX_NAME_CHARS);
        String statusName = clip(row.optString("statusName", ""), MAX_NAME_CHARS);
        if (title.isEmpty() || projectName.isEmpty() || projectSlug.isEmpty() || statusName.isEmpty()) return null;
        String statusColor = sanitizeHexColor(row.optString("statusColor", null));
        Long estimationPoints = null;
        if (row.has("estimationPoints") && !row.isNull("estimationPoints")) {
            long points = row.optLong("estimationPoints");
            if (points >= 0) estimationPoints = points;
        }
        String sprintName = clip(row.optString("sprintName", ""), MAX_NAME_CHARS);
        if (sprintName.isEmpty()) sprintName = null;
        return new Item(localId, title, projectName, projectSlug, statusName, statusColor, estimationPoints, sprintName);
    }

    private static Item sanitizeItem(Item item) {
        if (item == null || item.localId <= 0) return null;
        String title = clip(item.title, MAX_TITLE_CHARS);
        String projectName = clip(item.projectName, MAX_NAME_CHARS);
        String projectSlug = clip(item.projectSlug, MAX_NAME_CHARS);
        String statusName = clip(item.statusName, MAX_NAME_CHARS);
        if (title.isEmpty() || projectName.isEmpty() || projectSlug.isEmpty() || statusName.isEmpty()) return null;
        String sprintName = clip(item.sprintName, MAX_NAME_CHARS);
        if (sprintName.isEmpty()) sprintName = null;
        return new Item(
            item.localId,
            title,
            projectName,
            projectSlug,
            statusName,
            sanitizeHexColor(item.statusColor),
            item.estimationPoints != null && item.estimationPoints >= 0 ? item.estimationPoints : null,
            sprintName
        );
    }

    static String sanitizeHexColor(String color) {
        if (color == null) return null;
        String trimmed = color.trim();
        return HEX_COLOR.matcher(trimmed).matches() ? trimmed : null;
    }

    private static String clip(String value, int maxChars) {
        if (value == null) return "";
        String trimmed = value.trim();
        if (trimmed.length() <= maxChars) return trimmed;
        return trimmed.substring(0, maxChars);
    }
}
