import { app } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { I18N_LOCALE_CHANGED, apiErrorMessage, t } from '../i18n/index.js';
import { bindPublicLocaleSelect, renderPublicLocaleSelectHTML, syncPublicLocaleSelect } from '../i18n/locale-select.js';
import { showToast, getAppVersion, escapeHTML, redirectAfterAuth } from '../utils.js';

const PATH_SHOW = "M12 4.5C7 4.5 2.73 7.61 1 12c1.73 4.39 6 7.5 11 7.5s9.27-3.11 11-7.5c-1.73-4.39-6-7.5-11-7.5zM12 17c-2.76 0-5-2.24-5-5s2.24-5 5-5 5 2.24 5 5-2.24 5-5 5zm0-8c-1.66 0-3 1.34-3 3s1.34 3 3 3 3-1.34 3-3-1.34-3-3-3z";
const PATH_HIDE = "M2 5.27L3.28 4 20 20.72 18.73 22 15.65 18.92C14.5 19.3 13.28 19.5 12 19.5 7 19.5 2.73 16.39 1 12c.69-1.76 1.79-3.31 3.19-4.54L2 5.27zM12 9a3 3 0 0 1 3 3c0 .35-.06.69-.17 1l-3.83-3.83c.31-.06.65-.17 1-.17zM12 4.5c5 0 9.27 3.11 11 7.5-.82 2.08-2.21 3.88-4 5.19L17.58 15.76C18.94 14.82 20.06 13.54 20.82 12 19.17 8.64 15.76 6.5 12 6.5c-1.09 0-2.16.18-3.16.5L7.3 5.47C8.74 4.85 10.33 4.5 12 4.5zM3.18 12C4.83 15.36 8.24 17.5 12 17.5c.69 0 1.37-.07 2-.21L11.72 15c-1.43-.15-2.57-1.29-2.72-2.72L5.6 8.87C4.61 9.72 3.78 10.78 3.18 12z";

type AuthUser = { id: number; email?: string; name?: string };

type AuthBaseState = {
  mode: "auth";
  options: {
    next: string;
    bootstrap: boolean;
    oidcEnabled: boolean;
    localAuthEnabled: boolean;
    selfServicePasswordResetEnabled: boolean;
  };
  draft: {
    name: string;
    email: string;
    password: string;
  };
  passwordVisible: boolean;
};

type ForgotPasswordState = {
  mode: "forgot";
  options: {
    authState: AuthBaseState;
  };
  draft: {
    email: string;
  };
  submitting: boolean;
};

type TwoFactorState = {
  mode: "2fa";
  options: {
    tempToken: string;
    user: AuthUser;
    next: string;
  };
  draft: {
    code: string;
  };
};

type ResetPasswordState = {
  mode: "reset";
  options: {
    token: string;
  };
  draft: {
    newPassword: string;
    confirmPassword: string;
  };
  passwordVisible: boolean;
};

type AuthViewState = AuthBaseState | ForgotPasswordState | TwoFactorState | ResetPasswordState;
type AuthGlobal = typeof globalThis & {
  __scrumboyAuthLocaleListener?: EventListener;
};

let authViewState: AuthViewState | null = null;
let authLocaleListenerBound = false;

function getAuthLocaleSelect(): HTMLButtonElement | null {
  return document.getElementById("authLocaleSelect") as HTMLButtonElement | null;
}

function bindAuthLocaleSelect(): void {
  bindPublicLocaleSelect(getAuthLocaleSelect());
}

function nonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value : null;
}

function stripOidcErrorFromNext(next: string): string {
  const qIndex = next.indexOf("?");
  if (qIndex === -1) return next;
  const pathname = next.slice(0, qIndex);
  const params = new URLSearchParams(next.slice(qIndex + 1));
  if (!params.has("oidc_error")) return next;
  params.delete("oidc_error");
  const search = params.toString();
  return search ? `${pathname}?${search}` : pathname;
}

function isAuthViewVisible(): boolean {
  return !!app.querySelector(".page--auth");
}

function httpStatusMessage(status: unknown): string | null {
  return typeof status === "number" && Number.isFinite(status) ? `HTTP ${status}` : null;
}

