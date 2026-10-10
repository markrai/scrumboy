// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import type { Board } from '../types.js';
import {
  accessForMemberRouteBoard,
  boardRealtimeAudience,
  isPublicBoardAccess,
  PUBLIC_BOARD_ACCESS,
} from './board-access.js';
import { getBoardAccess } from './selectors.js';
import { resetUserScopedState, setBoardAccess } from './mutations.js';

function boardWith(project: Record<string, unknown>): Board {
  return { project, columns: {}, tags: [] } as unknown as Board;
}

describe('BoardAccess', () => {
  it('classifies member-route payloads as member or temporary only', () => {
    expect(accessForMemberRouteBoard(boardWith({ id: 1, expiresAt: null }))).toEqual({ kind: 'member' });
    expect(accessForMemberRouteBoard(boardWith({ id: 2, expiresAt: '2030-01-01T00:00:00Z' }))).toEqual({ kind: 'temporary' });
    // The member route can never produce public access.
    expect(isPublicBoardAccess(accessForMemberRouteBoard(boardWith({ id: 3 })))).toBe(false);
  });

  it('public access carries no role, project ID, or identity and is immutable', () => {
    expect(PUBLIC_BOARD_ACCESS).toEqual({ kind: 'public', readOnly: true });
    expect(Object.keys(PUBLIC_BOARD_ACCESS).sort()).toEqual(['kind', 'readOnly']);
    expect(Object.isFrozen(PUBLIC_BOARD_ACCESS)).toBe(true);
    expect(() => {
      (PUBLIC_BOARD_ACCESS as any).kind = 'member';
    }).toThrow();
  });

  it('selects realtime audience from access, never from login state', () => {
    expect(boardRealtimeAudience({ kind: 'member' })).toBe('member-or-temporary');
    expect(boardRealtimeAudience({ kind: 'temporary' })).toBe('member-or-temporary');
    expect(boardRealtimeAudience(PUBLIC_BOARD_ACCESS)).toBe('public');
    expect(boardRealtimeAudience(null)).toBe('none');
    expect(isPublicBoardAccess(null)).toBe(false);
  });

  it('resets with user-scoped state so a new identity re-resolves access', () => {
    setBoardAccess(PUBLIC_BOARD_ACCESS);
    expect(getBoardAccess()).toBe(PUBLIC_BOARD_ACCESS);
    resetUserScopedState();
    expect(getBoardAccess()).toBeNull();
  });
});
