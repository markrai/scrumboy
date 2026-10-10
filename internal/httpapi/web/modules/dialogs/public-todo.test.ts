// @vitest-environment jsdom
// DOMPurify does not support happy-dom; jsdom exercises the real sanitizer.
import createDOMPurify from 'dompurify';
import MarkdownIt from 'markdown-it';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../i18n/index.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../i18n/index.js')>()),
  t: (key: string) => key,
}));

import { renderPublicTodoContent, type PublicTodoDialogContext } from './public-todo.js';
import type { PublicTodo } from '../public-board-api.js';

function context(overrides: Partial<PublicTodoDialogContext> = {}): PublicTodoDialogContext {
  return {
    slug: 'ignite',
    priorities: { high: { key: 'high', name: 'High', color: '#ff0000', position: 0 } },
    sprints: [{ number: 2, name: 'Sprint Two', state: 'ACTIVE' }],
    showPoints: true,
    markdownEnabled: true,
    mermaidEnabled: false,
    onNavigateToStory: vi.fn(),
    onClosedByUser: vi.fn(),
    ...overrides,
  };
}

const todo: PublicTodo = {
  localId: 7,
  title: 'T',
  body: [
    '**bold** <img src=x onerror="alert(1)"> <script>alert(2)</script>',
    '',
    '[safe](https://example.com) [bad](javascript:alert(3)) [data](data:text/html,<b>x</b>) [vbscript](vbscript:msgbox(4))',
    '',
    '<a href="https://evil.example" onclick="alert(4)">raw</a>',
  ].join('\n'),
  columnKey: 'backlog',
  estimationPoints: 5,
  priorityKey: 'high',
  sprintNumber: 2,
  tags: [{ name: '<b>tag</b>', color: 'expression(alert(1))' }],
};

describe('public story detail content', () => {
  beforeEach(() => {
    (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
    (window as any).DOMPurify = createDOMPurify(window);
    document.body.innerHTML = '';
  });

  it('renders Markdown through the shared sanitizer with no executable content', async () => {
    const container = document.createElement('div');
    await renderPublicTodoContent(container, todo, [], context());
    const notes = container.querySelector('.public-todo__notes')!;
    expect(notes.querySelector('strong')?.textContent).toBe('bold');
    expect(container.querySelector('img, script, iframe, [onerror], [onclick]')).toBeNull();
    const hrefs = Array.from(container.querySelectorAll('a')).map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(['https://example.com']);
    const external = container.querySelector('a[href="https://example.com"]')!;
    expect(external.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('falls back to inert plain text when Markdown is disabled', async () => {
    const container = document.createElement('div');
    await renderPublicTodoContent(container, todo, [], context({ markdownEnabled: false }));
    const notes = container.querySelector('.public-todo__notes')!;
    expect(notes.classList.contains('public-todo__notes--plain')).toBe(true);
    expect(notes.textContent).toBe(todo.body);
    expect(notes.children).toHaveLength(0);
  });

  it('shows only approved fields, escaped tags, and same-board links', async () => {
    const onNavigateToStory = vi.fn();
    const container = document.createElement('div');
    await renderPublicTodoContent(container, todo, [{ direction: 'inbound', localId: 3, title: '<i>Three</i>' }], context({ onNavigateToStory }));
    expect(container.querySelector('.tag')?.textContent).toBe('<b>tag</b>');
    expect((container.querySelector('.tag') as HTMLElement).getAttribute('style')).toBeNull();
    expect(container.querySelector('.card__priority')?.textContent).toBe('High');
    expect(container.querySelector('.card__points')?.textContent).toBe('5');
    expect(container.textContent).toContain('Sprint Two (2)');
    const link = container.querySelector<HTMLButtonElement>('[data-public-link-open="3"]')!;
    expect(link.textContent).toBe('#3 <i>Three</i>');
    expect(link.closest('a')).toBeNull();
    link.click();
    expect(onNavigateToStory).toHaveBeenCalledWith(3);
    for (const forbidden of ['assignee', 'creator', 'created', 'updated', 'archived']) {
      expect(container.innerHTML.toLowerCase()).not.toContain(forbidden);
    }
    expect(container.querySelector('input, textarea, select, form, button:not([data-public-link-open])')).toBeNull();
  });
});
