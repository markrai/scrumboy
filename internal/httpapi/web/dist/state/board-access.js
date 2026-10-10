import { isTemporaryBoard } from '../utils.js';
/** The only public access value. It carries no role, project ID, or identity. */
export const PUBLIC_BOARD_ACCESS = Object.freeze({ kind: 'public', readOnly: true });
const MEMBER_BOARD_ACCESS = Object.freeze({ kind: 'member' });
const TEMPORARY_BOARD_ACCESS = Object.freeze({ kind: 'temporary' });
/**
 * Access for a board returned by the existing member/temporary board route.
 * The route already enforced membership or temporary-board rules; this only
 * classifies which of those two existing experiences the payload represents.
 */
export function accessForMemberRouteBoard(board) {
    return isTemporaryBoard(board) ? TEMPORARY_BOARD_ACCESS : MEMBER_BOARD_ACCESS;
}
export function isPublicBoardAccess(access) {
    return access?.kind === 'public';
}
export function boardRealtimeAudience(access) {
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
