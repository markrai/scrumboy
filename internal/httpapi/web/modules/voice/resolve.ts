import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';
import { normalizeLookup } from './normalize.js';
import { cloneCommandFailure, localizedCommandFailure, isCommandFailure, validateCommandIR, type CommandFailure, type CommandIR, type CommandResult, type ParsedCommandDraft, type ResolvedCommand } from './schema.js';
import type { McpToolName } from './mcp-client.js';
import { BUILTIN_STATUS_ALIASES } from './vocabulary.js';
import { resolveTodoTarget } from './target-resolver.js';
import { voiceText } from './i18n.js';

export type ResolveContext = {
  projectId: number;
  projectSlug: string;
  board: Board;
  members: BoardMember[];
  callTool?: <T = unknown>(tool: McpToolName, input: Record<string, unknown>) => Promise<T>;
};

type LaneRef = { key: string; name: string; isDone: boolean };

type MembersListResponse = {
  items?: BoardMember[];
};

export type ResolveCommandOptions = {
  selectedLocalId?: number;
  allowedLocalIds?: number[];
};

function boardLanes(board: Board): LaneRef[] {
  if (board.columnOrder && board.columnOrder.length > 0) {
    return board.columnOrder.map((lane) => ({
      key: lane.key,
      name: lane.name,
      isDone: !!lane.isDone,
    }));
  }
  return Object.keys(board.columns ?? {}).map((key) => ({
    key,
    name: key.replace(/_/g, " "),
    isDone: key === "done",
  }));
}

function addAlias(aliases: Map<string, Set<LaneRef>>, alias: string, lane: LaneRef): void {
  const key = normalizeLookup(alias);
  if (!key) return;
  const existing = aliases.get(key) ?? new Set<LaneRef>();
  existing.add(lane);
  aliases.set(key, existing);
}

function buildLaneAliasMap(board: Board): Map<string, Set<LaneRef>> {
  const lanes = boardLanes(board);
  const byKey = new Map(lanes.map((lane) => [lane.key, lane]));
  const aliases = new Map<string, Set<LaneRef>>();

  for (const lane of lanes) {
    addAlias(aliases, lane.name, lane);
    addAlias(aliases, lane.key, lane);
    addAlias(aliases, lane.key.replace(/_/g, " "), lane);
  }

  const doneLane = byKey.get("done") ?? lanes.find((lane) => lane.isDone);
  for (const [alias, key] of BUILTIN_STATUS_ALIASES) {
    const targetKey = key === "done" ? doneLane?.key : key;
    if (!targetKey) continue;
    const lane = byKey.get(targetKey);
    if (lane) addAlias(aliases, alias, lane);
  }

  return aliases;
}

function resolveStatus(rawStatus: string, board: Board): CommandResult<LaneRef> {
  const alias = normalizeLookup(rawStatus);
  const matches = buildLaneAliasMap(board).get(alias);
  if (!matches || matches.size === 0) {
    return localizedCommandFailure("unknown_status", "voice.errors.statusNotFound", "Status was not found on this board.");
  }
  const lanes = Array.from(matches);
  if (lanes.length > 1) {
    return localizedCommandFailure("ambiguous_status", "voice.errors.statusAmbiguous", "Status matches more than one lane.");
  }
  return { ok: true, value: lanes[0] };
}

function memberAliases(member: BoardMember): string[] {
  const aliases: string[] = [];
  if (member.name) aliases.push(member.name);
  if (member.email) {
    aliases.push(member.email);
    aliases.push(member.email.split("@")[0]);
  }
  return aliases.map(normalizeLookup).filter(Boolean);
}

function findMatchingMembers(rawUser: string, members: BoardMember[]): BoardMember[] {
  const wanted = normalizeLookup(rawUser);
  if (!wanted) return [];
  return members.filter((member) => memberAliases(member).includes(wanted));
}

