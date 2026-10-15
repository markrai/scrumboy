// @vitest-environment happy-dom
/**
 * Characterization of story-card rendering, frozen before the shared
 * story-card-content extraction. The legacy* functions below are verbatim
 * copies of the pre-extraction renderers. Member board and Wall output must
 * stay byte-identical; public cards must stay DOM-equivalent (whitespace and
 * style-string spacing may differ).
 */
import { beforeAll, describe, expect, it, vi } from 'vitest';
import enCatalog from '../i18n/locales/en.json';
import { formatDueBadge, formatDueDateTooltip, isOverdue } from '../dialogs/todo-due.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import { t } from '../i18n/index.js';
import { escapeHTML, renderAvatarContent, sanitizeHexColor } from '../utils.js';
import type { BoardMember } from '../state/state.js';
import type { Todo } from '../types.js';
import type { PublicPriority, PublicTodo, PublicWorkflowColumn } from '../public-board-api.js';
import { WALL_STORY_WIDTH } from '../dialogs/wall-story-geometry.js';
import { buildStoryElement, type WallStory } from '../dialogs/wall-rendering.js';
import { renderStoryCardContent, renderTodoCard, type RenderTodoCardOpts } from './board-rendering.js';
import { renderPublicTodoCard } from './public-board-rendering.js';

// ---- Frozen legacy renderers (do not edit) ---------------------------------

function legacyRenderStoryCardContent(
  todo: Todo,
  membersByUserId?: Record<number, BoardMember>,
  opts?: RenderTodoCardOpts,
): string {
  const showPoints = !!opts?.showPointsMode && todo.estimationPoints != null;
  const tagColors = opts?.tagColors ?? null;
  const tags = (todo.tags || [])
    .map((tagName) => {
      const tagColor = tagColors ? (tagColors[tagName] ?? null) : null;
      const safe = sanitizeHexColor(tagColor);
      const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"` : "";
      return `<span class="tag" ${colorStyle}>${escapeHTML(tagName)}</span>`;
    })
    .join("");
  const assignee = membersByUserId != null && todo.assigneeUserId != null ? membersByUserId[todo.assigneeUserId] : null;
  const avatarHTML = assignee
    ? `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`
    : '';
  const pointsHTML = showPoints
    ? `<span class="card__points"${titleAttr(FIELD_TOOLTIPS.estimationPoints)} aria-label="${escapeHTML(t("todo.fields.estimationPoints"))}" data-i18n-aria-label="todo.fields.estimationPoints">${todo.estimationPoints}</span>`
    : "";
  const priorityTier = todo.priorityKey ? opts?.priorityTiers?.[todo.priorityKey] : null;
  const priorityColor = sanitizeHexColor(priorityTier?.color ?? null);
  const priorityStyle = priorityColor
    ? ` style="border-color:${priorityColor}; background:${priorityColor}20; color:${priorityColor};"`
    : "";
  const priorityHTML = priorityTier
    ? `<span class="card__priority"${priorityStyle}${titleAttr(FIELD_TOOLTIPS.priority)} aria-label="${escapeHTML(t("todo.fields.priority"))}: ${escapeHTML(priorityTier.name)}">${escapeHTML(priorityTier.name)}</span>`
    : "";
  let dueHTML = "";
  if (todo.dueAt) {
    const label = formatDueBadge(todo.dueAt);
    if (label) {
      const doneKey = (todo.columnKey || todo.status || "").toLowerCase();
      const overdue = isOverdue(todo.dueAt, opts?.doneColumnKeys?.has(doneKey) ?? false);
      const aria = escapeHTML(t("todo.due.cardLabel", { date: label }));
      dueHTML = `<span class="card__due${overdue ? " card__due--overdue" : ""}" title="${aria}" aria-label="${aria}">${escapeHTML(label)}</span>`;
    }
  }
  const footerContent = dueHTML + priorityHTML + pointsHTML + avatarHTML;
  return `
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${todo.localId}</span>
          <span class="card__title">${escapeHTML(todo.title)}</span>
        </div>
        ${tags || footerContent ? `
  <div class="card__tags">
    <span class="card__tags-list">
      ${tags}
    </span>
    <span class="card__badges">
      ${footerContent}
    </span>
  </div>
` : ""}
      </div>
  `;
}

