import { recordLocalMutation } from '../realtime/guard.js';
import { callMcpTool, type McpToolName } from './mcp-client.js';
import type { CommandIR } from './schema.js';

type McpCommandIR = Extract<CommandIR, { intent: "todos.create" | "todos.move" | "todos.delete" | "todos.assign" }>;

export type McpCommandCall = {
  tool: McpToolName;
  input: Record<string, unknown>;
};

export type ExecuteOptions = {
  callTool?: <T = unknown>(tool: McpToolName, input: Record<string, unknown>, options?: { signal?: AbortSignal }) => Promise<T>;
  refreshBoard?: () => Promise<void>;
  recordMutation?: () => void;
  signal?: AbortSignal;
  openTodo?: (localId: number) => Promise<void>;
};

export function buildMcpCall(ir: McpCommandIR): McpCommandCall {
  switch (ir.intent) {
    case "todos.create":
      return {
        tool: "todos.create",
        input: {
          projectSlug: ir.projectSlug,
          title: ir.entities.title,
        },
      };
    case "todos.move":
      return {
        tool: "todos.move",
        input: {
          projectSlug: ir.projectSlug,
          localId: ir.entities.localId,
          toColumnKey: ir.entities.toColumnKey,
        },
      };
    case "todos.delete":
      return {
        tool: "todos.delete",
        input: {
          projectSlug: ir.projectSlug,
          localId: ir.entities.localId,
        },
      };
    case "todos.assign":
      return {
        tool: "todos.update",
        input: {
          projectSlug: ir.projectSlug,
          localId: ir.entities.localId,
          patch: {
            assigneeUserId: ir.entities.assigneeUserId,
          },
        },
      };
  }
}

export async function executeCommandIR(ir: CommandIR, options: ExecuteOptions = {}): Promise<unknown> {
  if (ir.intent === "open_todo") {
    if (!options.openTodo) {
      throw new Error("Open todo action is unavailable.");
    }
    await options.openTodo(ir.entities.localId);
    return { ok: true };
  }

  const call = buildMcpCall(ir);
  const callTool = options.callTool ?? callMcpTool;
  const markMutation = options.recordMutation ?? recordLocalMutation;

  markMutation();
  const result = options.signal
    ? await callTool(call.tool, call.input, { signal: options.signal })
    : await callTool(call.tool, call.input);
  if (options.refreshBoard) {
    await options.refreshBoard();
  }
  return result;
}
