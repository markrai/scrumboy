// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import { buildTopbarHtml, renderTodoCard } from './board-rendering.js';
import { buildTopbarHtml as buildTopbarHtmlDist } from '../../dist/views/board-rendering.js';
import enCatalog from '../i18n/locales/en.json';
import pseudoCatalog from '../i18n/locales/pseudo.json';

function board(): Board {
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 1,
    },
    tags: [],
    columns: { backlog: [] },
  };
}

function renderTopbar(showVoiceCommands: boolean): string {
  return buildTopbarHtml({
    board: board(),
    minimalTopbar: false,
    search: '',
    searchPlaceholder: 'Search',
    isMobile: false,
    isAnonymousTempBoard: false,
    currentUserProjectRole: 'maintainer',
    showVoiceCommands,
    user: null,
    backLabel: 'Projects',
  });
}

describe('board topbar rendering', () => {
  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
  });

  it('renders the voice command trigger only when explicitly enabled', () => {
    expect(renderTopbar(true)).toContain('topbar--voice-commands-on');
    expect(renderTopbar(true)).toContain('id="voiceCommandBtn"');
    expect(renderTopbar(true)).toContain('aria-label="VoiceFlow"');
    expect(renderTopbar(true)).toContain('<img');
    expect(renderTopbar(true)).toContain('src="/mic.svg"');
    expect(renderTopbar(true)).toContain('width="20"');
    expect(renderTopbar(true)).toContain('height="20"');
    expect(renderTopbar(false)).toContain('topbar--voice-commands-off');
    expect(renderTopbar(false)).not.toContain('id="voiceCommandBtn"');
  });

  it('uses the VoiceFlow title catalog key for trigger aria and title text', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({
      locale: 'pseudo',
      loadLocale: vi.fn(async (locale: 'en' | 'pseudo') => (locale === 'pseudo' ? pseudoCatalog : enCatalog)),
    });

    const html = renderTopbar(true);

    expect(html).toContain(`aria-label="${pseudoCatalog['voice.title']}"`);
    expect(html).toContain(`title="${pseudoCatalog['voice.title']}"`);
  });

  it('renders the mic button to the left of the search input (desktop)', () => {
    const html = renderTopbar(true);
    expect(html.indexOf('id="voiceCommandBtn"')).toBeGreaterThan(-1);
    expect(html.indexOf('id="searchInput"')).toBeGreaterThan(-1);
    expect(html.indexOf('id="voiceCommandBtn"')).toBeLessThan(html.indexOf('id="searchInput"'));

    const distHtml = buildTopbarHtmlDist({
      board: board(),
      minimalTopbar: false,
      search: '',
      searchPlaceholder: 'Search',
      isMobile: false,
      isAnonymousTempBoard: false,
      currentUserProjectRole: 'maintainer',
      showVoiceCommands: true,
      user: null,
      backLabel: 'Projects',
    });
    expect(distHtml.indexOf('id="voiceCommandBtn"')).toBeGreaterThan(-1);
    expect(distHtml.indexOf('id="searchInput"')).toBeGreaterThan(-1);
    expect(distHtml.indexOf('id="voiceCommandBtn"')).toBeLessThan(distHtml.indexOf('id="searchInput"'));
  });

  it('renders plain escaped titles on cards and never renders markdown from todo bodies', () => {
    const html = renderTodoCard({
      id: 7,
      localId: 12,
      title: '**Plain** <Title>',
      body: ['# Hidden body heading', '', '```mermaid', 'graph TD', 'A-->B', '```'].join('\n'),
      status: 'BACKLOG',
      tags: [],
    });

    expect(html).toContain('**Plain** &lt;Title&gt;');
    expect(html).not.toContain('<strong>Plain</strong>');
    expect(html).not.toContain('Hidden body heading');
    expect(html).not.toContain('<h1>');
    expect(html).not.toContain('graph TD');
    expect(html).not.toContain('A--&gt;B');
    expect(html).not.toContain('todo-mermaid');
  });

  it('always renders a drag handle, even under chronological sort where only cross-lane drag is allowed', () => {
    const todo = {
      id: 7,
      localId: 12,
      title: 'Reorder me',
      body: '',
      status: 'BACKLOG',
      tags: [],
    };

    const html = renderTodoCard(todo);
    expect(html).toContain('card__drag-handle');
    expect(html).toContain('aria-label="Drag card"');
    expect(html).toContain('data-i18n-aria-label="board.todo.dragCard"');
  });
});
