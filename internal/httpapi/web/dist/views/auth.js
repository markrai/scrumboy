import { app } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { showToast, getAppVersion, escapeHTML, redirectAfterAuth } from '../utils.js';
export function renderAuth(opts = {}) {
    const next = opts.next || (window.location.pathname + window.location.search);
    const bootstrapAvailable = !!opts.bootstrap;
    const oidcEnabled = !!opts.oidcEnabled;
    const localAuthEnabled = opts.localAuthEnabled !== false;
    const version = getAppVersion();
    const ssoButtonHTML = oidcEnabled
        ? `<a class="btn btn--sso" href="/api/auth/oidc/login?return_to=${encodeURIComponent(next)}">Continue with SSO</a>`
        : '';
    const showLocalForm = localAuthEnabled;
    const dividerHTML = oidcEnabled && showLocalForm
        ? `<div class="auth-divider"><span>or</span></div>`
        : '';
    app.innerHTML = `
    <div class="page page--auth">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
      </div>
      <div class="container">
        <div class="panel">
          <div class="panel__header">
            <div class="panel__title">${bootstrapAvailable ? "First-time setup" : "Sign in"}</div>
          </div>
          <div class="muted" style="margin-bottom: 12px;">
            Authentication is enabled for this instance. Anonymous boards remain shareable by URL; durable projects require sign-in.
          </div>
          ${ssoButtonHTML}
          ${dividerHTML}
          ${showLocalForm ? `
          <form id="authForm" class="stack">
            ${bootstrapAvailable ? `<input class="input" id="authName" placeholder="Name" maxlength="200" autocomplete="name" required />` : ``}
            <input class="input" id="authEmail" placeholder="Email" maxlength="200" autocomplete="email" required />
            <div class="password-row">
              <input class="input" id="authPassword" placeholder="Password" type="password" maxlength="200" autocomplete="current-password" required />
              <button type="button" class="password-toggle" id="authPasswordToggle" aria-label="Show password" title="Show password">
                <svg id="authPasswordIcon" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z"/></svg>
              </button>
            </div>
            <div class="row" style="margin-top: 8px;">
              <div class="spacer"></div>
              ${bootstrapAvailable
        ? `<button class="btn" type="button" id="bootstrapBtn" title="First-time setup">Bootstrap</button>`
        : `<button class="btn" type="submit" id="loginBtn">Login</button>`}
            </div>
          </form>
          ` : ''}
        </div>
      </div>
      ${version ? `<div class="app-version">v${escapeHTML(version)}</div>` : ''}
    </div>
  `;
    const nameEl = document.getElementById("authName");
    const emailEl = document.getElementById("authEmail");
    const pwEl = document.getElementById("authPassword");
    const pwToggle = document.getElementById("authPasswordToggle");
    const pwIcon = document.getElementById("authPasswordIcon")?.querySelector("path");
    const PATH_SHOW = "M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z";
    const PATH_HIDE = "M2 5.27L3.28 4 20 20.72 18.73 22 15.65 18.92C14.5 19.3 13.28 19.5 12 19.5 7 19.5 2.73 16.39 1 12c.69-1.76 1.79-3.31 3.19-4.54L2 5.27zM12 9a3 3 0 0 1 3 3c0 .35-.06.69-.17 1l-3.83-3.83c.31-.06.65-.17 1-.17zM12 4.5c5 0 9.27 3.11 11 7.5-.82 2.08-2.21 3.88-4 5.19L17.58 15.76C18.94 14.82 20.06 13.54 20.82 12 19.17 8.64 15.76 6.5 12 6.5c-1.09 0-2.16.18-3.16.5L7.3 5.47C8.74 4.85 10.33 4.5 12 4.5zM3.18 12C4.83 15.36 8.24 17.5 12 17.5c.69 0 1.37-.07 2-.21L11.72 15c-1.43-.15-2.57-1.29-2.72-2.72L5.6 8.87C4.61 9.72 3.78 10.78 3.18 12z";
    if (pwToggle && pwEl && pwIcon) {
        pwToggle.addEventListener("click", () => {
            const isPassword = pwEl.type === "password";
            pwEl.type = isPassword ? "text" : "password";
            pwIcon.setAttribute("d", isPassword ? PATH_HIDE : PATH_SHOW);
            pwToggle.setAttribute("aria-label", isPassword ? "Hide password" : "Show password");
            pwToggle.setAttribute("title", isPassword ? "Hide password" : "Show password");
        });
    }
    const params = new URLSearchParams(window.location.search);
    const oidcError = params.get('oidc_error');
    if (oidcError) {
        const msgs = {
            state_invalid: 'Login session expired or invalid. Please try again.',
            provider: 'The identity provider returned an error.',
            token: 'Authentication failed. Please try again.',
            email: 'A verified email address is required.',
        };
        showToast(msgs[oidcError] || 'Authentication failed.');
        window.history.replaceState({}, '', window.location.pathname);
    }
    if (bootstrapAvailable) {
        const bootstrapBtn = document.getElementById("bootstrapBtn");
        if (bootstrapBtn) {
            bootstrapBtn.addEventListener("click", async () => {
                const name = nameEl ? nameEl.value : "";
                const email = emailEl.value;
                const password = pwEl.value;
                try {
                    await apiFetch("/api/auth/bootstrap", { method: "POST", body: JSON.stringify({ name, email, password }) });
                    redirectAfterAuth(next || "/");
                }
                catch (err) {
                    showToast(err.message);
                }
            });
        }
    }
    else {
        const authForm = document.getElementById("authForm");
        if (authForm) {
            authForm.addEventListener("submit", async (e) => {
                e.preventDefault();
                const email = emailEl.value;
                const password = pwEl.value;
                try {
                    const res = await apiFetch("/api/auth/login", { method: "POST", body: JSON.stringify({ email, password }) });
                    if (res && res.requires2fa && res.tempToken && res.user) {
                        render2FAStep({ tempToken: res.tempToken, user: res.user, next });
                        return;
                    }
                    redirectAfterAuth(next || "/");
                }
                catch (err) {
                    const msg = err?.status === 429 ? "Too many attempts; try again later." : err?.message;
                    showToast(msg);
                }
            });
        }
    }
}
function render2FAStep(opts) {
    const { tempToken, user, next } = opts;
    const displayName = user.email || user.name || "your account";
    const version = getAppVersion();
    app.innerHTML = `
    <div class="page page--auth">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
      </div>
      <div class="container">
        <div class="panel">
          <div class="panel__header">
            <div class="panel__title">Two-factor authentication</div>
          </div>
          <div class="muted" style="margin-bottom: 12px;">
            Enter the 6-digit code from your authenticator app, or a recovery code.
          </div>
          <form id="auth2FAForm" class="stack">
            <input class="input" id="auth2FACode" placeholder="Code for ${escapeHTML(displayName)}" maxlength="20" autocomplete="one-time-code" required />
            <div class="row" style="margin-top: 8px;">
              <div class="spacer"></div>
              <button class="btn" type="submit" id="auth2FASubmit">Verify</button>
            </div>
          </form>
        </div>
      </div>
      ${version ? `<div class="app-version">v${escapeHTML(version)}</div>` : ""}
    </div>
  `;
    const form = document.getElementById("auth2FAForm");
    const codeEl = document.getElementById("auth2FACode");
    if (form && codeEl) {
        form.addEventListener("submit", async (e) => {
            e.preventDefault();
            const code = codeEl.value.trim();
            try {
                await apiFetch("/api/auth/login/2fa", {
                    method: "POST",
                    body: JSON.stringify({ tempToken, code }),
                });
                redirectAfterAuth(next || "/");
            }
            catch (err) {
                const msg = err?.status === 429 ? "Too many attempts; try again later." : err?.message;
                showToast(msg);
            }
        });
    }
}
export function renderResetPassword(token) {
    const urlObj = new URL(window.location.href);
    const tokenFromUrl = token ?? urlObj.searchParams.get("token") ?? "";
    if (!tokenFromUrl) {
        showToast("Invalid or missing reset link");
        window.location.href = "/";
        return;
    }
    const version = getAppVersion();
    app.innerHTML = `
    <div class="page page--auth">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
      </div>
      <div class="container">
        <div class="panel">
          <div class="panel__header">
            <div class="panel__title">Reset Password</div>
          </div>
          <div class="muted" style="margin-bottom: 12px;">
            Enter your new password. The link expires in 30 minutes.
          </div>
          <form id="resetPasswordForm" class="stack">
            <label class="field">
              <div class="field__label">New password</div>
              <div class="password-row">
                <input class="input" id="resetNewPassword" type="password" placeholder="Min 8 characters" maxlength="200" autocomplete="new-password" required />
                <button type="button" class="password-toggle" id="resetPasswordToggle" aria-label="Show password" title="Show password">
                  <svg id="resetPasswordIcon" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z"/></svg>
                </button>
              </div>
            </label>
            <label class="field">
              <div class="field__label">Confirm password</div>
              <input class="input" id="resetConfirmPassword" type="password" placeholder="Confirm new password" maxlength="200" autocomplete="new-password" required />
            </label>
            <div class="row" style="margin-top: 8px;">
              <div class="spacer"></div>
              <button class="btn" type="submit" id="resetPasswordSubmit">Reset Password</button>
            </div>
          </form>
        </div>
      </div>
      ${version ? `<div class="app-version">v${escapeHTML(version)}</div>` : ""}
    </div>
  `;
    const form = document.getElementById("resetPasswordForm");
    const newPwEl = document.getElementById("resetNewPassword");
    const confirmPwEl = document.getElementById("resetConfirmPassword");
    const pwToggle = document.getElementById("resetPasswordToggle");
    const pwIcon = document.getElementById("resetPasswordIcon")?.querySelector("path");
    const PATH_SHOW = "M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z";
    const PATH_HIDE = "M2 5.27L3.28 4 20 20.72 18.73 22 15.65 18.92C14.5 19.3 13.28 19.5 12 19.5 7 19.5 2.73 16.39 1 12c.69-1.76 1.79-3.31 3.19-4.54L2 5.27zM12 9a3 3 0 0 1 3 3c0 .35-.06.69-.17 1l-3.83-3.83c.31-.06.65-.17 1-.17zM12 4.5c5 0 9.27 3.11 11 7.5-.82 2.08-2.21 3.88-4 5.19L17.58 15.76C18.94 14.82 20.06 13.54 20.82 12 19.17 8.64 15.76 6.5 12 6.5c-1.09 0-2.16.18-3.16.5L7.3 5.47C8.74 4.85 10.33 4.5 12 4.5zM3.18 12C4.83 15.36 8.24 17.5 12 17.5c.69 0 1.37-.07 2-.21L11.72 15c-1.43-.15-2.57-1.29-2.72-2.72L5.6 8.87C4.61 9.72 3.78 10.78 3.18 12z";
    if (pwToggle && newPwEl && confirmPwEl && pwIcon) {
        pwToggle.addEventListener("click", () => {
            const isPassword = newPwEl.type === "password";
            const nextType = isPassword ? "text" : "password";
            newPwEl.type = nextType;
            confirmPwEl.type = nextType;
            pwIcon.setAttribute("d", isPassword ? PATH_HIDE : PATH_SHOW);
            pwToggle.setAttribute("aria-label", isPassword ? "Hide password" : "Show password");
            pwToggle.setAttribute("title", isPassword ? "Hide password" : "Show password");
        });
    }
    if (form && newPwEl && confirmPwEl) {
        form.addEventListener("submit", async (e) => {
            e.preventDefault();
            const newPassword = newPwEl.value;
            const confirmPassword = confirmPwEl.value;
            if (newPassword !== confirmPassword) {
                showToast("Passwords do not match");
                return;
            }
            try {
                await apiFetch("/api/auth/reset-password", {
                    method: "POST",
                    body: JSON.stringify({ token: tokenFromUrl, new_password: newPassword }),
                });
                showToast("Password reset successfully. Please log in.");
                window.location.href = "/";
            }
            catch (err) {
                showToast(err.message || "Invalid or expired reset token");
            }
        });
    }
}