function legacyRenderTodoCard(
  todo: Todo,
  columnColor?: string,
  membersByUserId?: Record<number, BoardMember>,
  opts?: RenderTodoCardOpts,
): string {
  const content = legacyRenderStoryCardContent(todo, membersByUserId, opts);
  const borderStyle = columnColor ? ` style="border-color:${escapeHTML(columnColor)}"` : "";
  const selectedClass = opts?.selectedIds?.has(todo.id) ? " card--selected" : "";
  const doneKey = (todo.columnKey || todo.status || "").toLowerCase();
  const overdue = !todo.archivedAt
    && isOverdue(todo.dueAt, opts?.doneColumnKeys?.has(doneKey) ?? false);
  let overdueWarningHTML = "";
  if (overdue && todo.dueAt) {
    const fullDate = formatDueDateTooltip(todo.dueAt);
    if (fullDate) {
      const warning = escapeHTML(t("todo.due.overdueWarning", { date: fullDate }));
      overdueWarningHTML = `<span class="card__overdue-warning" title="${escapeHTML(fullDate)}" aria-label="${warning}">!</span>`;
    }
  }
  const dragHandleHTML = `
      <div class="card__drag-handle" aria-label="${escapeHTML(t("board.todo.dragCard"))}" data-i18n-aria-label="board.todo.dragCard">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
          <circle cx="4" cy="3" r="1.5"/>
          <circle cx="4" cy="8" r="1.5"/>
          <circle cx="4" cy="13" r="1.5"/>
          <circle cx="12" cy="3" r="1.5"/>
          <circle cx="12" cy="8" r="1.5"/>
          <circle cx="12" cy="13" r="1.5"/>
        </svg>
      </div>
    `;
  return `
    <button class="card card--${todo.status.toLowerCase()}${selectedClass}"${borderStyle} data-todo-id="${todo.id}" data-todo-local-id="${todo.localId}"${todo.assigneeUserId != null ? ` data-assignee-user-id="${todo.assigneeUserId}"` : ""} id="todo_${todo.id}" type="button">
      ${content}
      <div class="card__top-actions">${overdueWarningHTML}${dragHandleHTML}</div>
    </button>
  `;
}

function legacyBuildStoryElement(
  story: WallStory,
  membersByUserId?: Record<number, BoardMember>,
  opts?: RenderTodoCardOpts,
): HTMLElement {
  const el = document.createElement("div");
  const archivedClass = story.todo.archivedAt ? " wall-story--archived" : "";
  el.className = `wall-story card card--${story.todo.status.toLowerCase()}${archivedClass}`;
  el.dataset.storyLocalId = String(story.localId);
  el.dataset.version = String(story.version);
  el.dataset.todoId = String(story.todo.id);
  el.style.left = `${Math.round(story.x)}px`;
  el.style.top = `${Math.round(story.y)}px`;
  el.style.width = `${WALL_STORY_WIDTH}px`;
  el.setAttribute("role", "button");
  el.setAttribute("tabindex", "0");
  el.setAttribute("aria-label", `${t("wall.menu.openStory")} #${story.localId}`);
  el.innerHTML = legacyRenderStoryCardContent(story.todo, membersByUserId, opts);
  return el;
}

function legacyAttrI18n(kind: 'text' | 'aria-label' | 'placeholder' | 'title', key: string): string {
  return ` data-i18n-${kind}="${escapeHTML(key)}"`;
}

function legacyColorStyle(color: string | null, withText = false): string {
  const safe = sanitizeHexColor(color ?? undefined);
  if (!safe) return '';
  return ` style="border-color: ${safe}; background: ${safe}20;${withText ? ` color: ${safe};` : ''}"`;
}

function legacyTagColorStyle(color: string | null): string {
  const safe = sanitizeHexColor(color ?? undefined);
  if (!safe) return '';
  return ` style="border-color: ${safe}; background: ${safe}20; color: var(--text);"`;
}