function authApiErrorMessage(err: unknown, fallbackKey: string): string {
  const status = (err as { status?: unknown })?.status;
  if (status === 429) {
    return t("errors.RATE_LIMITED");
  }

  const rawApiMessage = nonEmptyString((err as { data?: { error?: { message?: unknown } } })?.data?.error?.message);
  const rawMessage = nonEmptyString((err as { message?: unknown })?.message);
  const generatedStatusMessage = httpStatusMessage(status);

  if (rawApiMessage || (rawMessage && rawMessage !== generatedStatusMessage)) {
    return apiErrorMessage(err);
  }

  return apiErrorMessage(err, { fallbackKey });
}

function setPasswordToggleState(
  inputs: HTMLInputElement[],
  button: HTMLElement | null,
  pathEl: Element | null,
  visible: boolean,
): void {
  for (const input of inputs) {
    input.type = visible ? "text" : "password";
  }
  pathEl?.setAttribute("d", visible ? PATH_HIDE : PATH_SHOW);
  const label = t(visible ? "auth.password.hide" : "auth.password.show");
  button?.setAttribute("aria-label", label);
  button?.setAttribute("title", label);
}

function twoFactorDisplayName(user: AuthUser): string {
  return user.email || user.name || t("auth.2fa.accountFallback");
}

function applyAuthViewTranslations(): void {
  if (!authViewState || !isAuthViewVisible()) return;

  syncPublicLocaleSelect(getAuthLocaleSelect());

  if (authViewState.mode === "auth") {
    const { bootstrap } = authViewState.options;
    const titleEl = document.querySelector(".panel__title");
    if (titleEl) titleEl.textContent = t(bootstrap ? "auth.bootstrap.title" : "auth.signIn.title");
    const helperEl = document.querySelector(".muted");
    if (helperEl) helperEl.textContent = t("auth.shared.helper");
    const ssoBtn = document.getElementById("authSsoBtn");
    if (ssoBtn) ssoBtn.textContent = t("auth.oidc.button");
    const divider = document.querySelector(".auth-divider span");
    if (divider) divider.textContent = t("auth.shared.or");
    const nameEl = document.getElementById("authName") as HTMLInputElement | null;
    const emailEl = document.getElementById("authEmail") as HTMLInputElement | null;
    const pwEl = document.getElementById("authPassword") as HTMLInputElement | null;
    if (nameEl) nameEl.placeholder = t("auth.fields.name.placeholder");
    if (emailEl) emailEl.placeholder = t("auth.fields.email.placeholder");
    if (pwEl) pwEl.placeholder = t("auth.fields.password.placeholder");
    if (bootstrap) {
      const bootstrapBtn = document.getElementById("bootstrapBtn");
      if (bootstrapBtn) {
        bootstrapBtn.textContent = t("auth.actions.bootstrap");
        bootstrapBtn.setAttribute("title", t("auth.bootstrap.title"));
      }
    } else {
      const loginBtn = document.getElementById("loginBtn");
      if (loginBtn) loginBtn.textContent = t("auth.actions.login");
      const forgotPasswordBtn = document.getElementById("authForgotPassword");
      if (forgotPasswordBtn) forgotPasswordBtn.textContent = t("auth.forgot.link");
    }
    setPasswordToggleState(
      pwEl ? [pwEl] : [],
      document.getElementById("authPasswordToggle") as HTMLElement | null,
      document.getElementById("authPasswordIcon")?.querySelector("path") || null,
      authViewState.passwordVisible,
    );
    return;
  }

  if (authViewState.mode === "2fa") {
    const titleEl = document.querySelector(".panel__title");
    if (titleEl) titleEl.textContent = t("auth.2fa.title");
    const helperEl = document.querySelector(".muted");
    if (helperEl) helperEl.textContent = t("auth.2fa.helper");
    const codeEl = document.getElementById("auth2FACode") as HTMLInputElement | null;
    if (codeEl) {
      codeEl.placeholder = t("auth.2fa.placeholder", {
        account: twoFactorDisplayName(authViewState.options.user),
      });
    }
    const submitBtn = document.getElementById("auth2FASubmit");
    if (submitBtn) submitBtn.textContent = t("auth.2fa.submit");
    return;
  }

  if (authViewState.mode === "forgot") {
    const titleEl = document.querySelector(".panel__title");
    if (titleEl) titleEl.textContent = t("auth.forgot.title");
    const helperEl = document.querySelector(".muted");
    if (helperEl) helperEl.textContent = t("auth.forgot.helper");
    const emailEl = document.getElementById("authForgotEmail") as HTMLInputElement | null;
    if (emailEl) {
      emailEl.placeholder = t("auth.fields.email.placeholder");
      emailEl.setAttribute("aria-label", t("auth.fields.email.placeholder"));
    }
    const backBtn = document.getElementById("authForgotBack");
    if (backBtn) backBtn.textContent = t("auth.forgot.backToSignIn");
    const submitBtn = document.getElementById("authForgotSubmit");
    if (submitBtn) submitBtn.textContent = t("auth.forgot.submit");
    return;
  }

  const titleEl = document.querySelector(".panel__title");
  if (titleEl) titleEl.textContent = t("auth.reset.title");
  const helperEl = document.querySelector(".muted");
  if (helperEl) helperEl.textContent = t("auth.reset.helper");
  const fieldLabels = document.querySelectorAll(".field__label");
  if (fieldLabels[0]) fieldLabels[0].textContent = t("auth.fields.newPassword.label");
  if (fieldLabels[1]) fieldLabels[1].textContent = t("auth.fields.confirmPassword.label");
  const newPwEl = document.getElementById("resetNewPassword") as HTMLInputElement | null;
  const confirmPwEl = document.getElementById("resetConfirmPassword") as HTMLInputElement | null;
  if (newPwEl) newPwEl.placeholder = t("auth.fields.newPassword.placeholder");
  if (confirmPwEl) confirmPwEl.placeholder = t("auth.fields.confirmPassword.placeholder");
  const submitBtn = document.getElementById("resetPasswordSubmit");
  if (submitBtn) submitBtn.textContent = t("auth.actions.resetPassword");
  setPasswordToggleState(
    newPwEl && confirmPwEl ? [newPwEl, confirmPwEl] : [],
    document.getElementById("resetPasswordToggle") as HTMLElement | null,
    document.getElementById("resetPasswordIcon")?.querySelector("path") || null,
    authViewState.passwordVisible,
  );
}

