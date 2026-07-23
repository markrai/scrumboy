import { voiceText } from './i18n.js';

export type McpToolName =
  | "todos_create"
  | "todos_get"
  | "todos_search"
  | "todos_move"
  | "todos_delete"
  | "todos_update"
  | "members_list";

type McpEnvelope<T> =
  | { ok: true; data: T; meta?: Record<string, unknown> }
  | { ok: false; error?: { code?: string; message?: string; details?: unknown } };

export type McpCallOptions = {
  signal?: AbortSignal;
};

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function mcpError(message: string, status: number, data: unknown): Error {
  const err = new Error(message);
  (err as Error & { status?: number; data?: unknown }).status = status;
  (err as Error & { status?: number; data?: unknown }).data = data;
  return err;
}

export async function callMcpTool<T = unknown>(tool: McpToolName, input: Record<string, unknown>, options: McpCallOptions = {}): Promise<T> {
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
  const data = await res.json().catch(() => null) as McpEnvelope<T> | null;
  if (!isObject(data)) {
    throw mcpError(
      res.ok
        ? voiceText("voice.errors.mcpInvalidResponse", "Invalid MCP response")
        : voiceText("voice.errors.mcpHttpFailure", "HTTP {status}", { status: res.status }),
      res.status,
      data,
    );
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
