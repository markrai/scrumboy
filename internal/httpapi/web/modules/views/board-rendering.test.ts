// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import type { Board } from '../types.js';
import { buildTopbarHtml } from './board-rendering.js';
import { buildTopbarHtml as buildTopbarHtmlDist } from '../../dist/views/board-rendering.js';

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
});
