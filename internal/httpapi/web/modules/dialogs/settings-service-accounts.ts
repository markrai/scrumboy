import { apiFetch } from '../api.js';
import { attachDialogClose, bindDialogLocale, escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { apiErrorMessageOrRaw, formatDate, hydrateI18n, t } from '../i18n/index.js';

/**
 * Admin UI for service accounts and their tokens (Settings > Users).
 * Thin client over /api/admin/service-accounts and /api/admin/service-tokens, which wrap the
 * same store methods as the admin_*Service* MCP tools. The server enforces the matrix
 * (create/list: Owner or Admin; delete and mint: Owner; list/revoke tokens: Owner or Admin);
 * the UI only hides actions the current role cannot perform.
 */

type ServiceAccount = { userId: number; stableUserId: string; name: string; createdAt: string };
type ServiceToken = {
  tokenId: number;
  userId: number;
  name?: string | null;
  createdAt: string;
  lastUsedAt?: string | null;
  revokedAt?: string | null;
};

type BindOptions = {
  signal: AbortSignal;
  rerender: () => Promise<void>;
  isOwner: boolean;
};

type MintResponse = {
  tokenId?: number;
  token?: unknown;
};

const DATE_OPTS: Intl.DateTimeFormatOptions = { month: "short", day: "numeric", year: "numeric" };

/** Hard bound on list walks so a hostile/nextCursor loop cannot grow without limit. */
export const MAX_LIST_PAGES = 100;

/**
 * Bumped on Settings teardown, logout and account switching so in-flight mint/create/delete
 * completions cannot reopen dialogs, show secrets, revoke under another session, or rerender.
 */
let serviceAccountsLifecycleGeneration = 0;

/** Closers for open SA management / plaintext dialogs; invoked on lifecycle invalidation. */
const openServiceAccountDialogClosers = new Set<() => void>();

function registerServiceAccountDialogCloser(close: () => void): () => void {
  openServiceAccountDialogClosers.add(close);
  return () => {
    openServiceAccountDialogClosers.delete(close);
  };
}

/**
 * Ends the current service-account UI lifecycle: closes open token/plaintext dialogs
 * (clearing any displayed secret) and invalidates in-flight completion handlers.
 * Does not issue revoke/cleanup mutations — those must not run under a different session.
 */
export function invalidateServiceAccountsLifecycle(): void {
  serviceAccountsLifecycleGeneration++;
  const closers = [...openServiceAccountDialogClosers];
  openServiceAccountDialogClosers.clear();
  for (const close of closers) {
    try {
      close();
    } catch {
      // Best-effort UI cleanup only.
    }
  }
}

/** Test helper: current lifecycle generation. */
export function getServiceAccountsLifecycleGeneration(): number {
  return serviceAccountsLifecycleGeneration;
}

function lifecycleStillValid(generation: number): boolean {
  return generation === serviceAccountsLifecycleGeneration;
}

/** Follows nextCursor to the end so no account or token is hidden behind a page boundary. */
export async function fetchAllPages<T>(path: string): Promise<T[]> {
  const out: T[] = [];
  let cursor = "";
  const seen = new Set<string>();
  for (let page = 0; page < MAX_LIST_PAGES; page++) {
    if (cursor) {
      if (seen.has(cursor)) {
        throw new Error("service-account list returned a repeated cursor");
      }
      seen.add(cursor);
    }
    const sep = path.includes("?") ? "&" : "?";
    const res = await apiFetch<{ items: T[]; nextCursor?: string | null } | null>(
      `${path}${sep}limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
    );
    out.push(...(res?.items ?? []));
    if (!res?.nextCursor) {
      return out;
    }
    cursor = res.nextCursor;
  }
  throw new Error(
    "service-account list incomplete: page limit reached while more results remain",
  );
}

export async function renderServiceAccountsSectionHTML(isOwner: boolean): Promise<string> {
  let body: string;
  try {
    const items = await fetchAllPages<ServiceAccount>("/api/admin/service-accounts");
    body = items.length === 0
      ? `<div class="muted" data-i18n-text="settings.serviceAccounts.empty">No service accounts yet.</div>`
      : `
        <table class="users-table">
          <thead>
            <tr>
              <th data-i18n-text="settings.serviceAccounts.table.name">Name</th>
              <th data-i18n-text="settings.serviceAccounts.table.created">Created</th>
              <th data-i18n-text="settings.users.table.actions">Actions</th>
            </tr>
          </thead>
          <tbody>
            ${items.map((a) => `
              <tr>
                <td>${escapeHTML(a.name)}</td>
                <td>${escapeHTML(formatDate(a.createdAt, DATE_OPTS))}</td>
                <td>
                  <div class="users-table__actions">
                    <button class="btn btn--ghost btn--small" data-action="sa-tokens" data-sa-id="${a.userId}" data-sa-name="${escapeHTML(a.name)}" data-i18n-text="settings.serviceAccounts.actions.tokens">Tokens</button>
                    ${isOwner ? `<button class="btn btn--danger btn--small" data-action="sa-delete" data-sa-id="${a.userId}" data-sa-name="${escapeHTML(a.name)}" data-i18n-text="settings.serviceAccounts.actions.delete">Delete</button>` : ""}
                  </div>
                </td>
              </tr>`).join("")}
          </tbody>
        </table>`;
  } catch (err: any) {
    body = `<div class="muted" role="alert">${escapeHTML(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.loadFailed" }))}</div>`;
  }

  return `
    <div class="settings-section" style="margin-top: 24px;">
      <div class="settings-section__title" data-i18n-text="settings.serviceAccounts.title">Service accounts</div>
      <div class="settings-section__description muted" data-i18n-text="settings.serviceAccounts.description">Non-interactive identities for integrations. A service account can't sign in, holds no system role, and reaches only the projects it is added to as a member.</div>
      ${body}
      <form id="createServiceAccountForm" class="api-tokens-create">
        <label class="field api-tokens-create__name">
          <div class="field__label" data-i18n-text="settings.serviceAccounts.create.nameLabel">Name</div>
          <input type="text" id="createServiceAccountName" class="input" data-i18n-placeholder="settings.serviceAccounts.create.namePlaceholder" placeholder="e.g. plans-bot" maxlength="200" required />
        </label>
        <button type="submit" class="btn" id="createServiceAccountSubmit" data-i18n-text="settings.serviceAccounts.create.submit">Create service account</button>
      </form>
    </div>
  `;
}

function clearPlaintextTokenInput(dialog: HTMLElement): void {
  const input = dialog.querySelector<HTMLInputElement>("#saTokenCreatedDisplay");
  if (input) {
    input.value = "";
    input.removeAttribute("value");
  }
}

/** Shows a freshly minted token once, with a copy action. Dialog cleanup removes secret-bearing DOM. */
function showServiceTokenCreatedDialog(token: string): void {
  const dialog = document.createElement("dialog");
  dialog.className = "dialog";
  dialog.dataset.saTokenCreatedDialog = "1";
  dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="settings.serviceAccounts.tokenCreated.title">Service token created</div>
        <button class="btn btn--ghost" type="button" id="saTokenCreatedClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>
      <p class="muted" role="alert" data-i18n-text="settings.serviceAccounts.tokenCreated.warning">Copy this token now — it won't be shown again.</p>
      <div class="field" style="margin: 12px 0;">
        <input type="text" id="saTokenCreatedDisplay" class="input" readonly value="${escapeHTML(token)}" style="font-size: 12px;" />
      </div>
      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn" id="saTokenCreatedCopy" data-i18n-text="settings.serviceAccounts.tokenCreated.copy">Copy</button>
        <button type="button" class="btn" id="saTokenCreatedDone" data-i18n-text="settings.serviceAccounts.tokenCreated.done">Done</button>
      </div>
    </div>
  `;
  document.body.appendChild(dialog);
  (dialog as HTMLDialogElement).showModal();
  const releaseLocale = bindDialogLocale(dialog);

  let unregister = () => {};
  const close = attachDialogClose(dialog, releaseLocale, () => {
    clearPlaintextTokenInput(dialog);
    unregister();
  });
  unregister = registerServiceAccountDialogCloser(close);
  dialog.querySelector("#saTokenCreatedClose")?.addEventListener("click", close);
  dialog.querySelector("#saTokenCreatedDone")?.addEventListener("click", close);
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) close();
  });
  const input = dialog.querySelector<HTMLInputElement>("#saTokenCreatedDisplay");
  dialog.querySelector("#saTokenCreatedCopy")?.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(input?.value ?? "");
      showToast(t("settings.serviceAccounts.tokenCreated.copied"));
    } catch {
      input?.select();
      showToast(t("settings.serviceAccounts.tokenCreated.copyManual"));
    }
  });
}

/**
 * Best-effort revoke when a mint 2xx response lacked a usable secret, or when the
 * mint dialog was dismissed in the same authenticated lifecycle before the secret
 * could be shown. Failures are swallowed — the caller already warns when needed.
 */
async function bestEffortRevokeServiceToken(tokenId: string | number): Promise<void> {
  try {
    await apiFetch(`/api/admin/service-tokens/${encodeURIComponent(String(tokenId))}`, { method: "DELETE" });
  } catch {
    // Caller already warned; nothing more to surface.
  }
}

function renderTokenRows(tokens: ServiceToken[]): string {
  if (tokens.length === 0) {
    return `<div class="muted" data-i18n-text="settings.serviceAccounts.tokens.empty">No tokens yet.</div>`;
  }
  return `
    <table class="api-tokens-table">
      <thead>
        <tr>
          <th data-i18n-text="settings.profile.apiTokens.table.name">Name</th>
          <th data-i18n-text="settings.profile.apiTokens.table.created">Created</th>
          <th data-i18n-text="settings.profile.apiTokens.table.lastUsed">Last used</th>
          <th data-i18n-text="settings.profile.apiTokens.table.status">Status</th>
          <th data-i18n-text="settings.profile.apiTokens.table.actions">Actions</th>
        </tr>
      </thead>
      <tbody>
        ${tokens.map((tok) => `
          <tr>
            <td>${tok.name ? escapeHTML(tok.name) : `<span class="muted" data-i18n-text="settings.profile.apiTokens.unnamed">(unnamed)</span>`}</td>
            <td>${escapeHTML(formatDate(tok.createdAt, DATE_OPTS))}</td>
            <td>${tok.lastUsedAt ? escapeHTML(formatDate(tok.lastUsedAt, DATE_OPTS)) : `<span class="muted" data-i18n-text="settings.profile.apiTokens.neverUsed">Never used</span>`}</td>
            <td>${tok.revokedAt
              ? `<span class="status-pill status-pill--revoked" data-i18n-text="settings.profile.apiTokens.badge.revoked">Revoked</span>`
              : `<span class="status-pill status-pill--active" data-i18n-text="settings.profile.apiTokens.badge.active">Active</span>`}</td>
            <td>${tok.revokedAt ? "" : `<button class="btn btn--danger btn--small" data-action="sa-revoke" data-token-id="${tok.tokenId}" data-token-name="${escapeHTML(tok.name ?? "")}" data-i18n-text="settings.profile.apiTokens.actions.revoke">Revoke</button>`}</td>
          </tr>`).join("")}
      </tbody>
    </table>`;
}

/** Token management dialog for one service account; minting is Owner-only (server-enforced). */
async function showServiceAccountTokensDialog(accountId: number, accountName: string, isOwner: boolean): Promise<void> {
  const dialog = document.createElement("dialog");
  dialog.className = "dialog";
  dialog.dataset.saTokensDialog = "1";
  dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title" id="saTokensTitle">${escapeHTML(t("settings.serviceAccounts.tokens.title", { name: accountName }))}</div>
        <button class="btn btn--ghost" type="button" id="saTokensClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>
      <div id="saTokensBody"></div>
      ${isOwner ? `
        <form id="saTokenMintForm" class="api-tokens-create">
          <label class="field api-tokens-create__name">
            <div class="field__label" data-i18n-text="settings.profile.apiTokens.create.nameLabel">Name (optional)</div>
            <input type="text" id="saTokenMintName" class="input" maxlength="200" placeholder="e.g. production" data-i18n-placeholder="settings.serviceAccounts.tokens.namePlaceholder" />
          </label>
          <button type="submit" class="btn" id="saTokenMintSubmit" data-i18n-text="settings.profile.apiTokens.create.submit">Create token</button>
        </form>` : `<p class="muted" data-i18n-text="settings.serviceAccounts.tokens.ownerOnlyMint">Only the system owner can create service tokens.</p>`}
    </div>
  `;
  document.body.appendChild(dialog);
  (dialog as HTMLDialogElement).showModal();
  const syncTitleLocale = () => {
    const title = dialog.querySelector<HTMLElement>("#saTokensTitle");
    if (title) title.textContent = t("settings.serviceAccounts.tokens.title", { name: accountName });
  };
  const releaseLocale = bindDialogLocale(dialog, syncTitleLocale);
  let dialogAlive = true;
  let refreshGeneration = 0;
  let unregister = () => {};
  const close = attachDialogClose(dialog, releaseLocale, () => {
    dialogAlive = false;
    refreshGeneration++;
    unregister();
  });
  unregister = registerServiceAccountDialogCloser(close);
  dialog.querySelector("#saTokensClose")?.addEventListener("click", close);
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) close();
  });

  const bodyEl = dialog.querySelector<HTMLElement>("#saTokensBody")!;
  const pendingRevokes = new Set<string>();

  const wireRevokeButtons = () => {
    bodyEl.querySelectorAll<HTMLElement>('[data-action="sa-revoke"]').forEach((btn) => {
      btn.addEventListener("click", async () => {
        if (!dialogAlive) return;
        const id = btn.getAttribute("data-token-id");
        if (!id || pendingRevokes.has(id)) return;
        const name = btn.getAttribute("data-token-name") || t("settings.profile.apiTokens.unnamed");
        const confirmed = await showConfirmDialog(
          t("settings.profile.apiTokens.revoke.confirmMessage", { name }),
          t("settings.profile.apiTokens.revoke.confirmTitle"),
          t("settings.profile.apiTokens.revoke.confirmAction"),
        );
        if (!dialogAlive || !confirmed || pendingRevokes.has(id)) return;
        pendingRevokes.add(id);
        if (btn instanceof HTMLButtonElement) btn.disabled = true;
        try {
          try {
            await apiFetch(`/api/admin/service-tokens/${encodeURIComponent(id)}`, { method: "DELETE" });
          } catch (err: any) {
            if (err?.status !== 404) {
              if (dialogAlive) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.toast.revokeFailed" }));
                if (btn instanceof HTMLButtonElement) btn.disabled = false;
              }
              return;
            }
          }
          if (dialogAlive) showToast(t("settings.profile.apiTokens.toast.revoked"));
        } finally {
          pendingRevokes.delete(id);
        }
        if (dialogAlive) {
          try {
            await refresh();
          } catch {
            // Revocation already succeeded; refresh is best-effort.
          }
        }
      });
    });
  };

  const refresh = async () => {
    const generation = ++refreshGeneration;
    try {
      const items = await fetchAllPages<ServiceToken>(`/api/admin/service-accounts/${accountId}/tokens`);
      if (!dialogAlive || generation !== refreshGeneration) return;
      bodyEl.innerHTML = renderTokenRows(items);
      // Dynamically inserted rows need locale hydration after render.
      hydrateI18n(bodyEl);
      wireRevokeButtons();
    } catch (err: any) {
      if (!dialogAlive || generation !== refreshGeneration) return;
      bodyEl.innerHTML = `<div class="muted" role="alert">${escapeHTML(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.tokens.loadFailed" }))}</div>`;
    }
  };

  const mintForm = dialog.querySelector<HTMLFormElement>("#saTokenMintForm");
  mintForm?.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!dialogAlive) return;
    const lifecycleGen = serviceAccountsLifecycleGeneration;
    const nameInput = dialog.querySelector<HTMLInputElement>("#saTokenMintName");
    const submit = dialog.querySelector<HTMLButtonElement>("#saTokenMintSubmit");
    const name = nameInput?.value.trim() || undefined;
    const resetSubmit = () => {
      if (submit && dialogAlive && lifecycleStillValid(lifecycleGen)) {
        submit.disabled = false;
        submit.textContent = t("settings.profile.apiTokens.create.submit");
      }
    };
    if (submit) {
      if (submit.disabled) return;
      submit.disabled = true;
      submit.textContent = t("settings.profile.apiTokens.create.creating");
    }

    let created: MintResponse | null | undefined;
    try {
      created = await apiFetch<MintResponse>(`/api/admin/service-accounts/${accountId}/tokens`, {
        method: "POST",
        body: JSON.stringify({ name }),
      });
    } catch (err: any) {
      if (dialogAlive && lifecycleStillValid(lifecycleGen)) {
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.toast.createFailed" }));
      }
      resetSubmit();
      return;
    }

    // Authenticated session / Settings lifecycle ended while mint was in flight:
    // never show the secret and never revoke under a different session.
    if (!lifecycleStillValid(lifecycleGen)) {
      return;
    }

    if (!dialogAlive) {
      // Same lifecycle, dialog dismissed before the secret could be shown — revoke.
      if (created?.tokenId != null) {
        await bestEffortRevokeServiceToken(created.tokenId);
      }
      return;
    }

    const secret = created?.token;
    const hasUsableSecret = typeof secret === "string" && secret.length > 0;
    if (!hasUsableSecret) {
      if (created?.tokenId != null) {
        await bestEffortRevokeServiceToken(created.tokenId);
      }
      if (!lifecycleStillValid(lifecycleGen) || !dialogAlive) return;
      showToast(t("settings.profile.apiTokens.toast.createMalformed"));
      try {
        await refresh();
      } catch {
        // Best-effort only.
      }
      resetSubmit();
      return;
    }

    if (nameInput) nameInput.value = "";
    showToast(t("settings.profile.apiTokens.toast.created"));
    showServiceTokenCreatedDialog(secret);
    try {
      await refresh();
    } catch {
      // Secret already shown; refresh failure must not look like mint failure.
    }
    resetSubmit();
  });

  await refresh();
}

