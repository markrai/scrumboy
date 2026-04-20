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
    const res = await fetch("/mcp", {
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
        throw mcpError(res.ok ? "Invalid MCP response" : `HTTP ${res.status}`, res.status, data);
    }
    if (typeof data.ok !== "boolean" || (data.ok === true && !("data" in data))) {
        throw mcpError("Invalid MCP response", res.status, data);
    }
    if (!res.ok || !data || data.ok !== true) {
        const errorValue = "error" in data && isObject(data.error) ? data.error : null;
        const message = typeof errorValue?.message === "string" && errorValue.message ? errorValue.message : `HTTP ${res.status}`;
        throw mcpError(message, res.status, data);
    }
    return data.data;
}