function ensureAuthLocaleListener(): void {
  if (authLocaleListenerBound) return;
  authLocaleListenerBound = true;
  const authGlobal = globalThis as AuthGlobal;
  if (authGlobal.__scrumboyAuthLocaleListener) {
    document.removeEventListener(I18N_LOCALE_CHANGED, authGlobal.__scrumboyAuthLocaleListener);
  }
  const listener: EventListener = () => {
    if (!authViewState || !isAuthViewVisible()) return;
    applyAuthViewTranslations();
  };
  authGlobal.__scrumboyAuthLocaleListener = listener;
  document.addEventListener(I18N_LOCALE_CHANGED, listener);
}

function authShellHTML(content: string, version: string): string {
  return `
    <div class="page page--auth">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
        ${renderPublicLocaleSelectHTML({ id: "authLocaleSelect", className: "auth-locale-select" })}
      </div>
      <div class="container">
        <div class="panel">
          ${content}
        </div>
      </div>
      ${version ? `<div class="app-version">v${escapeHTML(version)}</div>` : ""}
    </div>
  `;
}

function renderOidcErrorToast(): boolean {
  const params = new URLSearchParams(window.location.search);
  const oidcError = params.get("oidc_error");
  if (!oidcError) return false;

  const key = `auth.oidc.error.${oidcError}`;
  const knownKeys = new Set([
    "auth.oidc.error.state_invalid",
    "auth.oidc.error.provider",
    "auth.oidc.error.token",
    "auth.oidc.error.email",
	"auth.oidc.error.auth_time",
	"auth.oidc.error.identity_mismatch",
	"auth.oidc.error.link_rejected",
	"auth.oidc.error.link_required",
	"auth.oidc.error.session_changed",
  ]);
  showToast(t(knownKeys.has(key) ? key : "auth.oidc.error.generic"));
  params.delete("oidc_error");
  const remaining = params.toString();
  window.history.replaceState(
    {},
    "",
    remaining ? `${window.location.pathname}?${remaining}` : window.location.pathname,
  );
  return true;
}

