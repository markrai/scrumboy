import type { Board, BoardAccess } from '../types.js';
import { isTemporaryBoard } from '../utils.js';

/** The only public access value. It carries no role, project ID, or identity. */
export const PUBLIC_BOARD_ACCESS: BoardAccess = Object.freeze({ kind: 'public', readOnly: true } as const);

const MEMBER_BOARD_ACCESS: BoardAccess = Object.freeze({ kind: 'member' } as const);
const TEMPORARY_BOARD_ACCESS: BoardAccess = Object.freeze({ kind: 'temporary' } as const);

/**
 * Access for a board returned by the existing member/temporary board route.
 * The route already enforced membership or temporary-board rules; this only
 * classifies which of those two existing experiences the payload represents.
 */
export function accessForMemberRouteBoard(board: Board): BoardAccess {
  return isTemporaryBoard(board) ? TEMPORARY_BOARD_ACCESS : MEMBER_BOARD_ACCESS;
}

export function isPublicBoardAccess(access: BoardAccess | null | undefined): access is Extract<BoardAccess, { kind: 'public' }> {
  return access?.kind === 'public';
}

/**
 * Which board-specific realtime transport the rendered board may use. Member
 * and temporary boards keep the existing path; public boards use only the
 * isolated public stream. Login state never selects the audience.
 */
export type BoardRealtimeAudience = 'member-or-temporary' | 'public' | 'none';

export function boardRealtimeAudience(access: BoardAccess | null | undefined): BoardRealtimeAudience {
  switch (access?.kind) {
    case 'member':
    case 'temporary':
      return 'member-or-temporary';
    case 'public':
      return 'public';
    default:
      return 'none';
  }
}
