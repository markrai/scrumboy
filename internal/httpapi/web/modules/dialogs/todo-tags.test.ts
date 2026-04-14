// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const selectorState: {
  autocompleteSuggestion: string | null;
  availableTags: string[];
  availableTagsMap: Record<string, string>;
  board: any;
  boardMembers: any[];
  slug: string | null;
  tagColors: Record<string, string>;
  user: { id: number; name?: string; email?: string } | null;
} = {
  autocompleteSuggestion: null,
  availableTags: [],
  availableTagsMap: {},
  board: { project: { creatorUserId: 1 } },
  boardMembers: [],
  slug: 'alpha',
  tagColors: {},
  user: null,
};

const permissionState = {
  canEditTags: true,
};

const apiFetchMock = vi.fn();
const setAutocompleteSuggestionMock = vi.fn((next: string | null) => {
  selectorState.autocompleteSuggestion = next;
});
const setAvailableTagsMock = vi.fn();
const setAvailableTagsMapMock = vi.fn();
const setEditingTodoMock = vi.fn();
const setTagColorsMock = vi.fn();
const recordLocalMutationMock = vi.fn();

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../dom/elements.js', () => ({
  addTagBtn: document.getElementById('addTagBtn'),
  closeTodoBtn: document.getElementById('closeTodoBtn'),
  deleteTodoBtn: document.getElementById('deleteTodoBtn'),
  shareTodoBtn: document.getElementById('shareTodoBtn'),
  todoBody: document.getElementById('todoBody'),
  todoDialog: document.getElementById('todoDialog'),
  todoDialogTitle: document.getElementById('todoDialogTitle'),
  todoEstimationField: document.getElementById('todoEstimationField'),
  todoEstimationPoints: document.getElementById('todoEstimationPoints'),
  todoStatus: document.getElementById('todoStatus'),
  todoTags: document.getElementById('todoTags'),
  todoTitle: document.getElementById('todoTitle'),
}));

vi.mock('../realtime/guard.js', () => ({
  recordLocalMutation: recordLocalMutationMock,
}));

vi.mock('../state/selectors.js', () => ({
  getAutocompleteSuggestion: () => selectorState.autocompleteSuggestion,
  getAvailableTags: () => selectorState.availableTags,
  getAvailableTagsMap: () => selectorState.availableTagsMap,
  getBoard: () => selectorState.board,
  getBoardMembers: () => selectorState.boardMembers,
  getSlug: () => selectorState.slug,
  getTagColors: () => selectorState.tagColors,
  getUser: () => selectorState.user,
}));

vi.mock('../state/mutations.js', () => ({
  setAutocompleteSuggestion: setAutocompleteSuggestionMock,
  setAvailableTags: setAvailableTagsMock,
  setAvailableTagsMap: setAvailableTagsMapMock,
  setEditingTodo: setEditingTodoMock,
  setTagColors: setTagColorsMock,
}));