function renderAuthView(state: AuthBaseState, options: { handleOidcError: boolean }): void {
  ensureAuthLocaleListener();
  authViewState = state;

  const { next, bootstrap, oidcEnabled, localAuthEnabled, selfServicePasswordResetEnabled } = state.options;
  const version = getAppVersion();
  const showLocalForm = localAuthEnabled;
  const showForgotPassword = !bootstrap && showLocalForm && selfServicePasswordResetEnabled;
  const ssoButtonHTML = oidcEnabled
    ? `<a class="btn btn--sso" id="authSsoBtn" href="/api/auth/oidc/login?return_to=${encodeURIComponent(next)}">${escapeHTML(t("auth.oidc.button"))}</a>`
    : "";
  const dividerHTML = oidcEnabled && showLocalForm
    ? `<div class="auth-divider"><span>${escapeHTML(t("auth.shared.or"))}</span></div>`
    : "";

  app.innerHTML = authShellHTML(`
    <div class="panel__header">
      <div class="panel__title">${escapeHTML(t(bootstrap ? "auth.bootstrap.title" : "auth.signIn.title"))}</div>
    </div>
    <div class="muted" style="margin-bottom: 12px;">
      ${escapeHTML(t("auth.shared.helper"))}
    </div>
    ${ssoButtonHTML}
    ${dividerHTML}
    ${showLocalForm ? `
      <form id="authForm" class="stack">
        ${bootstrap ? `<input class="input" id="authName" placeholder="${escapeHTML(t("auth.fields.name.placeholder"))}" maxlength="200" autocomplete="name" required />` : ``}
        <input class="input" id="authEmail" placeholder="${escapeHTML(t("auth.fields.email.placeholder"))}" maxlength="200" autocomplete="email" required />
        <div class="password-row">
          <input class="input" id="authPassword" placeholder="${escapeHTML(t("auth.fields.password.placeholder"))}" type="password" maxlength="200" autocomplete="current-password" required />
          <button type="button" class="password-toggle" id="authPasswordToggle" aria-label="${escapeHTML(t("auth.password.show"))}" title="${escapeHTML(t("auth.password.show"))}">
            <svg id="authPasswordIcon" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="${PATH_SHOW}"/></svg>
          </button>
        </div>
        <div class="row auth-actions" style="margin-top: 8px;">
          ${showForgotPassword
            ? `<button class="btn btn--ghost" type="button" id="authForgotPassword">${escapeHTML(t("auth.forgot.link"))}</button>`
            : ""}
          <div class="spacer"></div>
          ${bootstrap
            ? `<button class="btn" type="button" id="bootstrapBtn" title="${escapeHTML(t("auth.bootstrap.title"))}">${escapeHTML(t("auth.actions.bootstrap"))}</button>`
            : `<button class="btn" type="submit" id="loginBtn">${escapeHTML(t("auth.actions.login"))}</button>`}
        </div>
      </form>
    ` : ""}
  `, version);
  bindAuthLocaleSelect();

  const nameEl = document.getElementById("authName") as HTMLInputElement | null;
  const emailEl = document.getElementById("authEmail") as HTMLInputElement | null;
  const pwEl = document.getElementById("authPassword") as HTMLInputElement | null;
  const pwToggle = document.getElementById("authPasswordToggle") as HTMLElement | null;
  const pwIcon = document.getElementById("authPasswordIcon")?.querySelector("path");
  const forgotPasswordBtn = document.getElementById("authForgotPassword");

  if (nameEl) {
    nameEl.value = state.draft.name;
    nameEl.addEventListener("input", () => {
      state.draft.name = nameEl.value;
    });
  }
  if (emailEl) {
    emailEl.value = state.draft.email;
    emailEl.addEventListener("input", () => {
      state.draft.email = emailEl.value;
    });
  }
  if (pwEl) {
    pwEl.value = state.draft.password;
    setPasswordToggleState([pwEl], pwToggle, pwIcon, state.passwordVisible);
    pwEl.addEventListener("input", () => {
      state.draft.password = pwEl.value;
    });
  }

  if (pwToggle && pwEl) {
    pwToggle.addEventListener("click", () => {
      state.passwordVisible = pwEl.type === "password";
      setPasswordToggleState([pwEl], pwToggle, pwIcon, state.passwordVisible);
    });
  }

  if (options.handleOidcError && renderOidcErrorToast()) {
    state.options.next = stripOidcErrorFromNext(state.options.next);
    const ssoBtn = document.getElementById("authSsoBtn");
    if (ssoBtn) {
      ssoBtn.setAttribute(
        "href",
        `/api/auth/oidc/login?return_to=${encodeURIComponent(state.options.next)}`,
      );
    }
  }

  if (forgotPasswordBtn && emailEl && pwEl) {
    forgotPasswordBtn.addEventListener("click", () => {
      state.draft = {
        name: nameEl?.value || "",
        email: emailEl.value,
        password: pwEl.value,
      };
      renderForgotPasswordStep(state);
    });
  }

  if (bootstrap) {
    const bootstrapBtn = document.getElementById("bootstrapBtn");
    if (bootstrapBtn && emailEl && pwEl) {
      bootstrapBtn.addEventListener("click", async () => {
        state.draft = {
          name: nameEl?.value || "",
          email: emailEl.value,
          password: pwEl.value,
        };
        try {
          await apiFetch("/api/auth/bootstrap", {
            method: "POST",
            body: JSON.stringify({
              name: state.draft.name,
              email: state.draft.email,
              password: state.draft.password,
            }),
          });
          redirectAfterAuth(state.options.next || "/");
        } catch (err) {
          showToast(authApiErrorMessage(err, "auth.bootstrap.failed"));
        }
      });
    }
    return;
  }

  const authForm = document.getElementById("authForm");
  if (authForm && emailEl && pwEl) {
    authForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      state.draft = {
        name: nameEl?.value || "",
        email: emailEl.value,
        password: pwEl.value,
      };
      try {
        const res = await apiFetch<{ requires2fa?: boolean; tempToken?: string; user?: AuthUser } | null>(
          "/api/auth/login",
          { method: "POST", body: JSON.stringify({ email: state.draft.email, password: state.draft.password }) },
        );
        if (res && res.requires2fa && res.tempToken && res.user) {
          render2FAStep({ tempToken: res.tempToken, user: res.user, next: state.options.next });
          return;
        }
        redirectAfterAuth(state.options.next || "/");
      } catch (err) {
        showToast(authApiErrorMessage(err, "auth.login.failed"));
      }
    });
  }
}

