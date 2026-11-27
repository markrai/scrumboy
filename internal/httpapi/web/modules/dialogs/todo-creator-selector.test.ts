// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { apiFetchMock, authState, localeState } = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  authState: { userId: 7 },
  localeState: { prefix: '' },
}));

vi.mock('../api.js', () => ({ apiFetch: apiFetchMock }));
vi.mock('../state/selectors.js', () => ({
  getUser: () => authState.userId > 0 ? { id: authState.userId } : null,
}));
vi.mock('../i18n/index.js', () => ({
  t: (key: string, params?: Record<string, unknown>) => {
    const messages: Record<string, string> = {
      'common.close': 'Close',
      'todo.creator.current': 'Current creator',
      'todo.creator.edit': 'Change creator',
      'todo.creator.loadFailed': 'Other users could not be loaded. Project members are still available.',
      'todo.creator.loading': 'Loading other users…',
      'todo.creator.member': 'Project member',
      'todo.creator.noResults': 'No matching users',
      'todo.creator.nonMember': 'Not a project member',
      'todo.creator.reset': 'Reset',
      'todo.creator.resetLabel': 'Reset creator change',
      'todo.creator.searchLabel': 'Search creator candidates',
      'todo.creator.searchPlaceholder': 'Search users',
      'todo.creator.selectorLabel': 'Choose creator',
      'todo.creator.serviceAccount': 'Service account',
      'todo.creator.unrecordedAttribution': 'Creator not recorded',
      'todo.creator.unresolvedAttribution': 'Creator details unavailable',
      'todo.creator.userFallback': `User ${params?.id ?? ''}`,
      'todo.dialog.openedBy': `by ${params?.name ?? ''}`,
    };
    return `${localeState.prefix}${messages[key] ?? key}`;
  },
}));

function installDom(): void {
  document.body.innerHTML = `
    <span id="todoDialogCreatedBy"></span>
    <button id="todoCreatorEditBtn" type="button" hidden></button>
    <button id="todoCreatorResetBtn" type="button" hidden></button>
    <div id="todoCreatorSelector" hidden>
      <input id="todoCreatorSearch" />
      <button id="todoCreatorSelectorClose" type="button"></button>
      <div id="todoCreatorStatus"></div>
      <div id="todoCreatorOptions"></div>
    </div>
    <select id="todoAssignee"><option value="31" selected>Assignee</option></select>
  `;
}

