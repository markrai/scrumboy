// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const selectorState: {
  user: { id: number } | null;
} = {
  user: null,
};

const boardModeState: {
  anonymous: boolean;
  temporary: boolean;
} = {
  anonymous: false,
  temporary: false,
};

vi.mock('../state/selectors.js', () => ({
  getUser: () => selectorState.user,
}));

vi.mock('../utils.js', () => ({
  isAnonymousBoard: () => boardModeState.anonymous,
  isTemporaryBoard: () => boardModeState.temporary,
}));

async function loadPermissionsModule() {
  const mod = await import('./todo-permissions.js');
  return mod;
}

describe('todo-permissions', () => {
  beforeEach(() => {
    selectorState.user = null;
    boardModeState.anonymous = false;
    boardModeState.temporary = false;
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
    selectorState.user = null;
    boardModeState.anonymous = false;
    boardModeState.temporary = false;
  });

  it('grants the full durable-board edit matrix to maintainers', async () => {
    const mod = await loadPermissionsModule();

    const permissions = mod.computeTodoDialogPermissions({
      board: { project: { creatorUserId: 1 } },
      mode: 'edit',
      todo: { assigneeUserId: 7 },
      role: 'maintainer',
    });

    expect(permissions).toEqual({
      canChangeSprint: true,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
      canEditAssignment: true,
      canDeleteTodo: true,
      canEditTitle: true,
      canEditStatus: true,
      canSubmitTodo: true,
      canEditLinks: true,
    });
  });

  it('lets assigned contributors edit notes and submit edits but not delete or reassign', async () => {
    selectorState.user = { id: 42 };
    const mod = await loadPermissionsModule();

    const permissions = mod.computeTodoDialogPermissions({
      board: { project: { creatorUserId: 1 } },
      mode: 'edit',
      todo: { assigneeUserId: 42 },
      role: 'contributor',
    });

    expect(permissions).toEqual({
      canChangeSprint: false,
      canChangeEstimation: false,
      canEditTags: false,
      canEditNotes: true,
      canEditAssignment: false,
      canDeleteTodo: false,
      canEditTitle: false,
      canEditStatus: false,
      canSubmitTodo: true,
      canEditLinks: true,
    });
  });

  it('blocks unassigned contributors from submitting edits while still allowing link edits', async () => {
    selectorState.user = { id: 42 };
    const mod = await loadPermissionsModule();

    const permissions = mod.computeTodoDialogPermissions({
      board: { project: { creatorUserId: 1 } },
      mode: 'edit',
      todo: { assigneeUserId: 99 },
      role: 'contributor',
    });

    expect(permissions).toEqual({
      canChangeSprint: false,
      canChangeEstimation: false,
      canEditTags: false,
      canEditNotes: false,
      canEditAssignment: false,
      canDeleteTodo: false,
      canEditTitle: false,
      canEditStatus: false,
      canSubmitTodo: false,
      canEditLinks: true,
    });
  });

  it('keeps viewers read-only on durable boards', async () => {
    selectorState.user = { id: 7 };
    const mod = await loadPermissionsModule();

    const permissions = mod.computeTodoDialogPermissions({
      board: { project: { creatorUserId: 1 } },
      mode: 'edit',
      todo: { assigneeUserId: 7 },
      role: 'viewer',
    });

    expect(permissions).toEqual({
      canChangeSprint: false,
      canChangeEstimation: false,
      canEditTags: false,
      canEditNotes: false,
      canEditAssignment: false,
      canDeleteTodo: false,
      canEditTitle: false,
      canEditStatus: false,
      canSubmitTodo: false,
      canEditLinks: false,
    });
  });

  it('treats anonymous boards as the base-maintainer path while still disallowing sprint and assignment changes', async () => {
    boardModeState.anonymous = true;
    boardModeState.temporary = true;
    const mod = await loadPermissionsModule();

    const permissions = mod.computeTodoDialogPermissions({
      board: { project: { expiresAt: '2026-04-14T12:00:00Z' } },
      mode: 'edit',
      todo: { assigneeUserId: 7 },
      role: 'viewer',
    });

    expect(permissions).toEqual({
      canChangeSprint: false,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
      canEditAssignment: false,
      canDeleteTodo: true,
      canEditTitle: true,
      canEditStatus: true,
      canSubmitTodo: true,
      canEditLinks: true,
    });
  });

  it('uses the temporary-board create flow to allow submit/title/status/tags/estimation without sprint or assignment changes', async () => {
    boardModeState.temporary = true;
    const mod = await loadPermissionsModule();

    const permissions = mod.computeTodoDialogPermissions({
      board: { project: { creatorUserId: 1, expiresAt: '2026-04-14T12:00:00Z' } },
      mode: 'create',
      role: 'viewer',
    });

    expect(permissions).toEqual({
      canChangeSprint: false,
      canChangeEstimation: true,
      canEditTags: true,
      canEditNotes: true,
      canEditAssignment: false,
      canDeleteTodo: false,
      canEditTitle: true,
      canEditStatus: true,
      canSubmitTodo: true,
      canEditLinks: false,
    });
  });

  it('exposes the mutable permission snapshot through set/get without leaking caller mutations back in', async () => {
    const mod = await loadPermissionsModule();
    const expected = mod.computeTodoDialogPermissions({
      board: { project: { creatorUserId: 1 } },
      mode: 'edit',
      role: 'maintainer',
      todo: { assigneeUserId: 9 },
    });

    mod.setTodoFormPermissions(expected);
    const first = mod.getTodoFormPermissions();
    expect(first).toEqual(expected);

    const mutated = { ...first, canDeleteTodo: false };
    expect(mutated.canDeleteTodo).toBe(false);
    expect(mod.getTodoFormPermissions()).toEqual(expected);
  });
});