export function renderAuth(opts: { next?: string; bootstrap?: boolean; oidcEnabled?: boolean; localAuthEnabled?: boolean; selfServicePasswordResetEnabled?: boolean } = {}): void {
  const next = opts.next ?? stripOidcErrorFromNext(window.location.pathname + window.location.search);
  const state: AuthBaseState = {
    mode: "auth",
    options: {
      next,
      bootstrap: !!opts.bootstrap,
      oidcEnabled: !!opts.oidcEnabled,
      localAuthEnabled: opts.localAuthEnabled !== false,
      selfServicePasswordResetEnabled: !!opts.selfServicePasswordResetEnabled,
    },
    draft: {
      name: "",
      email: "",
      password: "",
    },
    passwordVisible: false,
  };
  renderAuthView(state, { handleOidcError: true });
}

function renderForgotPasswordView(state: ForgotPasswordState): void {
  ensureAuthLocaleListener();
  authViewState = state;

  const version = getAppVersion();
  app.innerHTML = authShellHTML(`
    <div class="panel__header">
      <div class="panel__title">${escapeHTML(t("auth.forgot.title"))}</div>
    </div>
    <div class="muted" style="margin-bottom: 12px;">
      ${escapeHTML(t("auth.forgot.helper"))}
    </div>
    <form id="authForgotForm" class="stack">
      <input class="input" id="authForgotEmail" type="email" placeholder="${escapeHTML(t("auth.fields.email.placeholder"))}" aria-label="${escapeHTML(t("auth.fields.email.placeholder"))}" maxlength="200" autocomplete="email" required />
      <div class="row auth-actions" style="margin-top: 8px;">
        <button class="btn btn--ghost" type="button" id="authForgotBack">${escapeHTML(t("auth.forgot.backToSignIn"))}</button>
        <div class="spacer"></div>
        <button class="btn" type="submit" id="authForgotSubmit">${escapeHTML(t("auth.forgot.submit"))}</button>
      </div>
    </form>
  `, version);
  bindAuthLocaleSelect();

  const form = document.getElementById("authForgotForm");
  const emailEl = document.getElementById("authForgotEmail") as HTMLInputElement | null;
  const backBtn = document.getElementById("authForgotBack") as HTMLButtonElement | null;
  const submitBtn = document.getElementById("authForgotSubmit") as HTMLButtonElement | null;

  if (emailEl) {
    emailEl.value = state.draft.email;
    emailEl.addEventListener("input", () => {
      state.draft.email = emailEl.value;
    });
    emailEl.focus();
  }

  if (backBtn && emailEl) {
    backBtn.addEventListener("click", () => {
      if (state.submitting) return;
      state.options.authState.draft.email = emailEl.value;
      renderAuthView(state.options.authState, { handleOidcError: false });
    });
  }

  if (form && emailEl && submitBtn) {
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (state.submitting) return;

      state.draft.email = emailEl.value.trim();
      state.options.authState.draft.email = state.draft.email;
      state.submitting = true;
      submitBtn.disabled = true;
      if (backBtn) backBtn.disabled = true;

      try {
        await apiFetch("/api/auth/request-password-reset", {
          method: "POST",
          body: JSON.stringify({ email: state.draft.email }),
        });
        if (authViewState !== state || !isAuthViewVisible()) return;
        showToast(t("auth.forgot.success"));
        renderAuthView(state.options.authState, { handleOidcError: false });
      } catch (err) {
        if (authViewState !== state || !isAuthViewVisible()) return;
        state.submitting = false;
        submitBtn.disabled = false;
        if (backBtn) backBtn.disabled = false;
        showToast(authApiErrorMessage(err, "auth.forgot.failed"));
      }
    });
  }
}