/** Wires the controls rendered by {@link renderServiceAccountsSectionHTML}. */
export function bindServiceAccountsInteractions({ signal, rerender, isOwner }: BindOptions): void {
  const form = document.getElementById("createServiceAccountForm") as HTMLFormElement | null;
  form?.addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = document.getElementById("createServiceAccountName") as HTMLInputElement | null;
    const submit = document.getElementById("createServiceAccountSubmit") as HTMLButtonElement | null;
    const name = input?.value.trim();
    if (!name) return;
    if (submit?.disabled) return;
    if (submit) submit.disabled = true;
    const lifecycleGen = serviceAccountsLifecycleGeneration;
    try {
      await apiFetch("/api/admin/service-accounts", { method: "POST", body: JSON.stringify({ name }) });
      if (signal.aborted || !lifecycleStillValid(lifecycleGen)) return;
      showToast(t("settings.serviceAccounts.toast.created"));
      try {
        await rerender();
      } catch {
        // Create succeeded; refresh is best-effort.
      }
    } catch (err: any) {
      if (signal.aborted || !lifecycleStillValid(lifecycleGen)) return;
      showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.toast.createFailed" }));
      if (submit) submit.disabled = false;
    }
  }, { signal });

  document.querySelectorAll<HTMLElement>('[data-action="sa-tokens"]').forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = Number(btn.getAttribute("data-sa-id"));
      if (!Number.isFinite(id) || id <= 0) return;
      void showServiceAccountTokensDialog(id, btn.getAttribute("data-sa-name") ?? "", isOwner);
    }, { signal });
  });

  document.querySelectorAll<HTMLElement>('[data-action="sa-delete"]').forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.getAttribute("data-sa-id");
      if (!id) return;
      const name = btn.getAttribute("data-sa-name") ?? "";
      const lifecycleGen = serviceAccountsLifecycleGeneration;
      const confirmed = await showConfirmDialog(
        t("settings.serviceAccounts.delete.confirmMessage", { name }),
        t("settings.serviceAccounts.delete.confirmTitle"),
        t("settings.serviceAccounts.delete.confirmAction"),
      );
      if (!confirmed) return;
      if (signal.aborted || !lifecycleStillValid(lifecycleGen)) return;
      if (btn instanceof HTMLButtonElement) {
        if (btn.disabled) return;
        btn.disabled = true;
      }
      try {
        await apiFetch(`/api/admin/service-accounts/${encodeURIComponent(id)}`, { method: "DELETE" });
        if (signal.aborted || !lifecycleStillValid(lifecycleGen)) return;
        showToast(t("settings.serviceAccounts.toast.deleted"));
        try {
          await rerender();
        } catch {
          // Deletion already final.
        }
      } catch (err: any) {
        if (signal.aborted || !lifecycleStillValid(lifecycleGen)) return;
        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.toast.deleteFailed" }));
        if (btn instanceof HTMLButtonElement) btn.disabled = false;
      }
    }, { signal });
  });
}