async function resolveMember(rawUser: string, context: ResolveContext): Promise<CommandResult<BoardMember>> {
  let matches = findMatchingMembers(rawUser, context.members);
  if (matches.length === 0 && context.callTool) {
    try {
      const data = await context.callTool<MembersListResponse>("members_list", {
        projectSlug: context.projectSlug,
      });
      if (Array.isArray(data?.items)) {
        matches = findMatchingMembers(rawUser, data.items);
      }
    } catch {
      return localizedCommandFailure("unknown_user", "voice.errors.assigneeNotFound", "Assignee was not found in this project.");
    }
  }
  if (matches.length === 0) {
    return localizedCommandFailure("unknown_user", "voice.errors.assigneeNotFound", "Assignee was not found in this project.");
  }
  const uniqueById = new Map(matches.map((member) => [member.userId, member]));
  if (uniqueById.size > 1) {
    return localizedCommandFailure("ambiguous_user", "voice.errors.assigneeAmbiguous", "Assignee matches more than one project member.");
  }
  return { ok: true, value: Array.from(uniqueById.values())[0] };
}

function validateResolvedIR(ir: CommandIR, context: ResolveContext): CommandResult<CommandIR> {
  return validateCommandIR(ir, {
    projectId: context.projectId,
    projectSlug: context.projectSlug,
    board: context.board,
  });
}

function withDraft(failure: CommandFailure, draft: ParsedCommandDraft): CommandFailure {
  return cloneCommandFailure(failure, { draft });
}

async function resolveDraftTarget(draft: Exclude<ParsedCommandDraft, { intent: "todos.create" }>, context: ResolveContext, options: ResolveCommandOptions) {
  const resolved = await resolveTodoTarget(draft.target, {
    projectSlug: context.projectSlug,
    board: context.board,
    callTool: context.callTool,
  }, options.selectedLocalId, options.allowedLocalIds);
  if (isCommandFailure(resolved)) {
    return resolved.code === "ambiguous_story" ? withDraft(resolved, draft) : resolved;
  }
  return resolved;
}

export function formatResolvedCommand(command: ResolvedCommand): Pick<ResolvedCommand, "summary" | "confirmLabel"> {
  switch (command.ir.intent) {
    case "todos.create": {
      const title = command.ir.entities.title;
      return {
        summary: voiceText("voice.summary.create", "Create todo \"{title}\"", { title }),
        confirmLabel: voiceText("voice.action.create", "Create"),
      };
    }
    case "open_todo": {
      const localId = command.ir.entities.localId;
      const title = command.storyTitle ?? "";
      return {
        summary: voiceText("voice.summary.open", "Open todo #{localId}: {title}", { localId, title }),
        confirmLabel: voiceText("voice.action.open", "Open"),
      };
    }
    case "todos.delete": {
      const localId = command.ir.entities.localId;
      const title = command.storyTitle ?? "";
      return {
        summary: voiceText("voice.summary.delete", "Delete todo #{localId}: {title}", { localId, title }),
        confirmLabel: voiceText("voice.action.delete", "Delete"),
      };
    }
    case "todos.move": {
      const localId = command.ir.entities.localId;
      const title = command.storyTitle ?? "";
      const statusName = command.statusName ?? "";
      return {
        summary: voiceText("voice.summary.move", "Move todo #{localId}: {title} to {statusName}", { localId, title, statusName }),
        confirmLabel: voiceText("voice.action.move", "Move"),
      };
    }
    case "todos.assign": {
      const localId = command.ir.entities.localId;
      const title = command.storyTitle ?? "";
      const assigneeName = command.assigneeName ?? "";
      return {
        summary: voiceText("voice.summary.assign", "Assign todo #{localId}: {title} to {assigneeName}", { localId, title, assigneeName }),
        confirmLabel: voiceText("voice.action.assign", "Assign"),
      };
    }
    default: {
      const exhaustive: never = command.ir;
      return exhaustive;
    }
  }
}

function withResolvedCommandDisplay(command: ResolvedCommand): ResolvedCommand {
  return { ...command, ...formatResolvedCommand(command) };
}

