// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import type { Board } from '../types.js';
import { buildTopbarHtml } from './board-rendering.js';

function board(expires?: string): Board {
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 1,
      ...(expires ? { expiresAt: expires } : {}),
    } as any,
    tags: [],
    columns: { backlog: [] },
  };
}

function render(args: {
  expires?: string;
  role: string | null;
  isAnonymousTempBoard: boolean;
  wallEnabled?: boolean;
  isMobile?: boolean;
}): string {
  return buildTopbarHtml({
    board: board(args.expires),
    minimalTopbar: false,
    search: '',
    searchPlaceholder: 'Search',
    isMobile: !!args.isMobile,
    isAnonymousTempBoard: args.isAnonymousTempBoard,
    currentUserProjectRole: args.role,
    showVoiceCommands: false,
    user: null,
    backLabel: 'Projects',
    wallEnabled: args.wallEnabled,
  });
}

describe('wall topbar gating', () => {
  it('renders the wall button on durable boards for contributors and maintainers', () => {
    for (const role of ['maintainer', 'contributor']) {
      const html = render({ role, isAnonymousTempBoard: false, wallEnabled: true });
      expect(html).toContain('id="wallBtn"');
    }
  });

  it('hides the wall button on anonymous/temp boards even when the flag is on', () => {
    const html = render({ role: 'maintainer', isAnonymousTempBoard: true, wallEnabled: true, expires: new Date(Date.now() + 60_000).toISOString() });
    expect(html).not.toContain('id="wallBtn"');
  });

  it('hides the wall button on durable boards when expiresAt is present (authenticated temp)', () => {
    const html = render({ role: 'maintainer', isAnonymousTempBoard: false, wallEnabled: true, expires: new Date(Date.now() + 60_000).toISOString() });
    expect(html).not.toContain('id="wallBtn"');
  });

  it('hides the wall button when the feature flag is off', () => {
    const html = render({ role: 'maintainer', isAnonymousTempBoard: false, wallEnabled: false });
    expect(html).not.toContain('id="wallBtn"');
  });

  it('hides the wall button for viewers', () => {
    const html = render({ role: 'viewer', isAnonymousTempBoard: false, wallEnabled: true });
    expect(html).not.toContain('id="wallBtn"');
  });
});
