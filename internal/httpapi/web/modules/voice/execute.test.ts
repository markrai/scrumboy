import { describe, expect, it, vi } from 'vitest';
import { buildMcpCall, executeCommandIR } from './execute.js';

describe('voice command MCP mapping', () => {
  it('maps supported intents to existing MCP tools only', () => {
    expect(buildMcpCall({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login' },
    })).toEqual({
      tool: 'todos.create',
      input: { projectSlug: 'alpha', title: 'Fix login' },
    });

    expect(buildMcpCall({
      intent: 'todos.move',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, toColumnKey: 'done' },
    })).toEqual({
      tool: 'todos.move',
      input: { projectSlug: 'alpha', localId: 56, toColumnKey: 'done' },
    });

    expect(buildMcpCall({
      intent: 'todos.delete',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    })).toEqual({
      tool: 'todos.delete',
      input: { projectSlug: 'alpha', localId: 56 },
    });

    expect(buildMcpCall({
      intent: 'todos.assign',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, assigneeUserId: 7 },
    })).toEqual({
      tool: 'todos.update',
      input: { projectSlug: 'alpha', localId: 56, patch: { assigneeUserId: 7 } },
    });
  });

  it('records mutation before MCP and refreshes only after success', async () => {
    const events: string[] = [];
    const callTool = vi.fn().mockImplementation(async () => {
      events.push('call');
      return { todo: { localId: 56 } };
    });
    const recordMutation = vi.fn(() => events.push('record'));
    const refreshBoard = vi.fn().mockImplementation(async () => {
      events.push('refresh');
    });

    await executeCommandIR({
      intent: 'todos.delete',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    }, { callTool, recordMutation, refreshBoard });

    expect(recordMutation).toHaveBeenCalledTimes(1);
    expect(callTool).toHaveBeenCalledWith('todos.delete', { projectSlug: 'alpha', localId: 56 });
    expect(refreshBoard).toHaveBeenCalledTimes(1);
    expect(events).toEqual(['record', 'call', 'refresh']);
  });

  it('opens todos through the injected board navigation path without MCP mutation', async () => {
    const callTool = vi.fn();
    const recordMutation = vi.fn();
    const refreshBoard = vi.fn();
    const openTodo = vi.fn().mockResolvedValue(undefined);

    await executeCommandIR({
      intent: 'open_todo',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    }, { callTool, recordMutation, refreshBoard, openTodo });

    expect(openTodo).toHaveBeenCalledWith(56);
    expect(callTool).not.toHaveBeenCalled();
    expect(recordMutation).not.toHaveBeenCalled();
    expect(refreshBoard).not.toHaveBeenCalled();
  });

  it('requires an injected open todo action for open commands', async () => {
    await expect(executeCommandIR({
      intent: 'open_todo',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    })).rejects.toThrow('Open todo action is unavailable.');
  });

  it('does not refresh after MCP failure', async () => {
    const callTool = vi.fn().mockRejectedValue(new Error('forbidden'));
    const recordMutation = vi.fn();
    const refreshBoard = vi.fn().mockResolvedValue(undefined);

    await expect(executeCommandIR({
      intent: 'todos.delete',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    }, { callTool, recordMutation, refreshBoard })).rejects.toThrow('forbidden');

    expect(recordMutation).toHaveBeenCalledTimes(1);
    expect(refreshBoard).not.toHaveBeenCalled();
  });

  it('passes abort signals to MCP callers when provided', async () => {
    const callTool = vi.fn().mockResolvedValue({ todo: { localId: 56 } });
    const controller = new AbortController();

    await executeCommandIR({
      intent: 'todos.move',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, toColumnKey: 'done' },
    }, { callTool, signal: controller.signal });

    expect(callTool).toHaveBeenCalledWith(
      'todos.move',
      { projectSlug: 'alpha', localId: 56, toColumnKey: 'done' },
      { signal: controller.signal },
    );
  });
});
