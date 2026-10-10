/**
 * Settings → Sharing: Maintainer-only publication management.
 *
 * Uses only GET/PATCH /api/board/{slug}/publication. The server (Phase 1
 * PublicationService) is authoritative for authorization, eligibility,
 * audit, and stream revocation; this UI never assumes a mutation succeeded
 * until the server confirms it and refetches status after uncertain failures.
 */

import { apiFetch } from '../api.js';
import { getAppRuntime } from '../platform/runtime.js';
import { escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { t } from '../i18n/index.js';

type PublicationStatus = { enabled: boolean; publishable: boolean };
type SharingView =
  | { kind: 'loading' }
  | { kind: 'ready'; status: PublicationStatus; pending: boolean }
  | { kind: 'load-failed' }
  | { kind: 'not-allowed' };

export const SHARING_ROOT_ATTR = 'data-sharing-root';

export function publicBoardUrl(slug: string): string {
  return `${getAppRuntime().publicLinkOrigin()}/${slug}`;
}

function i18n(key: string): string {
  return ` data-i18n-text="${escapeHTML(key)}"`;
}

export function renderSharingTabShell(): string {
  return `<div class="settings-sharing" ${SHARING_ROOT_ATTR}>${renderSharingView({ kind: 'loading' }, '')}</div>`;
}

export function renderSharingView(view: SharingView, slug: string): string {
  const heading = `<h3 class="settings-sharing__heading"${i18n('settings.sharing.heading')}>${escapeHTML(t('settings.sharing.heading'))}</h3>`;
  if (view.kind === 'loading') {
    return `${heading}<p class="muted" role="status"${i18n('settings.sharing.loading')}>${escapeHTML(t('settings.sharing.loading'))}</p>`;
  }
  if (view.kind === 'not-allowed') {
    return `${heading}<p class="muted" role="status"${i18n('settings.sharing.notAllowed')}>${escapeHTML(t('settings.sharing.notAllowed'))}</p>`;
  }
  if (view.kind === 'load-failed') {
    return `${heading}<p class="muted" role="alert"${i18n('settings.sharing.loadFailed')}>${escapeHTML(t('settings.sharing.loadFailed'))}</p>
      <button class="btn btn--ghost" type="button" data-sharing-retry${i18n('common.retry')}>${escapeHTML(t('common.retry'))}</button>`;
  }
  const { status, pending } = view;
  const statusKey = status.enabled ? 'settings.sharing.statusPublic' : 'settings.sharing.statusPrivate';
  const descriptionKey = status.enabled ? 'settings.sharing.descriptionPublic' : 'settings.sharing.descriptionPrivate';
  const disabled = pending ? ' disabled aria-disabled="true"' : '';
  const url = publicBoardUrl(slug);
  const link = status.enabled
    ? `<div class="field settings-sharing__link">
        <label class="field__label" for="sharingPublicUrl"${i18n('settings.sharing.publicLink')}>${escapeHTML(t('settings.sharing.publicLink'))}</label>
        <div class="settings-sharing__link-row">
          <input class="input" id="sharingPublicUrl" type="text" readonly value="${escapeHTML(url)}" />
          <button class="btn btn--ghost" type="button" data-sharing-copy${i18n('settings.sharing.copyLink')}>${escapeHTML(t('settings.sharing.copyLink'))}</button>
        </div>
      </div>`
    : '';
  const reserved = !status.enabled && !status.publishable
    ? `<p class="muted" role="note"${i18n('settings.sharing.reservedSlug')}>${escapeHTML(t('settings.sharing.reservedSlug'))}</p>`
    : '';
  const action = status.enabled
    ? `<button class="btn btn--danger" type="button" data-sharing-toggle="unpublish"${disabled}${i18n('settings.sharing.unpublish')}>${escapeHTML(t('settings.sharing.unpublish'))}</button>`
    : `<button class="btn btn--success" type="button" data-sharing-toggle="publish"${status.publishable ? disabled : ' disabled aria-disabled="true"'}${i18n('settings.sharing.publish')}>${escapeHTML(t('settings.sharing.publish'))}</button>`;
  return `${heading}
    <p class="settings-sharing__status" role="status" aria-live="polite" data-sharing-state="${status.enabled ? 'public' : 'private'}">
      <span class="settings-sharing__badge settings-sharing__badge--${status.enabled ? 'public' : 'private'}"${i18n(statusKey)}>${escapeHTML(t(statusKey))}</span>
      <span class="muted"${i18n(descriptionKey)}>${escapeHTML(t(descriptionKey))}</span>
    </p>
    ${link}
    ${reserved}
    <div class="settings-sharing__actions"${pending ? ' aria-busy="true"' : ''}>${action}</div>`;
}

function errorStatus(err: unknown): number | undefined {
  return (err as { status?: number } | null)?.status;
}

function errorReason(err: unknown): string | undefined {
  return (err as { data?: { error?: { details?: { reason?: string } } } } | null)?.data?.error?.details?.reason;
}

/** Binds the Sharing tab for one settings render; the signal ends it on re-render. */
export function bindSharingTab(options: { slug: string; signal: AbortSignal }): void {
  const root = document.querySelector<HTMLElement>(`[${SHARING_ROOT_ATTR}]`);
  if (!root) return;
  const { slug, signal } = options;
  const endpoint = `/api/board/${encodeURIComponent(slug)}/publication`;
  let view: SharingView = { kind: 'loading' };
  let generation = 0;

  const isLive = () => !signal.aborted && root.isConnected;
  const render = () => {
    if (isLive()) root.innerHTML = renderSharingView(view, slug);
  };

  const load = async (): Promise<void> => {
    const myGeneration = ++generation;
    try {
      const status = await apiFetch<PublicationStatus>(endpoint, { signal } as RequestInit);
      if (!isLive() || myGeneration !== generation) return;
      view = { kind: 'ready', status: { enabled: !!status?.enabled, publishable: !!status?.publishable }, pending: false };
    } catch (err) {
      if (!isLive() || myGeneration !== generation) return;
      const code = errorStatus(err);
      view = code === 403 || code === 404 ? { kind: 'not-allowed' } : { kind: 'load-failed' };
    }
    render();
  };

  const toggle = async (enable: boolean): Promise<void> => {
    if (view.kind !== 'ready' || view.pending) return;
    const confirmed = await showConfirmDialog(
      t(enable ? 'settings.sharing.confirmPublishMessage' : 'settings.sharing.confirmUnpublishMessage'),
      t(enable ? 'settings.sharing.confirmPublishTitle' : 'settings.sharing.confirmUnpublishTitle'),
      t(enable ? 'settings.sharing.confirmPublishAction' : 'settings.sharing.unpublish'),
      enable ? 'success' : 'danger',
    );
    if (!confirmed || !isLive() || view.kind !== 'ready' || view.pending) return;
    view = { ...view, pending: true };
    render();
    const myGeneration = ++generation;
    try {
      const result = await apiFetch<{ enabled: boolean; changed: boolean }>(endpoint, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: enable }),
        signal,
      } as RequestInit);
      if (!isLive() || myGeneration !== generation) return;
      view = { kind: 'ready', status: { enabled: !!result?.enabled, publishable: true }, pending: false };
      render();
      showToast(t(result?.enabled ? 'settings.sharing.published' : 'settings.sharing.unpublished'));
    } catch (err) {
      if (!isLive() || myGeneration !== generation) return;
      const code = errorStatus(err);
      if (code === 403 || code === 404) {
        view = { kind: 'not-allowed' };
        render();
        return;
      }
      if (code === 400 && errorReason(err) === 'publication_slug_reserved') {
        view = { kind: 'ready', status: { enabled: false, publishable: false }, pending: false };
        render();
        showToast(t('settings.sharing.reservedSlug'));
        return;
      }
      // Outcome uncertain (network or server error): reconcile with the server.
      showToast(t('settings.sharing.failed'));
      view = { kind: 'loading' };
      render();
      await load();
    }
  };

  const copy = async (): Promise<void> => {
    try {
      await navigator.clipboard.writeText(publicBoardUrl(slug));
      showToast(t('settings.sharing.copied'));
    } catch {
      const input = root.querySelector<HTMLInputElement>('#sharingPublicUrl');
      input?.focus();
      input?.select();
      showToast(t('settings.sharing.copyFailed'));
    }
  };

  root.addEventListener('click', (event) => {
    const target = event.target as HTMLElement;
    const toggleBtn = target.closest<HTMLButtonElement>('[data-sharing-toggle]');
    if (toggleBtn && !toggleBtn.disabled) {
      void toggle(toggleBtn.getAttribute('data-sharing-toggle') === 'publish');
      return;
    }
    if (target.closest('[data-sharing-copy]')) {
      void copy();
      return;
    }
    if (target.closest('[data-sharing-retry]')) {
      view = { kind: 'loading' };
      render();
      void load();
    }
  }, { signal });

  void load();
}