function renderForgotPasswordStep(authState: AuthBaseState): void {
  const state: ForgotPasswordState = {
    mode: "forgot",
    options: {
      authState,
    },
    draft: {
      email: authState.draft.email,
    },
    submitting: false,
  };
  renderForgotPasswordView(state);
}

function render2FAView(state: TwoFactorState): void {
  ensureAuthLocaleListener();
  authViewState = state;

  const { tempToken, user } = state.options;
  const displayName = twoFactorDisplayName(user);
  const version = getAppVersion();

  app.innerHTML = authShellHTML(`
    <div class="panel__header">
      <div class="panel__title">${escapeHTML(t("auth.2fa.title"))}</div>
    </div>
    <div class="muted" style="margin-bottom: 12px;">
      ${escapeHTML(t("auth.2fa.helper"))}
    </div>
    <form id="auth2FAForm" class="stack">
      <input class="input" id="auth2FACode" placeholder="${escapeHTML(t("auth.2fa.placeholder", { account: displayName }))}" maxlength="20" autocomplete="one-time-code" required />
      <div class="row" style="margin-top: 8px;">
        <div class="spacer"></div>
        <button class="btn" type="submit" id="auth2FASubmit">${escapeHTML(t("auth.2fa.submit"))}</button>
      </div>
    </form>
  `, version);
  bindAuthLocaleSelect();

  const form = document.getElementById("auth2FAForm");
  const codeEl = document.getElementById("auth2FACode") as HTMLInputElement | null;
  if (codeEl) {
    codeEl.value = state.draft.code;
    codeEl.addEventListener("input", () => {
      state.draft.code = codeEl.value;
    });
  }

  if (form && codeEl) {
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      state.draft.code = codeEl.value.trim();
      try {
        await apiFetch("/api/auth/login/2fa", {
          method: "POST",
          body: JSON.stringify({ tempToken, code: state.draft.code }),
        });
        redirectAfterAuth(state.options.next || "/");
      } catch (err) {
        showToast(authApiErrorMessage(err, "auth.2fa.failed"));
      }
    });
  }
}

function render2FAStep(opts: { tempToken: string; user: AuthUser; next: string }): void {
  const state: TwoFactorState = {
    mode: "2fa",
    options: opts,
    draft: {
      code: "",
    },
  };
  render2FAView(state);
}

