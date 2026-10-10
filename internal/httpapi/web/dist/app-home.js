/**
 * Single route-mode policy for the workspace entry.
 *
 * In Full Mode with the landing override, `/` is the server-rendered
 * marketing landing and `/_app` is the workspace. Otherwise `/` remains the
 * workspace exactly as before. Board URLs (`/{slug}`, `/{slug}/t/{localId}`)
 * never depend on this decision.
 */
import { getAuthStatusAvailable, getLandingPageEnabled } from './state/selectors.js';
export const WORKSPACE_PATH = '/_app';
/** True only when the server reported the effective Full Mode landing override. */
export function isLandingModeActive() {
    return getAuthStatusAvailable() && getLandingPageEnabled();
}
/** Path of the authenticated workspace home (projects list or sign-in). */
export function appHomePath() {
    return isLandingModeActive() ? WORKSPACE_PATH : '/';
}
/** Marketing root when the landing override is active, otherwise the workspace. */
export function siteHomePath() {
    return '/';
}
export function isWorkspacePath(pathname) {
    return pathname === WORKSPACE_PATH || pathname === `${WORKSPACE_PATH}/`;
}
/** Client path to the existing sign-in UI that returns to `next` afterwards. */
export function signInPath(next) {
    return `/auth/login?next=${encodeURIComponent(next)}`;
}
