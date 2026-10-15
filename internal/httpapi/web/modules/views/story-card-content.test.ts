// @vitest-environment happy-dom
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import enCatalog from '../i18n/locales/en.json';
import {
  renderMemberStoryCardContentHtml,
  renderStoryCardContentHtml,
  type StoryCardContentInput,
  type StoryCardContentOptions,
} from './story-card-content.js';
import { toPublicStoryCardInput, renderPublicTodoCard } from './public-board-rendering.js';
import type { PublicPriority, PublicTodo, PublicWorkflowColumn } from '../public-board-api.js';

const hostile = '"><img src=x onerror=alert(1)><script>bad()</script>';
const accent: StoryCardContentOptions = { tagTone: 'accent', tooltips: true };
const theme: StoryCardContentOptions = { tagTone: 'theme', tooltips: false };

function input(over: Partial<StoryCardContentInput> = {}): StoryCardContentInput {
  return { localId: 7, title: 'Ship it', tags: [], priority: null, points: null, due: null, ...over };
}

function mount(html: string): HTMLElement {
  const host = document.createElement('div');
  host.innerHTML = html;
  return host.querySelector('.card__content') as HTMLElement;
}

beforeAll(async () => {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('renderStoryCardContentHtml', () => {
  it('renders only the inner content with the local number and escaped title', () => {
    const root = mount(renderStoryCardContentHtml(input({ title: hostile }), accent));
    expect(root.querySelector('.card__title-row .card__id-inline')?.textContent).toBe('#7');
    expect(root.querySelector('.card__title')?.textContent).toBe(hostile);
    expect(root.querySelector('img, script')).toBeNull();
  });

  it('omits the tags/badges footer when there are no tags or badges, and renders empty titles', () => {
    const root = mount(renderStoryCardContentHtml(input({ title: '' }), accent));
    expect(root.querySelector('.card__tags')).toBeNull();
    expect(root.querySelector('.card__title')?.textContent).toBe('');
  });

  it('escapes tag and priority names and drops unsafe colors', () => {
    const root = mount(renderStoryCardContentHtml(input({
      tags: [{ name: hostile, color: 'url(evil)' }, { name: 'x', color: 'red;background:url(x)' }],
      priority: { name: hostile, color: 'javascript:alert(1)' },
    }), accent));
    const tags = Array.from(root.querySelectorAll('.card__tags-list .tag'));
    expect(tags.map((tag) => tag.textContent)).toEqual([hostile, 'x']);
    expect(tags.every((tag) => !tag.hasAttribute('style'))).toBe(true);
    const priority = root.querySelector('.card__priority')!;
    expect(priority.textContent).toBe(hostile);
    expect(priority.hasAttribute('style')).toBe(false);
    expect(priority.getAttribute('aria-label')).toBe(`Priority: ${hostile}`);
    expect(root.querySelector('img, script')).toBeNull();
  });

  it('colors member tag text with the accent and public tag text with the theme foreground', () => {
    const tags = [{ name: 'ui', color: '#00ff00' }];
    const member = renderStoryCardContentHtml(input({ tags }), accent);
    const pub = renderStoryCardContentHtml(input({ tags }), theme);
    expect(member).toContain('style="border-color: #00ff00; background: #00ff0020; color: #00ff00;"');
    expect(pub).toContain('style="border-color: #00ff00; background: #00ff0020; color: var(--text);"');
  });

  it('keeps tag order as given', () => {
    const root = mount(renderStoryCardContentHtml(input({ tags: ['b', 'a', 'c'].map((name) => ({ name, color: null })) }), theme));
    expect(Array.from(root.querySelectorAll('.tag')).map((tag) => tag.textContent)).toEqual(['b', 'a', 'c']);
  });

  it('renders zero estimation points and hides points only when null', () => {
    const zero = mount(renderStoryCardContentHtml(input({ points: 0 }), theme));
    const points = zero.querySelector('.card__points')!;
    expect(points.textContent).toBe('0');
    expect(points.getAttribute('aria-label')).toBe('Estimation Points');
    expect(points.getAttribute('data-i18n-aria-label')).toBe('todo.fields.estimationPoints');
    expect(mount(renderStoryCardContentHtml(input({ points: null }), theme)).querySelector('.card__points')).toBeNull();
  });

  it('adds field tooltips to priority and points only when requested', () => {
    const value = input({ priority: { name: 'High', color: '#EF4444' }, points: 3 });
    const withTips = mount(renderStoryCardContentHtml(value, accent));
    expect(withTips.querySelector('.card__priority')?.getAttribute('title')).toBe('How urgent this ticket is, relative to others on this board.');
    expect(withTips.querySelector('.card__points')?.getAttribute('title')).toContain('Relative effort');
    const without = mount(renderStoryCardContentHtml(value, theme));
    expect(without.querySelector('.card__priority')?.hasAttribute('title')).toBe(false);
    expect(without.querySelector('.card__points')?.hasAttribute('title')).toBe(false);
    expect(without.querySelector('.card__priority')?.getAttribute('style')).toBe('border-color:#EF4444; background:#EF444420; color:#EF4444;');
  });

  it('renders due dates, overdue only outside done columns, and nothing for invalid dates', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2030-06-15T12:00:00Z'));
    const due = (dueAt: string, inDoneColumn: boolean) => mount(renderStoryCardContentHtml(input({ due: { dueAt, inDoneColumn } }), accent));
    const upcoming = due('2030-07-01T12:00:00Z', false).querySelector('.card__due')!;
    expect(upcoming.className).toBe('card__due');
    expect(upcoming.getAttribute('aria-label')).toMatch(/^Due /);
    expect(upcoming.getAttribute('title')).toBe(upcoming.getAttribute('aria-label'));
    expect(due('2030-06-01T12:00:00Z', false).querySelector('.card__due')?.className).toBe('card__due card__due--overdue');
    expect(due('2030-06-01T12:00:00Z', true).querySelector('.card__due')?.className).toBe('card__due');
    expect(due('not-a-date', false).querySelector('.card__due, .card__tags')).toBeNull();
  });

  it('keeps footer order: due, priority, points, then the member badge', () => {
    const html = renderMemberStoryCardContentHtml(input({
      due: { dueAt: '2099-01-01T12:00:00Z', inDoneColumn: false },
      priority: { name: 'High', color: null },
      points: 1,
    }), accent, '<div class="todo-avatar"></div>');
    const badges = Array.from(mount(html).querySelector('.card__badges')!.children).map((el) => el.className);
    expect(badges).toEqual(['card__due', 'card__priority', 'card__points', 'todo-avatar']);
  });

  it('emits no identity or interaction attributes', () => {
    const html = renderStoryCardContentHtml(input({
      tags: [{ name: 'ui', color: '#00ff00' }], priority: { name: 'High', color: '#EF4444' }, points: 2,
      due: { dueAt: '2099-01-01T12:00:00Z', inDoneColumn: false },
    }), accent);
    expect(html).not.toMatch(/\s(id|data-todo-id|data-todo-local-id|data-assignee-user-id|data-public-local-id|draggable|href|on\w+)=/);
    expect(html).not.toMatch(/card__drag-handle|card--selected|todo-avatar|card__overdue-warning/);
  });
});

describe('public story card adapter', () => {
  const priorities: Record<string, PublicPriority> = { high: { key: 'high', name: 'High', color: '#EF4444', position: 0 } };
  const todo: PublicTodo = {
    localId: 9, title: 'Public', body: 'Body text', columnKey: 'doing', estimationPoints: 0, priorityKey: 'high', sprintNumber: 4,
    tags: [{ name: 'ui', color: '#00ff00' }],
  };

  it('maps only public DTO fields and never passes due dates', () => {
    expect(toPublicStoryCardInput(todo, priorities, true)).toEqual({
      localId: 9, title: 'Public', tags: [{ name: 'ui', color: '#00ff00' }],
      priority: { name: 'High', color: '#EF4444' }, points: 0, due: null,
    });
    expect(toPublicStoryCardInput(todo, priorities, false).points).toBeNull();
    expect(toPublicStoryCardInput({ ...todo, priorityKey: 'missing' }, priorities, true).priority).toBeNull();
  });

  it('renders public cards without member identity, avatar, due, drag, or selection markup', () => {
    const column = { key: 'doing', name: 'Doing', color: '#336699', isDone: false, position: 0 } as PublicWorkflowColumn;
    const html = renderPublicTodoCard(todo, column, priorities, true);
    expect(html).not.toMatch(/data-todo-id|data-todo-local-id|data-assignee-user-id|id="todo_|card__drag-handle|todo-avatar|card__due|card--selected|card__top-actions/);
    expect(html).not.toContain('Body text');
    expect(html).toContain('data-public-local-id="9"');
  });
});

describe('story card responsive styles', () => {
  const css = readFileSync(resolve(process.cwd(), 'styles.css'), 'utf8');

  function mediaBlockContaining(selector: string): string {
    const at = css.indexOf(selector);
    const media = css.lastIndexOf('@media', at);
    return css.slice(media, css.indexOf('{', media)).trim();
  }

  it('keeps mobile-lane cards at their content height inside the scrolling list', () => {
    expect(css).toMatch(/\.board \.col--mobile-active \.col__list > \.card \{\s*flex-shrink: 0;\s*\}/);
    expect(mediaBlockContaining('.board .col--mobile-active .col__list > .card')).toBe('@media (max-width: 620px)');
  });

  it('wraps long tag names inside the pill rather than clipping or truncating them', () => {
    const rule = css.match(/\n\.board \.card__tags-list \.tag \{([^}]*)\}/)?.[1] ?? '';
    expect(rule).toContain('max-width: 100%');
    expect(rule).toContain('overflow-wrap: anywhere');
    expect(rule).not.toMatch(/text-overflow|white-space:\s*nowrap|overflow:\s*hidden/);
  });
});

describe('story card module boundaries', () => {
  function source(file: string): string {
    return readFileSync(resolve(process.cwd(), 'modules', file), 'utf8');
  }
  function importsOf(file: string): string[] {
    return Array.from(source(file).matchAll(/from\s+'([^']+)'/g)).map((m) => m[1]);
  }

  it('the shared renderer imports only presentation helpers', () => {
    expect(importsOf('views/story-card-content.ts').sort()).toEqual([
      '../dialogs/todo-due.js',
      '../field-tooltips.js',
      '../i18n/index.js',
      '../utils.js',
    ]);
  });

  it('only the authenticated board adapter uses the trusted member badge variant', () => {
    expect(source('views/board-rendering.ts')).toContain('renderMemberStoryCardContentHtml');
    for (const file of ['views/public-board-rendering.ts', 'views/public-board.ts', 'views/public-omni.ts', 'dialogs/wall-rendering.ts']) {
      expect(source(file), file).not.toContain('renderMemberStoryCardContentHtml');
    }
  });

  it('public rendering reaches card content only through the shared module', () => {
    const imports = importsOf('views/public-board-rendering.ts');
    expect(imports).toContain('./story-card-content.js');
    for (const forbidden of ['./board-rendering.js', './board.js', './board-filters.js', '../api.js', '../state/state.js', '../features/drag-drop.js']) {
      expect(imports).not.toContain(forbidden);
    }
  });
});