vi.mock('../sprints.js', () => ({
  normalizeSprints: (res: { sprints?: any[] } | null | undefined) => res?.sprints ?? [],
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  isAnonymousBoard: () => false,
  sanitizeHexColor: (color?: string | null) => {
    if (color && /^#[0-9a-fA-F]{6}$/.test(color.trim())) return color.trim();
    return null;
  },
  showToast: vi.fn(),
}));

vi.mock('./todo-permissions.js', () => ({
  computeTodoDialogPermissions: vi.fn(),
  getTodoFormPermissions: () => permissionState,
  setTodoFormPermissions: vi.fn(),
}));

function renderTagShell(): void {
  document.body.innerHTML = `
    <dialog id="todoDialog"></dialog>
    <button id="shareTodoBtn" type="button">Share</button>
    <div id="tagsChips"></div>
    <div class="tags-input-container">
      <input id="todoTags" type="text" />
      <button id="addTagBtn" type="button">Add</button>
    </div>
  `;
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadTodoTagsModule() {
  const mod = await import('./todo-tags.js');
  return mod;
}

describe('todo-tags', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    renderTagShell();
    selectorState.autocompleteSuggestion = null;
    selectorState.availableTags = [];
    selectorState.availableTagsMap = {};
    selectorState.board = { project: { creatorUserId: 1 } };
    selectorState.boardMembers = [];
    selectorState.slug = 'alpha';
    selectorState.tagColors = {};
    selectorState.user = null;
    permissionState.canEditTags = true;
    apiFetchMock.mockReset();
    setAutocompleteSuggestionMock.mockClear();
    setAvailableTagsMock.mockClear();
    setAvailableTagsMapMock.mockClear();
    setEditingTodoMock.mockClear();
    setTagColorsMock.mockClear();
    recordLocalMutationMock.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.resetModules();
    document.body.innerHTML = '';
    selectorState.autocompleteSuggestion = null;
    selectorState.availableTags = [];
    selectorState.availableTagsMap = {};
    selectorState.board = { project: { creatorUserId: 1 } };
    selectorState.boardMembers = [];
    selectorState.slug = 'alpha';
    selectorState.tagColors = {};
    selectorState.user = null;
    permissionState.canEditTags = true;
  });

  it('renders tag chips with sanitized colors and returns them in DOM order', async () => {
    selectorState.tagColors = {
      Bug: '#ff0000',
      Plain: 'invalid',
    };
    const mod = await loadTodoTagsModule();

    mod.renderTagsChips(['Bug', 'Plain'], { canRemove: true });

    const chips = Array.from(document.querySelectorAll('.tag-chip'));
    expect(chips).toHaveLength(2);
    expect(chips[0]?.getAttribute('data-tag')).toBe('Bug');
    expect(chips[0]?.getAttribute('style')).toContain('#ff0000');
    expect(chips[1]?.getAttribute('style') ?? '').not.toContain('invalid');
    expect(document.querySelectorAll('.tag-chip-remove')).toHaveLength(2);
    expect(mod.getTagsFromChips()).toEqual(['Bug', 'Plain']);

    mod.renderTagsChips(['Bug'], { canRemove: false });
    expect(document.querySelector('.tag-chip-remove')).toBeNull();
  });

  it('normalizes tags from the available-tags map and then falls back to existing chip casing', async () => {
    selectorState.availableTagsMap = {
      bug: 'Bug',
    };
    const mod = await loadTodoTagsModule();

    expect(mod.normalizeTagName('bug')).toBe('Bug');

    selectorState.availableTagsMap = {};
    mod.renderTagsChips(['Feature'], { canRemove: true });
    expect(mod.normalizeTagName('feature')).toBe('Feature');
    expect(mod.normalizeTagName('unknown')).toBe('unknown');
  });

  it('adds typed tags, accepts autocomplete suggestions, and ignores case-insensitive duplicates', async () => {
    selectorState.availableTags = ['Feature'];
    selectorState.availableTagsMap = {
      feature: 'Feature',
    };
    const mod = await loadTodoTagsModule();

    mod.setupTagAutocomplete();

    const input = document.getElementById('todoTags') as HTMLInputElement | null;
    if (!input) throw new Error('missing tag controls');

    input.value = 'Alpha, beta';
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(mod.getTagsFromChips()).toEqual(['Alpha', 'beta']);
    expect(input.value).toBe('');

    input.value = 'alpha';
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(mod.getTagsFromChips()).toEqual(['Alpha', 'beta']);

    input.value = 'fe';
    input.setSelectionRange(2, 2);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(selectorState.autocompleteSuggestion).toBe('Feature');
    expect(document.getElementById('tagAutocompleteSuggestion')).not.toBeNull();

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    expect(mod.getTagsFromChips()).toEqual(['Alpha', 'beta', 'Feature']);
    expect(selectorState.autocompleteSuggestion).toBeNull();
    expect(document.getElementById('tagAutocompleteSuggestion')).toBeNull();
  });

  it('clears autocomplete on Escape and blur using the existing timers', async () => {
    selectorState.availableTags = ['Feature'];
    selectorState.availableTagsMap = {
      feature: 'Feature',
    };
    const mod = await loadTodoTagsModule();

    mod.setupTagAutocomplete();

    const input = document.getElementById('todoTags') as HTMLInputElement | null;
    if (!input) throw new Error('missing todoTags input');

    input.value = 'fe';
    input.setSelectionRange(2, 2);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(document.getElementById('tagAutocompleteSuggestion')).not.toBeNull();

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(selectorState.autocompleteSuggestion).toBeNull();
    expect(document.getElementById('tagAutocompleteSuggestion')).toBeNull();

    input.value = 'fe';
    input.setSelectionRange(2, 2);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(document.getElementById('tagAutocompleteSuggestion')).not.toBeNull();

    input.dispatchEvent(new Event('blur', { bubbles: true }));
    vi.advanceTimersByTime(199);
    expect(document.getElementById('tagAutocompleteSuggestion')).not.toBeNull();
    vi.advanceTimersByTime(1);
    await flushPromises();
    expect(selectorState.autocompleteSuggestion).toBeNull();
    expect(document.getElementById('tagAutocompleteSuggestion')).toBeNull();
  });

  it('blocks add and remove flows when tag editing is disabled', async () => {
    const mod = await loadTodoTagsModule();
    permissionState.canEditTags = false;

    mod.renderTagsChips(['Blocked'], { canRemove: true });
    mod.setupTagAutocomplete();

    const input = document.getElementById('todoTags') as HTMLInputElement | null;
    const removeBtn = document.querySelector('.tag-chip-remove');
    if (!input || !(removeBtn instanceof HTMLElement)) throw new Error('missing disabled tag controls');

    input.value = 'Nope';
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    removeBtn.click();

    expect(mod.getTagsFromChips()).toEqual(['Blocked']);
  });

  it('does not double-bind the current input and can cleanly rebind after the dialog input is replaced', async () => {
    selectorState.availableTags = ['Feature'];
    selectorState.availableTagsMap = {
      feature: 'Feature',
    };
    const mod = await loadTodoTagsModule();

    mod.setupTagAutocomplete();
    mod.setupTagAutocomplete();
    setAutocompleteSuggestionMock.mockClear();

    const firstInput = document.getElementById('todoTags') as HTMLInputElement | null;
    if (!firstInput) throw new Error('missing first todoTags input');
    firstInput.value = 'fe';
    firstInput.setSelectionRange(2, 2);
    firstInput.dispatchEvent(new Event('input', { bubbles: true }));

    expect(setAutocompleteSuggestionMock).toHaveBeenCalledTimes(1);

    const replacement = firstInput.cloneNode(true) as HTMLInputElement;
    firstInput.replaceWith(replacement);
    mod.resetTodoTagAutocompleteBindings();
    setAutocompleteSuggestionMock.mockClear();

    mod.setupTagAutocomplete();

    replacement.value = 'fe';
    replacement.setSelectionRange(2, 2);
    replacement.dispatchEvent(new Event('input', { bubbles: true }));
    replacement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));

    expect(setAutocompleteSuggestionMock).toHaveBeenCalled();
    expect(mod.getTagsFromChips()).toEqual(['Feature']);
  });

  it('re-exports the tag helpers through todo.ts for coordinator compatibility', async () => {
    const tagsMod = await loadTodoTagsModule();
    const todoMod = await import('./todo.ts');

    expect(todoMod.normalizeTagName).toBe(tagsMod.normalizeTagName);
    expect(todoMod.getTagsFromChips).toBe(tagsMod.getTagsFromChips);
    expect(todoMod.renderTagsChips).toBe(tagsMod.renderTagsChips);
    expect(todoMod.setupTagAutocomplete).toBe(tagsMod.setupTagAutocomplete);
    expect(todoMod.removeTag).toBe(tagsMod.removeTag);
  });
});
