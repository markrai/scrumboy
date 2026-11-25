import { apiFetch } from '../api.js';
import { attachDialogClose, bindDialogLocale, escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { apiErrorMessageOrRaw, formatDate, t } from '../i18n/index.js';
const DATE_OPTS = { month: "short", day: "numeric", year: "numeric" };
export async function renderServiceAccountsSectionHTML(isOwner) {
    let body;
    try {
        const res = await apiFetch("/api/admin/service-accounts?limit=100");
        const items = res?.items ?? [];
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
    }
    catch (err) {
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
/** Shows a freshly minted token once, with a copy action. */
function showServiceTokenCreatedDialog(token) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
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
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const close = attachDialogClose(dialog, releaseLocale);
    dialog.querySelector("#saTokenCreatedClose")?.addEventListener("click", close);
    dialog.querySelector("#saTokenCreatedDone")?.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
    const input = dialog.querySelector("#saTokenCreatedDisplay");
    dialog.querySelector("#saTokenCreatedCopy")?.addEventListener("click", async () => {
        try {
            await navigator.clipboard.writeText(input?.value ?? "");
            showToast(t("settings.serviceAccounts.tokenCreated.copied"));
        }
        catch {
            input?.select();
            showToast(t("settings.serviceAccounts.tokenCreated.copyManual"));
        }
    });
}
function renderTokenRows(tokens, isOwner) {
    void isOwner;
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
async function showServiceAccountTokensDialog(accountId, accountName, isOwner) {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog";
    dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title">${escapeHTML(t("settings.serviceAccounts.tokens.title", { name: accountName }))}</div>
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
    dialog.showModal();
    const releaseLocale = bindDialogLocale(dialog);
    const close = attachDialogClose(dialog, releaseLocale);
    dialog.querySelector("#saTokensClose")?.addEventListener("click", close);
    dialog.addEventListener("click", (e) => {
        if (e.target === dialog)
            close();
    });
    const bodyEl = dialog.querySelector("#saTokensBody");
    const pendingRevokes = new Set();
    const refresh = async () => {
        try {
            const res = await apiFetch(`/api/admin/service-accounts/${accountId}/tokens?limit=100`);
            bodyEl.innerHTML = renderTokenRows(res?.items ?? [], isOwner);
        }
        catch (err) {
            bodyEl.innerHTML = `<div class="muted" role="alert">${escapeHTML(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.tokens.loadFailed" }))}</div>`;
        }
        bodyEl.querySelectorAll('[data-action="sa-revoke"]').forEach((btn) => {
            btn.addEventListener("click", async () => {
                const id = btn.getAttribute("data-token-id");
                if (!id || pendingRevokes.has(id))
                    return;
                const name = btn.getAttribute("data-token-name") || t("settings.profile.apiTokens.unnamed");
                const confirmed = await showConfirmDialog(t("settings.profile.apiTokens.revoke.confirmMessage", { name }), t("settings.profile.apiTokens.revoke.confirmTitle"), t("settings.profile.apiTokens.revoke.confirmAction"));
                if (!confirmed || pendingRevokes.has(id))
                    return;
                pendingRevokes.add(id);
                if (btn instanceof HTMLButtonElement)
                    btn.disabled = true;
                try {
                    await apiFetch(`/api/admin/service-tokens/${encodeURIComponent(id)}`, { method: "DELETE" });
                    showToast(t("settings.profile.apiTokens.toast.revoked"));
                }
                catch (err) {
                    showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.toast.revokeFailed" }));
                }
                finally {
                    pendingRevokes.delete(id);
                }
                await refresh();
            });
        });
    };
    const mintForm = dialog.querySelector("#saTokenMintForm");
    mintForm?.addEventListener("submit", async (e) => {
        e.preventDefault();
        const nameInput = dialog.querySelector("#saTokenMintName");
        const submit = dialog.querySelector("#saTokenMintSubmit");
        const name = nameInput?.value.trim() || undefined;
        if (submit)
            submit.disabled = true;
        try {
            const created = await apiFetch(`/api/admin/service-accounts/${accountId}/tokens`, {
                method: "POST",
                body: JSON.stringify({ name }),
            });
            if (!created?.token) {
                showToast(t("settings.profile.apiTokens.toast.createMalformed"));
            }
            else {
                if (nameInput)
                    nameInput.value = "";
                showServiceTokenCreatedDialog(created.token);
            }
        }
        catch (err) {
            showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.profile.apiTokens.toast.createFailed" }));
        }
        finally {
            if (submit)
                submit.disabled = false;
        }
        await refresh();
    });
    await refresh();
}
/** Wires the controls rendered by {@link renderServiceAccountsSectionHTML}. */
export function bindServiceAccountsInteractions({ signal, rerender, isOwner }) {
    const form = document.getElementById("createServiceAccountForm");
    form?.addEventListener("submit", async (e) => {
        e.preventDefault();
        const input = document.getElementById("createServiceAccountName");
        const submit = document.getElementById("createServiceAccountSubmit");
        const name = input?.value.trim();
        if (!name)
            return;
        if (submit)
            submit.disabled = true;
        try {
            await apiFetch("/api/admin/service-accounts", { method: "POST", body: JSON.stringify({ name }) });
            showToast(t("settings.serviceAccounts.toast.created"));
            await rerender();
        }
        catch (err) {
            showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.toast.createFailed" }));
            if (submit)
                submit.disabled = false;
        }
    }, { signal });
    document.querySelectorAll('[data-action="sa-tokens"]').forEach((btn) => {
        btn.addEventListener("click", () => {
            const id = Number(btn.getAttribute("data-sa-id"));
            if (!Number.isFinite(id) || id <= 0)
                return;
            void showServiceAccountTokensDialog(id, btn.getAttribute("data-sa-name") ?? "", isOwner);
        }, { signal });
    });
    document.querySelectorAll('[data-action="sa-delete"]').forEach((btn) => {
        btn.addEventListener("click", async () => {
            const id = btn.getAttribute("data-sa-id");
            if (!id)
                return;
            const name = btn.getAttribute("data-sa-name") ?? "";
            const confirmed = await showConfirmDialog(t("settings.serviceAccounts.delete.confirmMessage", { name }), t("settings.serviceAccounts.delete.confirmTitle"), t("settings.serviceAccounts.delete.confirmAction"));
            if (!confirmed)
                return;
            if (btn instanceof HTMLButtonElement)
                btn.disabled = true;
            try {
                await apiFetch(`/api/admin/service-accounts/${encodeURIComponent(id)}`, { method: "DELETE" });
                showToast(t("settings.serviceAccounts.toast.deleted"));
                await rerender();
            }
            catch (err) {
                showToast(apiErrorMessageOrRaw(err, { fallbackKey: "settings.serviceAccounts.toast.deleteFailed" }));
                if (btn instanceof HTMLButtonElement)
                    btn.disabled = false;
            }
        }, { signal });
    });
}