export async function resolveCommandDraft(
  draft: ParsedCommandDraft,
  context: ResolveContext,
  options: ResolveCommandOptions = {},
): Promise<CommandResult<ResolvedCommand>> {
  if (draft.intent === "todos.create") {
    const ir: CommandIR = {
      intent: "todos.create",
      projectId: context.projectId,
      projectSlug: context.projectSlug,
      entities: { title: draft.title },
    };
    const validated = validateResolvedIR(ir, context);
    if (isCommandFailure(validated)) return validated;
    return {
      ok: true,
      value: withResolvedCommandDisplay({
        ir: validated.value,
        summary: "",
        confirmLabel: "",
        danger: false,
        requiresConfirmation: true,
      }),
    };
  }

  if (draft.intent === "open_todo") {
    const target = await resolveDraftTarget(draft, context, options);
    if (isCommandFailure(target)) return target;
    const todo = target.value.todo;
    const ir: CommandIR = {
      intent: "open_todo",
      projectId: context.projectId,
      projectSlug: context.projectSlug,
      entities: { localId: todo.localId },
    };
    const validated = validateResolvedIR(ir, context);
    if (isCommandFailure(validated)) return validated;
    return {
      ok: true,
      value: withResolvedCommandDisplay({
        ir: validated.value,
        summary: "",
        confirmLabel: "",
        danger: false,
        requiresConfirmation: !!target.value.ambiguousId,
        storyTitle: todo.title,
      }),
    };
  }

  if (draft.intent === "todos.delete") {
    const target = await resolveDraftTarget(draft, context, options);
    if (isCommandFailure(target)) return target;
    const todo = target.value.todo;
    const ir: CommandIR = {
      intent: "todos.delete",
      projectId: context.projectId,
      projectSlug: context.projectSlug,
      entities: { localId: todo.localId },
    };
    const validated = validateResolvedIR(ir, context);
    if (isCommandFailure(validated)) return validated;
    return {
      ok: true,
      value: withResolvedCommandDisplay({
        ir: validated.value,
        summary: "",
        confirmLabel: "",
        danger: true,
        requiresConfirmation: true,
        storyTitle: todo.title,
      }),
    };
  }

  if (draft.intent === "todos.move") {
    const lane = resolveStatus(draft.rawStatus, context.board);
    if (isCommandFailure(lane)) return lane;
    const target = await resolveDraftTarget(draft, context, options);
    if (isCommandFailure(target)) return target;
    const todo = target.value.todo;
    const ir: CommandIR = {
      intent: "todos.move",
      projectId: context.projectId,
      projectSlug: context.projectSlug,
      entities: { localId: todo.localId, toColumnKey: lane.value.key },
    };
    const validated = validateResolvedIR(ir, context);
    if (isCommandFailure(validated)) return validated;
    return {
      ok: true,
      value: withResolvedCommandDisplay({
        ir: validated.value,
        summary: "",
        confirmLabel: "",
        danger: false,
        requiresConfirmation: true,
        storyTitle: todo.title,
        statusName: lane.value.name,
      }),
    };
  }

  const member = await resolveMember(draft.rawUser, context);
  if (isCommandFailure(member)) return member;
  const target = await resolveDraftTarget(draft, context, options);
  if (isCommandFailure(target)) return target;
  const todo = target.value.todo;
  const ir: CommandIR = {
    intent: "todos.assign",
    projectId: context.projectId,
    projectSlug: context.projectSlug,
    entities: { localId: todo.localId, assigneeUserId: member.value.userId },
  };
  const validated = validateResolvedIR(ir, context);
  if (isCommandFailure(validated)) return validated;
  return {
    ok: true,
    value: withResolvedCommandDisplay({
      ir: validated.value,
      summary: "",
      confirmLabel: "",
      danger: false,
      requiresConfirmation: true,
      storyTitle: todo.title,
      assigneeName: member.value.name || member.value.email,
    }),
  };
}