function configure(
  mod: typeof import('./todo-creator-selector.js'),
  overrides: Partial<Parameters<typeof mod.configureTodoCreatorSelector>[0]> = {},
): void {
  mod.configureTodoCreatorSelector({
    generation: 1,
    projectId: 4,
    todoLocalId: 12,
    authUserId: authState.userId,
    canEdit: true,
    originalCreatorUserId: 10,
    members: [
      { userId: 10, name: 'Ada', role: 'maintainer' },
      { userId: 11, name: 'Grace', role: 'viewer' },
    ],
    isContextCurrent: () => true,
    isEditAuthorized: () => true,
    ...overrides,
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function flush(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

describe('todo creator selector', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    apiFetchMock.mockReset();
    authState.userId = 7;
    localeState.prefix = '';
    apiFetchMock.mockResolvedValue([]);
    installDom();
  });

  afterEach(async () => {
    const mod = await import('./todo-creator-selector.js');
    mod.resetTodoCreatorSelector();
    document.body.innerHTML = '';
  });

  it('keeps project members first and deduplicates candidates by numeric user ID', async () => {
    const { mergeTodoCreatorCandidates } = await import('./todo-creator-selector.js');
    const candidates = mergeTodoCreatorCandidates(
      [
        { userId: 3, name: 'Member', role: 'viewer', isService: true },
        { userId: 3, name: 'Duplicate member', role: 'viewer' },
      ],
      [
        { id: 3, name: 'Duplicate available' },
        { id: 9, name: 'Outside', email: 'outside@example.test', isService: true },
        { id: 0, name: 'Invalid' },
      ],
    );

    expect(candidates).toEqual([
      { id: 3, name: 'Member', email: '', isMember: true, isService: true },
      { id: 9, name: 'Outside', email: 'outside@example.test', isMember: false, isService: true },
    ]);
  });

  it('shows read-only member attribution without exposing the editing control or privileged lookup', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { canEdit: false });

    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Ada');
    expect((document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('normalizes a missing authenticated identity without attempting a privileged lookup', async () => {
    authState.userId = 0;
    const mod = await import('./todo-creator-selector.js');
    configure(mod, {
      authUserId: null,
      members: [],
      isEditAuthorized: () => false,
    });

    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('Creator details unavailable');
    expect((document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('keeps read-only attribution when edit authorization is already absent before opening', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { isEditAuthorized: () => false });

    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Ada');
    expect((document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('offers null attribution to eligible maintainers and loads non-members only when opened', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { originalCreatorUserId: null });

    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('Creator not recorded');
    const button = document.getElementById('todoCreatorEditBtn') as HTMLButtonElement;
    expect(button.hidden).toBe(false);
    expect(apiFetchMock).not.toHaveBeenCalled();

    button.click();
    await flush();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/4/available-users', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it('resolves non-member attribution after authorized loading without creating a pending mutation', async () => {
    apiFetchMock.mockResolvedValue([{ id: 27, name: 'Outside creator', email: 'outside@example.test' }]);
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { originalCreatorUserId: 27 });

    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('Creator details unavailable');
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Outside creator');
    expect(document.querySelector('[data-user-id="27"]')?.textContent).toContain('Not a project member');
  });

  it('filters candidates and changes creator independently from assignment', async () => {
    apiFetchMock.mockResolvedValue([{ id: 27, name: 'Outside creator', email: 'outside@example.test' }]);
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();

    const search = document.getElementById('todoCreatorSearch') as HTMLInputElement;
    search.value = 'outside';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    expect(document.querySelectorAll('[role="option"]')).toHaveLength(1);
    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));

    expect(mod.getPendingTodoCreatorId()).toBe(27);
    expect((document.getElementById('todoAssignee') as HTMLSelectElement).value).toBe('31');
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Outside creator');
  });

  it('supports keyboard dismissal and selection while rendering candidate names as text', async () => {
    apiFetchMock.mockResolvedValue([{ id: 27, name: '<img src=x onerror=alert(1)>', email: 'outside@example.test' }]);
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    const button = document.getElementById('todoCreatorEditBtn') as HTMLButtonElement;
    const search = document.getElementById('todoCreatorSearch') as HTMLInputElement;

    button.click();
    await apiFetchMock.mock.results[0]?.value;
    await flush();
    expect(document.activeElement).toBe(search);
    expect(document.querySelector('[data-user-id="27"]')?.textContent).toContain('<img src=x onerror=alert(1)>');
    expect(document.querySelector('[data-user-id="27"] img')).toBeNull();

    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect((document.getElementById('todoCreatorSelector') as HTMLElement).hidden).toBe(true);
    expect(document.activeElement).toBe(button);
    expect(mod.getPendingTodoCreatorId()).toBeNull();

    button.click();
    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(mod.getPendingTodoCreatorId()).toBe(11);
    expect(document.activeElement).toBe(button);
  });

  it('preserves pending selection across locale refreshes and resets to the original clean value', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();

    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    expect(mod.getPendingTodoCreatorId()).toBe(11);
    localeState.prefix = '[translated] ';
    mod.refreshTodoCreatorSelectorLocale();
    expect(mod.getPendingTodoCreatorId()).toBe(11);
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('[translated] by Grace');
    expect((document.getElementById('todoCreatorResetBtn') as HTMLButtonElement).hidden).toBe(false);

    (document.getElementById('todoCreatorResetBtn') as HTMLButtonElement).click();
    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect((document.getElementById('todoCreatorResetBtn') as HTMLButtonElement).hidden).toBe(true);
  });

  it('removes the pending mutation when the original recorded creator is selected again', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    expect(mod.getPendingTodoCreatorId()).toBe(11);

    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    (document.querySelector('[data-user-id="10"]') as HTMLElement).click();

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect((document.getElementById('todoCreatorResetBtn') as HTMLButtonElement).hidden).toBe(true);
  });

  it('resets a changed creator to original null attribution without offering a clear choice', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { originalCreatorUserId: null });
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();

    expect(document.querySelector('[data-user-id=""]')).toBeNull();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    expect(mod.getPendingTodoCreatorId()).toBe(11);
    (document.getElementById('todoCreatorResetBtn') as HTMLButtonElement).click();

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('Creator not recorded');
  });

  it('always cancels Enter, ignores it during composition, and does not select without results', async () => {
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();
    const search = document.getElementById('todoCreatorSearch') as HTMLInputElement;
    search.value = 'missing';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    const noResultsEnter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    search.dispatchEvent(noResultsEnter);
    expect(noResultsEnter.defaultPrevented).toBe(true);
    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect((document.getElementById('todoCreatorSelector') as HTMLElement).hidden).toBe(false);

    search.value = 'Grace';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    search.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
    const composingEnter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    search.dispatchEvent(composingEnter);
    expect(composingEnter.defaultPrevented).toBe(true);
    expect(mod.getPendingTodoCreatorId()).toBeNull();
    search.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
  });

  it('ignores stale candidate responses after another todo replaces the dialog generation', async () => {
    const first = deferred<any[]>();
    apiFetchMock.mockReturnValueOnce(first.promise).mockResolvedValueOnce([]);
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { generation: 1, todoLocalId: 12, originalCreatorUserId: 10 });
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();

    configure(mod, {
      generation: 2,
      todoLocalId: 13,
      originalCreatorUserId: 11,
      members: [{ userId: 11, name: 'Grace', role: 'viewer' }],
    });
    first.resolve([{ id: 99, name: 'Stale user' }]);
    await flush();

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Grace');
    expect(document.querySelector('[data-user-id="99"]')).toBeNull();
  });

  it('keeps the original creator and usable member choices when non-member loading fails', async () => {
    apiFetchMock.mockRejectedValue(new Error('offline'));
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    await expect(apiFetchMock.mock.results[0]?.value).rejects.toThrow('offline');
    await flush();
    expect(document.getElementById('todoCreatorStatus')?.textContent).toContain('could not be loaded');

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(document.querySelectorAll('[role="option"]')).toHaveLength(2);
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    expect(mod.getPendingTodoCreatorId()).toBe(11);
  });

  it('aborts candidate loading and ignores its response after membership authorization is revoked', async () => {
    const request = deferred<any[]>();
    apiFetchMock.mockReturnValue(request.promise);
    let editAuthorized = true;
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { isEditAuthorized: () => editAuthorized });
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    const signal = apiFetchMock.mock.calls[0]?.[1]?.signal as AbortSignal;

    editAuthorized = false;
    const events = await import('../events.js');
    events.emit('members-updated', { projectId: 4 });

    expect(signal.aborted).toBe(true);
    expect((document.getElementById('todoCreatorSelector') as HTMLElement).hidden).toBe(true);
    expect((document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Ada');
    request.resolve([{ id: 27, name: 'Late privileged user', email: 'late@example.test' }]);
    await flush();
    expect(document.body.textContent).not.toContain('Late privileged user');
  });

  it('clears loaded privileged candidates and pending selection when membership authorization is revoked', async () => {
    apiFetchMock.mockResolvedValue([{ id: 27, name: 'Outside creator', email: 'outside@example.test' }]);
    let editAuthorized = true;
    const mod = await import('./todo-creator-selector.js');
    configure(mod, { isEditAuthorized: () => editAuthorized });
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();
    (document.querySelector('[data-user-id="27"]') as HTMLElement).click();
    expect(mod.getPendingTodoCreatorId()).toBe(27);

    editAuthorized = false;
    const events = await import('../events.js');
    events.emit('members-updated', { projectId: 4 });

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(document.body.textContent).not.toContain('Outside creator');
    expect(document.body.textContent).not.toContain('outside@example.test');
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Ada');
    localeState.prefix = '[translated] ';
    mod.refreshTodoCreatorSelectorLocale();
    expect(document.body.textContent).not.toContain('Outside creator');
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('[translated] by Ada');
  });

  it.each([401, 403, 404])('invalidates and clears privileged identity data after authorization loss (%s)', async (status) => {
    const error = Object.assign(new Error('forbidden'), { status });
    apiFetchMock.mockRejectedValue(error);
    const mod = await import('./todo-creator-selector.js');
    configure(mod);
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();

    expect((document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).hidden).toBe(true);
    expect((document.getElementById('todoCreatorSearch') as HTMLInputElement).value).toBe('');
    expect(document.getElementById('todoCreatorOptions')?.children).toHaveLength(0);
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Ada');
    expect(mod.getPendingTodoCreatorId()).toBeNull();
  });

  it('clears the whole context on authentication changes and only editing state on authorization loss', async () => {
    let contextCurrent = true;
    let editAuthorized = true;
    const mod = await import('./todo-creator-selector.js');
    configure(mod, {
      isContextCurrent: () => contextCurrent,
      isEditAuthorized: () => editAuthorized,
    });
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    authState.userId = 99;

    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect(document.getElementById('todoCreatorOptions')?.children).toHaveLength(0);
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('');

    authState.userId = 7;
    configure(mod, {
      isContextCurrent: () => contextCurrent,
      isEditAuthorized: () => editAuthorized,
    });
    (document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).click();
    await flush();
    (document.querySelector('[data-user-id="11"]') as HTMLElement).click();
    editAuthorized = false;
    mod.refreshTodoCreatorSelectorLocale();
    expect(mod.getPendingTodoCreatorId()).toBeNull();
    expect((document.getElementById('todoCreatorEditBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(document.getElementById('todoCreatorOptions')?.children).toHaveLength(0);
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('by Ada');

    contextCurrent = false;
    mod.refreshTodoCreatorSelectorAuthorization();
    expect(document.getElementById('todoDialogCreatedBy')?.textContent).toBe('');
  });
});