function legacyRenderPublicTodoCard(
  todo: PublicTodo,
  column: PublicWorkflowColumn,
  priorities: Record<string, PublicPriority>,
  showPoints: boolean,
): string {
  const tags = todo.tags
    .map((tag) => `<span class="tag"${legacyTagColorStyle(tag.color)}>${escapeHTML(tag.name)}</span>`)
    .join('');
  const priority = todo.priorityKey ? priorities[todo.priorityKey] : undefined;
  const priorityHTML = priority
    ? `<span class="card__priority"${legacyColorStyle(priority.color, true)} aria-label="${escapeHTML(t('todo.fields.priority'))}: ${escapeHTML(priority.name)}">${escapeHTML(priority.name)}</span>`
    : '';
  const pointsHTML = showPoints && todo.estimationPoints != null
    ? `<span class="card__points" aria-label="${escapeHTML(t('todo.fields.estimationPoints'))}"${legacyAttrI18n('aria-label', 'todo.fields.estimationPoints')}>${todo.estimationPoints}</span>`
    : '';
  const footer = priorityHTML + pointsHTML;
  const border = sanitizeHexColor(column.color ?? undefined);
  return `
    <button class="card card--public" type="button" data-public-local-id="${todo.localId}"${border ? ` style="border-color:${border}"` : ''}>
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${todo.localId}</span>
          <span class="card__title">${escapeHTML(todo.title)}</span>
        </div>
        ${tags || footer ? `<div class="card__tags"><span class="card__tags-list">${tags}</span><span class="card__badges">${footer}</span></div>` : ''}
      </div>
    </button>`;
}

// ---- Fixtures ----------------------------------------------------------------

const hostile = '"><img src=x onerror=alert(1)><script>bad()</script>&\'';
const past = '2020-06-15T12:00:00Z';
const future = '2099-06-15T12:00:00Z';

const members: Record<number, BoardMember> = {
  3: { userId: 3, name: 'Ada Lovelace', email: 'ada@example.com', image: null, role: 'maintainer' } as BoardMember,
  4: { userId: 4, name: '', email: 'grace@example.com', image: 'https://example.com/a.png', role: 'contributor' } as BoardMember,
  5: { userId: 5, name: hostile, email: hostile, image: null, role: 'viewer' } as BoardMember,
};

const fullOpts: RenderTodoCardOpts = {
  tagColors: { ui: '#00ff00', bad: 'url(evil)', [hostile]: '#123456', spaced: '  #abcdef  ' },
  showPointsMode: true,
  selectedIds: new Set([2]),
  priorityTiers: {
    high: { name: 'High', color: '#EF4444' },
    nocolor: { name: 'Plain', color: '' },
    evil: { name: hostile, color: 'javascript:alert(1)' },
  },
  doneColumnKeys: new Set(['done']),
};

function todo(over: Partial<Todo> = {}): Todo {
  return { id: 1, localId: 7, title: 'Ship it', body: 'hidden body', status: 'DOING', columnKey: 'doing', tags: [], ...over } as Todo;
}

const todoCases: Array<[string, Todo]> = [
  ['minimal (no footer)', todo()],
  ['hostile title', todo({ title: hostile })],
  ['empty title', todo({ title: '' })],
  ['tags with valid, invalid, missing, padded and hostile colors', todo({ tags: ['ui', 'bad', 'nocolor', 'spaced', hostile] })],
  ['priority with color', todo({ priorityKey: 'high' })],
  ['priority with empty color', todo({ priorityKey: 'nocolor' })],
  ['priority with hostile name and unsafe color', todo({ priorityKey: 'evil' })],
  ['unknown priority key', todo({ priorityKey: 'missing' })],
  ['zero points', todo({ estimationPoints: 0 })],
  ['points', todo({ estimationPoints: 13 })],
  ['null points', todo({ estimationPoints: null })],
  ['future due', todo({ dueAt: future })],
  ['overdue', todo({ dueAt: past })],
  ['overdue but done column', todo({ dueAt: past, status: 'DONE', columnKey: 'done' })],
  ['overdue, done by status fallback', todo({ dueAt: past, status: 'DONE', columnKey: undefined })],
  ['overdue archived', todo({ dueAt: past, archivedAt: '2020-06-16T00:00:00Z' } as Partial<Todo>)],
  ['invalid due', todo({ dueAt: 'not-a-date' })],
  ['assignee initials', todo({ assigneeUserId: 3 })],
  ['assignee image, email title fallback', todo({ assigneeUserId: 4 })],
  ['hostile assignee', todo({ assigneeUserId: 5 })],
  ['assignee missing from members', todo({ assigneeUserId: 99 })],
  ['selected', todo({ id: 2 })],
  ['everything', todo({
    id: 2, title: hostile, tags: ['ui', 'bad', hostile], priorityKey: 'high', estimationPoints: 0,
    dueAt: past, assigneeUserId: 3,
  })],
];

const optCases: Array<[string, RenderTodoCardOpts | undefined]> = [
  ['no opts', undefined],
  ['full opts', fullOpts],
  ['points hidden', { ...fullOpts, showPointsMode: false }],
  ['no tag colors', { ...fullOpts, tagColors: undefined }],
];

beforeAll(async () => {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
});

