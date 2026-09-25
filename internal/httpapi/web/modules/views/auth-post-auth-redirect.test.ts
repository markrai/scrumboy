// @vitest-environment happy-dom
/**
 * Proves password/2FA completion navigates through real redirectAfterAuth,
 * which sanitizes next at the final location.replace boundary.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());

vi.mock("../dom/elements.js", () => ({
  app: document.body,
}));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../utils.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../utils.js")>();
  return {
    ...actual,
    showToast: showToastMock,
    getAppVersion: () => "",
  };
});

const enCatalog = {
  "auth.2fa.accountFallback": "your account",
  "auth.2fa.failed": "Verification failed.",
  "auth.2fa.helper": "Enter the 6-digit code from your authenticator app, or a recovery code.",
  "auth.2fa.placeholder": "Code for {account}",
  "auth.2fa.submit": "Verify",
  "auth.2fa.title": "Two-factor authentication",
  "auth.actions.bootstrap": "Bootstrap",
  "auth.actions.login": "Sign in with your Scrumboy password",
  "auth.actions.resetPassword": "Reset Password",
  "auth.bootstrap.failed": "Setup failed.",
  "auth.bootstrap.title": "First-time setup",
  "auth.fields.confirmPassword.label": "Confirm password",
  "auth.fields.confirmPassword.placeholder": "Confirm new password",
  "auth.fields.email.placeholder": "Email",
  "auth.fields.name.placeholder": "Name",
  "auth.fields.newPassword.label": "New password",
  "auth.fields.newPassword.placeholder": "Min 8 characters",
  "auth.fields.password.placeholder": "Password",
  "auth.forgot.backToSignIn": "Back to sign in",
  "auth.forgot.failed": "Could not request a password reset.",
  "auth.forgot.helper": "Enter your email.",
  "auth.forgot.link": "Forgot your Scrumboy password?",
  "auth.forgot.submit": "Send reset link",
  "auth.forgot.success": "If an account exists for that email address, a password reset email has been sent.",
  "auth.forgot.title": "Reset your password",
  "auth.login.failed": "Login failed.",
  "auth.oidc.button": "Continue with SSO",
  "auth.oidc.error.email": "A verified email address is required.",
  "auth.oidc.error.domain_not_allowed": "Sign-up is restricted.",
  "auth.oidc.error.generic": "Authentication failed.",
  "auth.oidc.error.provider": "The identity provider returned an error.",
  "auth.oidc.error.state_invalid": "Login session expired or invalid. Please try again.",
  "auth.oidc.error.token": "Authentication failed. Please try again.",
  "auth.password.hide": "Hide password",
  "auth.password.show": "Show password",
  "auth.reset.helper": "Enter your new password.",
  "auth.reset.invalidLink": "Invalid or missing reset link",
  "auth.reset.invalidOrExpiredToken": "Invalid or expired reset token",
  "auth.reset.passwordsMismatch": "Passwords do not match",
  "auth.reset.success": "Password reset successfully. Please log in.",
  "auth.reset.title": "Reset Password",
  "auth.shared.helper": "Authentication is enabled.",
  "auth.shared.or": "or",
  "auth.signIn.title": "Sign in",
  "errors.RATE_LIMITED": "Too many attempts. Try again later.",
  "errors.UNAUTHORIZED": "Unauthorized",
  "errors.generic": "Something went wrong.",
  "errors.httpStatus": "HTTP {status}",
  "settings.language.selectLabel": "Language",
} as const;

vi.mock("../i18n/index.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../i18n/index.js")>();
  return {
    ...actual,
    t: (key: string) => (enCatalog as Record<string, string>)[key] ?? key,
    hasI18nKey: (key: string) => key in enCatalog,
  };
});

async function flushPromises(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

describe("auth post-auth redirect sanitizer boundary", () => {
  let replaceSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = "";
    apiFetchMock.mockReset();
    showToastMock.mockReset();
    window.history.replaceState({}, "", "/");
    replaceSpy = vi.spyOn(window.location, "replace").mockImplementation(() => {});
  });

  afterEach(() => {
    replaceSpy.mockRestore();
  });

  it("password login uses redirectAfterAuth sanitizer for malicious next", async () => {
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({});
    auth.renderAuth({
      next: "https://evil.example/phish",
      oidcEnabled: false,
      localAuthEnabled: true,
    });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(replaceSpy).toHaveBeenCalledWith(expect.stringMatching(/^\/\?_=\d+$/));
  });

  it("password login preserves a valid internal next through redirectAfterAuth", async () => {
    const auth = await import("./auth.js");
    apiFetchMock.mockResolvedValueOnce({});
    auth.renderAuth({
      next: "/dashboard?tab=mine",
      oidcEnabled: false,
      localAuthEnabled: true,
    });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(replaceSpy).toHaveBeenCalledWith(expect.stringMatching(/^\/dashboard\?tab=mine&_=\d+$/));
  });

  it("2FA completion uses redirectAfterAuth sanitizer for protocol-relative next", async () => {
    const auth = await import("./auth.js");
    apiFetchMock
      .mockResolvedValueOnce({
        requires2fa: true,
        tempToken: "temp-token",
        user: { id: 1, email: "user@example.com", name: "User" },
      })
      .mockResolvedValueOnce({});
    auth.renderAuth({
      next: "//evil.example/path",
      oidcEnabled: false,
      localAuthEnabled: true,
    });
    (document.getElementById("authEmail") as HTMLInputElement).value = "user@example.com";
    (document.getElementById("authPassword") as HTMLInputElement).value = "password";
    document.getElementById("authForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    (document.getElementById("auth2FACode") as HTMLInputElement).value = "123456";
    document.getElementById("auth2FAForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(replaceSpy).toHaveBeenCalledWith(expect.stringMatching(/^\/\?_=\d+$/));
  });
});