function renderResetPasswordView(state: ResetPasswordState): void {
  ensureAuthLocaleListener();
  authViewState = state;

  const version = getAppVersion();
  app.innerHTML = authShellHTML(`
    <div class="panel__header">
      <div class="panel__title">${escapeHTML(t("auth.reset.title"))}</div>
    </div>
    <div class="muted" style="margin-bottom: 12px;">
      ${escapeHTML(t("auth.reset.helper"))}
    </div>
    <form id="resetPasswordForm" class="stack">
      <label class="field">
        <div class="field__label">${escapeHTML(t("auth.fields.newPassword.label"))}</div>
        <div class="password-row">
          <input class="input" id="resetNewPassword" type="password" placeholder="${escapeHTML(t("auth.fields.newPassword.placeholder"))}" maxlength="200" autocomplete="new-password" required />
          <button type="button" class="password-toggle" id="resetPasswordToggle" aria-label="${escapeHTML(t("auth.password.show"))}" title="${escapeHTML(t("auth.password.show"))}">
            <svg id="resetPasswordIcon" width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="${PATH_SHOW}"/></svg>
          </button>
        </div>
      </label>
      <label class="field">
        <div class="field__label">${escapeHTML(t("auth.fields.confirmPassword.label"))}</div>
        <input class="input" id="resetConfirmPassword" type="password" placeholder="${escapeHTML(t("auth.fields.confirmPassword.placeholder"))}" maxlength="200" autocomplete="new-password" required />
      </label>
      <div class="row" style="margin-top: 8px;">
        <div class="spacer"></div>
        <button class="btn" type="submit" id="resetPasswordSubmit">${escapeHTML(t("auth.actions.resetPassword"))}</button>
      </div>
    </form>
  `, version);
  bindAuthLocaleSelect();

  const form = document.getElementById("resetPasswordForm");
  const newPwEl = document.getElementById("resetNewPassword") as HTMLInputElement | null;
  const confirmPwEl = document.getElementById("resetConfirmPassword") as HTMLInputElement | null;
  const pwToggle = document.getElementById("resetPasswordToggle") as HTMLElement | null;
  const pwIcon = document.getElementById("resetPasswordIcon")?.querySelector("path");

  if (newPwEl && confirmPwEl) {
    newPwEl.value = state.draft.newPassword;
    confirmPwEl.value = state.draft.confirmPassword;
    setPasswordToggleState([newPwEl, confirmPwEl], pwToggle, pwIcon, state.passwordVisible);
    newPwEl.addEventListener("input", () => {
      state.draft.newPassword = newPwEl.value;
    });
    confirmPwEl.addEventListener("input", () => {
      state.draft.confirmPassword = confirmPwEl.value;
    });
  }

  if (pwToggle && newPwEl && confirmPwEl) {
    pwToggle.addEventListener("click", () => {
      state.passwordVisible = newPwEl.type === "password";
      setPasswordToggleState([newPwEl, confirmPwEl], pwToggle, pwIcon, state.passwordVisible);
    });
  }

  if (form && newPwEl && confirmPwEl) {
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      state.draft = {
        newPassword: newPwEl.value,
        confirmPassword: confirmPwEl.value,
      };
      if (state.draft.newPassword !== state.draft.confirmPassword) {
        showToast(t("auth.reset.passwordsMismatch"));
        return;
      }
      try {
        await apiFetch("/api/auth/reset-password", {
          method: "POST",
          body: JSON.stringify({ token: state.options.token, new_password: state.draft.newPassword }),
        });
        showToast(t("auth.reset.success"));
        window.location.href = "/";
      } catch (err) {
        showToast(authApiErrorMessage(err, "auth.reset.invalidOrExpiredToken"));
      }
    });
  }
}

export function renderResetPassword(token?: string): void {
  const urlObj = new URL(window.location.href);
  const tokenFromUrl = token ?? urlObj.searchParams.get("token") ?? "";

  if (!tokenFromUrl) {
    authViewState = null;
    showToast(t("auth.reset.invalidLink"));
    window.location.href = "/";
    return;
  }

  const state: ResetPasswordState = {
    mode: "reset",
    options: {
      token: tokenFromUrl,
    },
    draft: {
      newPassword: "",
      confirmPassword: "",
    },
    passwordVisible: false,
  };
  renderResetPasswordView(state);
}
