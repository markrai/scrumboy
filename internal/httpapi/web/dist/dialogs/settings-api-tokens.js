import { apiFetch } from '../api.js';
import { attachDialogClose, bindDialogLocale, escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { apiErrorMessageOrRaw, formatDate, t } from '../i18n/index.js';
let cachedApiTokens = null;
let apiTokensLoadErrorMessage = null;
let apiTokensCacheGeneration = 0;
/** Drop the cached API token list so the Profile tab refetches on next render. */
export function invalidateApiTokensCache() {
    cachedApiTokens = null;
    apiTokensLoadErrorMessage = null;
    apiTokensCacheGeneration++;
}
const API_TOKEN_DATE_OPTS = {
    month: "short",
    day: "numeric",
    year: "numeric",
};
function formatApiTokenDate(value) {
    if (!value)
        return t("settings.profile.apiTokens.neverUsed");
    return formatDate(value, API_TOKEN_DATE_OPTS);
}
function renderApiTokenStatusBadges(token) {
    const badges = [];
    if (token.revokedAt) {
        badges.push(`<span class="status-pill status-pill--revoked" data-i18n-text="settings.profile.apiTokens.badge.revoked">Revoked</span>`);
    }
    else {
        badges.push(`<span class="status-pill status-pill--active" data-i18n-text="settings.profile.apiTokens.badge.active">Active</span>`);
    }
    if (token.isService) {
        badges.push(`<span class="status-pill status-pill--service" data-i18n-text="settings.profile.apiTokens.badge.service">Service</span>`);
    }
    return badges.join(" ");
}
async function loadApiTokens() {
    if (cachedApiTokens)
        return cachedApiTokens;
    const generation = apiTokensCacheGeneration;
    let res;
    try {
        res = await apiFetch("/api/me/tokens");
    }
    catch (err) {
        if (generation !== apiTokensCacheGeneration) {
            // This failure belongs to a request invalidated while it was in flight.
            // Do not let an obsolete error replace a newer generation's result.
            return loadApiTokens();
        }
        throw err;
    }
    if (generation !== apiTokensCacheGeneration) {
        // The cache was invalidated (by a create/revoke, possibly already repopulated
        // by a newer GET) while this request was in flight — this response is stale.
        // Never let it clobber a fresher cache; read the current generation instead.
        return loadApiTokens();
    }
    cachedApiTokens = res?.items ?? [];
    return cachedApiTokens;
}
/** Renders the "API Tokens" section of the Profile tab: existing tokens + a create-token form. */
export async function renderApiTokensSectionHTML() {
    let tokens = [];
    try {
        tokens = await loadApiTokens();
        apiTokensLoadErrorMessage = null;
    }
    catch (err) {
        apiTokensLoadErrorMessage = apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.loadFailed" });
        tokens = [];
    }
    const rowsHTML = tokens
        .map((tok) => {
        const displayName = tok.name
            ? escapeHTML(tok.name)
            : `<span class="muted" data-i18n-text="settings.profile.apiTokens.unnamed">(unnamed)</span>`;
        const actionsHTML = tok.revokedAt
            ? "-"
            : `<button class="btn btn--danger btn--small" data-action="revoke-api-token" data-token-id="${escapeHTML(String(tok.id))}" data-token-name="${escapeHTML(tok.name || "")}" data-i18n-text="settings.profile.apiTokens.actions.revoke">Revoke</button>`;
        return `
        <tr>
          <td>${displayName}</td>
          <td>${escapeHTML(formatApiTokenDate(tok.createdAt))}</td>
          <td>${escapeHTML(formatApiTokenDate(tok.lastUsedAt))}</td>
          <td>${renderApiTokenStatusBadges(tok)}</td>
          <td>${actionsHTML}</td>
        </tr>
      `;
    })
        .join("");
    const listHTML = apiTokensLoadErrorMessage
        ? `<div class="muted" role="alert">${escapeHTML(apiTokensLoadErrorMessage)}</div>`
        : tokens.length === 0
            ? `<div class="muted" data-i18n-text="settings.profile.apiTokens.empty">No API tokens yet.</div>`
            : `
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
          <tbody>${rowsHTML}</tbody>
        </table>
      `;
    return `
    <div class="settings-section" style="margin-top: 24px;">
      <div class="settings-section__title" data-i18n-text="settings.profile.apiTokens.title">API Tokens</div>
      <div class="settings-section__description muted" data-i18n-text="settings.profile.apiTokens.description">Tokens for authenticating with Scrumboy's API and MCP endpoint outside the browser.</div>
      ${listHTML}
      <form id="createApiTokenForm" class="api-tokens-create">
        <label class="field api-tokens-create__name">
          <div class="field__label" data-i18n-text="settings.profile.apiTokens.create.nameLabel">Name (optional)</div>
          <input type="text" id="createApiTokenName" class="input" data-i18n-placeholder="settings.profile.apiTokens.create.namePlaceholder" placeholder="e.g. CI pipeline" maxlength="200" />
        </label>
        <label class="api-tokens-create__service">
          <input type="checkbox" id="createApiTokenService" />
          <span data-i18n-text="settings.profile.apiTokens.create.serviceLabel">Service token — if this account is deleted, this token is deleted too; only a metadata record is kept for review, and another owner must create a replacement</span>
        </label>
        <button type="submit" class="btn" id="createApiTokenSubmit" data-i18n-text="settings.profile.apiTokens.create.submit">Create token</button>
      </form>
    </div>
  `;
}
/** Shows the newly-created API token's secret once, with a copy-to-clipboard action. */
function showApiTokenCreatedDialog(token) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="settings.profile.apiTokens.created.title">Token created</div>
        <button class="btn btn--ghost" type="button" id="apiTokenCreatedClose" aria-label="Close" data-i18n-aria-label="common.close">✕</button>
      </div>
      <p class="muted" role="alert" data-i18n-text="settings.profile.apiTokens.created.warning">Copy this token now — it won't be shown again.</p>
      <div class="field" style="margin: 12px 0;">
        <input type="text" id="apiTokenCreatedDisplay" class="input" readonly value="${escapeHTML(token)}" style="font-size: 12px;" />
      </div>
      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn" id="apiTokenCreatedCopy" data-i18n-text="settings.profile.apiTokens.created.copy">Copy</button>
        <button type="button" class="btn" id="apiTokenCreatedDone" data-i18n-text="settings.profile.apiTokens.created.done">Done</button>
      </div>
    </div>
  `;
    document.body.appendChild(dialog);
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const closeBtn = dialog.querySelector("#apiTokenCreatedClose");
    const doneBtn = dialog.querySelector("#apiTokenCreatedDone");
    const copyBtn = dialog.querySelector("#apiTokenCreatedCopy");
    const tokenInput = dialog.querySelector("#apiTokenCreatedDisplay");
    const close = attachDialogClose(dialog, releaseLocale);
    if (closeBtn)
        closeBtn.addEventListener("click", close);
    if (doneBtn)
        doneBtn.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
    if (copyBtn && tokenInput) {
        copyBtn.addEventListener("click", async () => {
            try {
                await navigator.clipboard.writeText(tokenInput.value);
                showToast(t("settings.profile.apiTokens.created.copied"));
            }
            catch {
                tokenInput.select();
                showToast(t("settings.profile.apiTokens.created.copyManual"));
            }
        });
    }
}
/**
 * Best-effort revoke of a token whose secret we couldn't confirm was shown to the user
 * (a malformed create response). Never lets a revoke failure surface as an error of its
 * own — the caller already has its own warning to show regardless.
 */
async function bestEffortRevoke(tokenId) {
    try {
        await apiFetch(`/api/me/tokens/${encodeURIComponent(String(tokenId))}`, { method: "DELETE" });
    }
    catch {
        // Nothing more we can do here; the caller warns the user to review it manually.
    }
}
/** Token ids with a revoke DELETE currently in flight, so a second confirmed click can't issue another. */
const pendingRevokeTokenIds = new Set();
/** Wires up the create-token form and revoke buttons rendered by {@link renderApiTokensSectionHTML}. */
export function bindApiTokensInteractions({ signal, rerender }) {
    const createApiTokenForm = document.getElementById("createApiTokenForm");
    if (createApiTokenForm) {
        createApiTokenForm.addEventListener("submit", async (e) => {
            e.preventDefault();
            const nameInput = document.getElementById("createApiTokenName");
            const serviceInput = document.getElementById("createApiTokenService");
            const submitBtn = document.getElementById("createApiTokenSubmit");
            const name = nameInput?.value.trim() || undefined;
            const isService = !!serviceInput?.checked;
            const resetSubmitButton = () => {
                if (submitBtn) {
                    submitBtn.disabled = false;
                    submitBtn.textContent = t("settings.profile.apiTokens.create.submit");
                }
            };
            if (submitBtn) {
                submitBtn.disabled = true;
                submitBtn.textContent = t("settings.profile.apiTokens.create.creating");
            }
            let created;
            try {
                created = await apiFetch("/api/me/tokens", {
                    method: "POST",
                    body: JSON.stringify({ name, isService }),
                });
            }
            catch (err) {
                // The POST itself failed — no credential was created (or if it was, the caller
                // never learned its id), so this is a genuine creation failure.
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.toast.createFailed" }));
                resetSubmitButton();
                return;
            }
            const secret = created?.token;
            const hasUsableSecret = typeof secret === "string" && secret.length > 0;
            if (!hasUsableSecret) {
                // A 2xx response without a usable secret: the server may still have created a
                // live credential we can never show. Never report this as ordinary success.
                invalidateApiTokensCache();
                if (created?.id != null) {
                    await bestEffortRevoke(created.id);
                }
                showToast(t("settings.profile.apiTokens.toast.createMalformed"));
                try {
                    await rerender();
                }
                catch {
                    // Best-effort refresh only; the warning above already told the user to check.
                }
                resetSubmitButton();
                return;
            }
            // We have a real, one-time secret. Show it immediately — before any further
            // fallible work — so a rejected rerender() can never cause it to be lost or
            // this successful creation to be reported as a failure.
            invalidateApiTokensCache();
            showToast(t("settings.profile.apiTokens.toast.created"));
            showApiTokenCreatedDialog(secret);
            try {
                await rerender();
            }
            catch {
                // The token was created and its secret already shown to the user; a refresh
                // failure here is a separate, lesser problem and must not be conflated with
                // creation failure (the button stays reset below either way).
            }
            resetSubmitButton();
        }, { signal });
    }
    document.querySelectorAll('[data-action="revoke-api-token"]').forEach((btn) => {
        btn.addEventListener("click", async (e) => {
            const el = e.currentTarget;
            const tokenId = el.getAttribute("data-token-id");
            if (!tokenId)
                return;
            if (pendingRevokeTokenIds.has(tokenId))
                return;
            const tokenName = el.getAttribute("data-token-name") || t("settings.profile.apiTokens.unnamed");
            const confirmed = await showConfirmDialog(t("settings.profile.apiTokens.revoke.confirmMessage", { name: tokenName }), t("settings.profile.apiTokens.revoke.confirmTitle"), t("settings.profile.apiTokens.revoke.confirmAction"));
            if (!confirmed)
                return;
            // Re-check after the confirm dialog: another confirmed click could have started
            // (and even finished) a DELETE for this same token while we were awaiting input.
            if (pendingRevokeTokenIds.has(tokenId))
                return;
            pendingRevokeTokenIds.add(tokenId);
            if (el instanceof HTMLButtonElement)
                el.disabled = true;
            try {
                try {
                    await apiFetch(`/api/me/tokens/${encodeURIComponent(tokenId)}`, { method: "DELETE" });
                }
                catch (err) {
                    if (err?.status !== 404) {
                        showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.toast.revokeFailed" }));
                        if (el instanceof HTMLButtonElement)
                            el.disabled = false;
                        return;
                    }
                    // Already revoked — by our own duplicate click, or another tab/session. The
                    // desired end state was already achieved, so treat it as success.
                }
                invalidateApiTokensCache();
                showToast(t("settings.profile.apiTokens.toast.revoked"));
                try {
                    await rerender();
                }
                catch {
                    // Revocation is already final. Refreshing the surrounding settings UI is
                    // best-effort and must not turn mutation success into a reported failure.
                }
            }
            finally {
                pendingRevokeTokenIds.delete(tokenId);
            }
        }, { signal });
    });
}
