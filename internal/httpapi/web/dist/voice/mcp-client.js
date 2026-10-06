import { voiceText } from './i18n.js';
import { getAppRuntime } from '../platform/runtime.js';
function isObject(value) {
    return value !== null && typeof value === "object" && !Array.isArray(value);
}
function mcpError(message, status, data) {
    const err = new Error(message);
    err.status = status;
    err.data = data;
    return err;
}
export async function callMcpTool(tool, input, options = {}) {
    const res = await getAppRuntime().transport().request("/mcp", {
        method: "POST",
        credentials: "same-origin",
        signal: options.signal,
        headers: {
            "Content-Type": "application/json",
            "X-Scrumboy": "1",
        },
        body: JSON.stringify({ tool, input }),
    });
    const data = await res.json().catch(() => null);
    if (!isObject(data)) {
        throw mcpError(res.ok
            ? voiceText("voice.errors.mcpInvalidResponse", "Invalid MCP response")
            : voiceText("voice.errors.mcpHttpFailure", "HTTP {status}", { status: res.status }), res.status, data);
    }
    if (typeof data.ok !== "boolean" || (data.ok === true && !("data" in data))) {
        throw mcpError(voiceText("voice.errors.mcpInvalidResponse", "Invalid MCP response"), res.status, data);
    }
    if (!res.ok || !data || data.ok !== true) {
        const errorValue = "error" in data && isObject(data.error) ? data.error : null;
        const message = typeof errorValue?.message === "string" && errorValue.message
            ? errorValue.message
            : voiceText("voice.errors.mcpHttpFailure", "HTTP {status}", { status: res.status });
        throw mcpError(message, res.status, data);
    }
    return data.data;
}