// ---- Member board and Wall: byte-identical ---------------------------------

describe('member story card characterization', () => {
  for (const [todoName, value] of todoCases) {
    for (const [optName, opts] of optCases) {
      it(`renderStoryCardContent and renderTodoCard match legacy output: ${todoName} / ${optName}`, () => {
        for (const membersArg of [members, undefined]) {
          expect(renderStoryCardContent(value, membersArg, opts)).toBe(legacyRenderStoryCardContent(value, membersArg, opts));
          expect(renderTodoCard(value, '#336699', membersArg, opts)).toBe(legacyRenderTodoCard(value, '#336699', membersArg, opts));
          expect(renderTodoCard(value, undefined, membersArg, opts)).toBe(legacyRenderTodoCard(value, undefined, membersArg, opts));
        }
      });
    }
  }

  it('keeps the observed member markup details', () => {
    const html = renderTodoCard(todo({ id: 2, tags: ['ui', 'nocolor'], priorityKey: 'high', estimationPoints: 0, dueAt: past, assigneeUserId: 3 }), '#336699', members, fullOpts);
    expect(html).toContain('data-todo-id="2" data-todo-local-id="7" data-assignee-user-id="3" id="todo_2"');
    expect(html).toContain('card card--doing card--selected');
    expect(html).toContain('<span class="tag" style="border-color: #00ff00; background: #00ff0020; color: #00ff00;">ui</span>');
    expect(html).toContain('<span class="tag" >nocolor</span>');
    expect(html).toContain('style="border-color:#EF4444; background:#EF444420; color:#EF4444;" title="');
    expect(html).toContain('aria-label="Priority: High">High</span>');
    expect(html).toContain('data-i18n-aria-label="todo.fields.estimationPoints">0</span>');
    expect(html).toContain('<div class="todo-avatar" title="Ada Lovelace">');
    expect(html).toContain('card__overdue-warning');
    expect(html).toContain('card__drag-handle');
    const badges = html.slice(html.indexOf('card__badges'));
    const order = ['card__due', 'card__priority', 'card__points', 'todo-avatar'].map((c) => badges.indexOf(c));
    expect(order.every((i) => i > 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
  });
});

describe('Wall story characterization', () => {
  const wallOpts: RenderTodoCardOpts[] = [
    { ...fullOpts, showPointsMode: true, selectedIds: undefined },
    { ...fullOpts, showPointsMode: false, selectedIds: undefined },
  ];
  for (const [todoName, value] of todoCases) {
    it(`buildStoryElement matches legacy output: ${todoName}`, () => {
      const story = { localId: value.localId, x: 10.4, y: 20.6, version: 3, todo: value } as unknown as WallStory;
      for (const opts of [...wallOpts, undefined]) {
        for (const membersArg of [members, undefined]) {
          expect(buildStoryElement(story, membersArg, opts).outerHTML).toBe(legacyBuildStoryElement(story, membersArg, opts).outerHTML);
        }
      }
    });
  }

  it('keeps Wall geometry, labels and the absence of board-only controls', () => {
    const story = { localId: 7, x: 10.4, y: 20.6, version: 3, todo: todo({ dueAt: past, archivedAt: '2020-06-16T00:00:00Z' } as Partial<Todo>) } as unknown as WallStory;
    const el = buildStoryElement(story, members, fullOpts);
    expect(el.className).toBe('wall-story card card--doing wall-story--archived');
    expect(el.style.width).toBe(`${WALL_STORY_WIDTH}px`);
    expect(el.style.left).toBe('10px');
    expect(el.style.top).toBe('21px');
    expect(el.getAttribute('role')).toBe('button');
    expect(el.getAttribute('aria-label')).toBe('Open Story #7');
    expect(el.querySelector('.card__drag-handle, .card__overdue-warning')).toBeNull();
    // Archived Wall stories still show an overdue due badge (pre-existing behavior).
    expect(el.querySelector('.card__due--overdue')).not.toBeNull();
  });
});

// ---- Public: DOM-equivalent ---------------------------------------------------

type NormalizedNode = string | { tag: string; attrs: Record<string, string>; children: NormalizedNode[] };

function normalizeStyle(style: string): string {
  return style.split(';').map((d) => d.trim()).filter(Boolean)
    .map((d) => { const i = d.indexOf(':'); return `${d.slice(0, i).trim()}:${d.slice(i + 1).trim()}`; })
    .join(';');
}

function normalizeNode(node: Node): NormalizedNode | null {
  if (node.nodeType === Node.TEXT_NODE) {
    const text = (node.textContent ?? '').trim();
    return text ? text : null;
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return null;
  const el = node as Element;
  const attrs: Record<string, string> = {};
  for (const attr of Array.from(el.attributes)) attrs[attr.name] = attr.name === 'style' ? normalizeStyle(attr.value) : attr.value;
  return {
    tag: el.tagName.toLowerCase(),
    attrs,
    children: Array.from(el.childNodes).map(normalizeNode).filter((n): n is NormalizedNode => n !== null),
  };
}

function normalizedDom(html: string): NormalizedNode[] {
  const host = document.createElement('div');
  host.innerHTML = html;
  return Array.from(host.childNodes).map(normalizeNode).filter((n): n is NormalizedNode => n !== null);
}

describe('public story card characterization', () => {
  const priorities: Record<string, PublicPriority> = {
    high: { key: 'high', name: 'High', color: '#EF4444', position: 0 },
    nocolor: { key: 'nocolor', name: 'Plain', color: null, position: 1 },
    evil: { key: 'evil', name: hostile, color: 'javascript:alert(1)', position: 2 },
  };
  const column = (color: string | null): PublicWorkflowColumn => ({ key: 'doing', name: 'Doing', color, isDone: false, position: 0 } as PublicWorkflowColumn);
  const publicTodo = (over: Partial<PublicTodo> = {}): PublicTodo => ({
    localId: 7, title: 'Ship it', body: 'hidden body', columnKey: 'doing', estimationPoints: null, priorityKey: null, sprintNumber: null, tags: [], ...over,
  });
  const cases: Array<[string, PublicTodo]> = [
    ['minimal', publicTodo()],
    ['hostile title', publicTodo({ title: hostile })],
    ['empty title', publicTodo({ title: '' })],
    ['tags', publicTodo({ tags: [{ name: 'ui', color: '#00ff00' }, { name: 'none', color: null }, { name: hostile, color: 'url(evil)' }] })],
    ['priority', publicTodo({ priorityKey: 'high' })],
    ['priority without color', publicTodo({ priorityKey: 'nocolor' })],
    ['hostile priority', publicTodo({ priorityKey: 'evil' })],
    ['unknown priority', publicTodo({ priorityKey: 'missing' })],
    ['zero points', publicTodo({ estimationPoints: 0 })],
    ['points', publicTodo({ estimationPoints: 5 })],
    ['everything', publicTodo({ title: hostile, tags: [{ name: 'ui', color: '#00ff00' }], priorityKey: 'high', estimationPoints: 0, sprintNumber: 2 })],
  ];
  for (const [name, value] of cases) {
    it(`renderPublicTodoCard is DOM-equivalent to legacy output: ${name}`, () => {
      for (const showPoints of [true, false]) {
        for (const color of ['#336699', null, 'javascript:alert(1)']) {
          expect(normalizedDom(renderPublicTodoCard(value, column(color), priorities, showPoints)))
            .toEqual(normalizedDom(legacyRenderPublicTodoCard(value, column(color), priorities, showPoints)));
        }
      }
    });
  }

  it('keeps the public wrapper, theme tag text and public-only accessibility', () => {
    const host = document.createElement('div');
    host.innerHTML = renderPublicTodoCard(publicTodo({ tags: [{ name: 'ui', color: '#00ff00' }], priorityKey: 'high', estimationPoints: 0 }), column('#336699'), priorities, true);
    const card = host.querySelector('button.card.card--public') as HTMLButtonElement;
    expect(card.getAttribute('type')).toBe('button');
    expect(card.getAttribute('data-public-local-id')).toBe('7');
    expect(card.getAttribute('style')).toBe('border-color:#336699');
    expect(card.querySelector('.tag')?.getAttribute('style')).toContain('color: var(--text)');
    expect(card.querySelector('.card__priority')?.getAttribute('aria-label')).toBe('Priority: High');
    expect(card.querySelector('.card__priority')?.hasAttribute('title')).toBe(false);
    expect(card.querySelector('.card__points')?.textContent).toBe('0');
    expect(card.querySelector('.card__points')?.hasAttribute('title')).toBe(false);
    expect(card.querySelector('[data-todo-id], [data-todo-local-id], [data-assignee-user-id], [id], .card__drag-handle, .todo-avatar, .card__due, .card__top-actions')).toBeNull();
    expect(card.classList.contains('card--selected')).toBe(false);
  });
});
