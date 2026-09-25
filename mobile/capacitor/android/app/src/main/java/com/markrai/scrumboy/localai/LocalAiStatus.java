package com.markrai.scrumboy.localai;

import com.getcapacitor.JSObject;

final class LocalAiStatus {
    private final String state;
    private final String detail;
    private final Long downloadedBytes;
    private final Long totalBytes;
    private final Integer contextTokenLimit;
    private final String providerModel;
    private final Long retryAfterMs;

    private LocalAiStatus(
        String state,
        String detail,
        Long downloadedBytes,
        Long totalBytes,
        Integer contextTokenLimit,
        String providerModel,
        Long retryAfterMs
    ) {
        this.state = state;
        this.detail = detail;
        this.downloadedBytes = downloadedBytes;
        this.totalBytes = totalBytes;
        this.contextTokenLimit = contextTokenLimit;
        this.providerModel = providerModel;
        this.retryAfterMs = retryAfterMs;
    }

    static LocalAiStatus unsupported(String reason) {
        return new LocalAiStatus("unsupported", reason, null, null, null, null, null);
    }

    static LocalAiStatus actionRequired(String action) {
        return new LocalAiStatus("action-required", action, null, null, null, null, null);
    }

    static LocalAiStatus preparing(Long downloadedBytes, Long totalBytes) {
        return new LocalAiStatus("preparing", null, downloadedBytes, totalBytes, null, null, null);
    }

    static LocalAiStatus ready(Integer contextTokenLimit, String providerModel) {
        return new LocalAiStatus(
            "ready",
            null,
            null,
            null,
            contextTokenLimit,
            LocalAiRequestValidator.providerModel(providerModel),
            null
        );
    }

    static LocalAiStatus temporarilyUnavailable(String reason, Long retryAfterMs) {
        return new LocalAiStatus("temporarily-unavailable", reason, null, null, null, null, retryAfterMs);
    }

    String state() {
        return state;
    }

    String detail() {
        return detail;
    }

    Long retryAfterMs() {
        return retryAfterMs;
    }

    JSObject toJSObject() {
        JSObject result = new JSObject();
        result.put("state", state);
        if (state.equals("unsupported")) result.put("reason", detail);
        if (state.equals("action-required")) result.put("action", detail);
        if (state.equals("temporarily-unavailable")) result.put("reason", detail);
        if (downloadedBytes != null) result.put("downloadedBytes", downloadedBytes);
        if (totalBytes != null) result.put("totalBytes", totalBytes);
        if (state.equals("ready")) result.put("maximumOutputTokens", LocalAiRequestValidator.MAX_OUTPUT_TOKENS);
        if (contextTokenLimit != null) result.put("contextTokenLimit", contextTokenLimit);
        if (providerModel != null) result.put("providerModel", providerModel);
        if (retryAfterMs != null) result.put("retryAfterMs", retryAfterMs);
        return result;
    }
}
