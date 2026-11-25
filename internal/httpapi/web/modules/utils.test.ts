// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, resetI18nForTests } from "./i18n/index.js";
import { confirmDelete, showConfirmDialog, showPromptDialog, showToast } from "./utils.js";

// dom/elements.ts looks up #toast when it is first imported, before any test can add markup.
const toastEl = vi.hoisted(() => {
  const el = document.createElement("div");
  el.id = "toast";
  el.className = "toast";
  el.setAttribute("popover", "manual");
  return el;
});

vi.mock("./dom/elements.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./dom/elements.js")>()),
  toast: toastEl,
}));

const enCatalog = {
  "common.cancel": "Cancel",
  "common.close": "Close",
  "common.confirm": "Confirm",
  "common.delete": "Delete",
  "common.prompt": "Prompt",
  "common.save": "Save",
  "common.value": "Value",
};

const pseudoCatalog = {
  "common.cancel": "[!! Cancel !!]",
  "common.close": "[!! Close !!]",
  "common.confirm": "[!! Confirm !!]",
  "common.delete": "[!! Delete !!]",
  "common.prompt": "[!! Prompt !!]",
  "common.save": "[!! Save !!]",
  "common.value": "[!! Value !!]",
};

function installDialogPolyfill(): void {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      if (!this.hasAttribute("open")) return;
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
}

describe("confirmDelete", () => {
  beforeEach(async () => {
    document.body.innerHTML = "";
    installDialogPolyfill();
    resetI18nForTests();
    await initI18n({
      locale: "en",
      loadLocale: async (locale) => locale === "pseudo" ? pseudoCatalog : enCatalog,
    });
  });

  it("uses default delete title and label", async () => {
    const resultPromise = confirmDelete("Delete this project?");
    const title = document.querySelector(".dialog__title");
    const confirmBtn = document.getElementById("confirmDialogConfirm");
    const message = document.querySelector(".dialog__content p");
    if (!(title instanceof HTMLElement)) throw new Error("missing confirm title");
    if (!(confirmBtn instanceof HTMLButtonElement)) throw new Error("missing confirm button");
    if (!(message instanceof HTMLElement)) throw new Error("missing confirm message");

    expect(title.textContent).toBe("Delete");
    expect(confirmBtn.textContent).toBe("Delete");
    expect(message.textContent).toBe("Delete this project?");

    confirmBtn.click();
    await expect(resultPromise).resolves.toBe(true);
  });

  it("supports custom title/label and resolves false on cancel", async () => {
    const resultPromise = confirmDelete({
      message: "Delete this user?",
      title: "Delete User",
      confirmLabel: "Yes, delete",
    });
    const title = document.querySelector(".dialog__title");
    const confirmBtn = document.getElementById("confirmDialogConfirm");
    const cancelBtn = document.getElementById("confirmDialogCancel");
    if (!(title instanceof HTMLElement)) throw new Error("missing custom title");
    if (!(confirmBtn instanceof HTMLButtonElement)) throw new Error("missing custom confirm button");
    if (!(cancelBtn instanceof HTMLButtonElement)) throw new Error("missing cancel button");

    expect(title.textContent).toBe("Delete User");
    expect(confirmBtn.textContent).toBe("Yes, delete");

    cancelBtn.click();
    await expect(resultPromise).resolves.toBe(false);
  });
});

describe("showConfirmDialog lifecycle", () => {
  beforeEach(async () => {
    document.body.innerHTML = "";
    installDialogPolyfill();
    resetI18nForTests();
    await initI18n({
      locale: "en",
      loadLocale: async (locale) => locale === "pseudo" ? pseudoCatalog : enCatalog,
    });
  });

  it.each([
    ["default", false, false],
    ["success", true, false],
    ["danger", false, true],
  ] as const)("applies the %s semantic confirmation tone", async (tone, success, danger) => {
    const resultPromise = showConfirmDialog("Proceed?", "Confirm", "Continue", tone);
    const confirmBtn = document.getElementById("confirmDialogConfirm") as HTMLButtonElement;

    expect(confirmBtn.classList.contains("btn--success")).toBe(success);
    expect(confirmBtn.classList.contains("btn--danger")).toBe(danger);

    confirmBtn.click();
    await expect(resultPromise).resolves.toBe(true);
  });

  it("resolves true exactly once even on double-click confirm", async () => {
    const resultPromise = showConfirmDialog("Proceed?");
    const confirmBtn = document.getElementById("confirmDialogConfirm") as HTMLButtonElement;
    confirmBtn.click();
    confirmBtn.click();
    await expect(resultPromise).resolves.toBe(true);
    // Dialog DOM cleaned up after settle.
    expect(document.getElementById("confirmDialogConfirm")).toBeNull();
  });

  it("uses active-locale defaults for shared dialog chrome", async () => {
    resetI18nForTests();
    await initI18n({
      locale: "pseudo",
      loadLocale: async (locale) => locale === "pseudo" ? pseudoCatalog : enCatalog,
    });

    const resultPromise = showConfirmDialog("Proceed?");
    const title = document.querySelector(".dialog__title");
    const cancelBtn = document.getElementById("confirmDialogCancel");
    const confirmBtn = document.getElementById("confirmDialogConfirm");
    if (!(title instanceof HTMLElement)) throw new Error("missing confirm title");
    if (!(cancelBtn instanceof HTMLButtonElement)) throw new Error("missing cancel button");
    if (!(confirmBtn instanceof HTMLButtonElement)) throw new Error("missing confirm button");

    expect(title.textContent).toBe("[!! Confirm !!]");
    expect(cancelBtn.textContent).toBe("[!! Cancel !!]");
    expect(confirmBtn.textContent).toBe("[!! Confirm !!]");

    cancelBtn.click();
    await expect(resultPromise).resolves.toBe(false);
  });

  it("resolves false when the close (x) button is pressed", async () => {
    const resultPromise = showConfirmDialog("Proceed?");
    const closeBtn = document.getElementById("confirmDialogClose") as HTMLButtonElement;
    closeBtn.click();
    await expect(resultPromise).resolves.toBe(false);
  });

  it("resolves false when the dialog is closed externally (regression for drag-to-trash)", async () => {
    const resultPromise = showConfirmDialog("Proceed?");
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    // Simulate a global outside-click helper calling close() without ever
    // interacting with any button inside the confirm dialog.
    dialog.close();
    await expect(resultPromise).resolves.toBe(false);
  });

  it("resolves false once and ignores subsequent close() calls", async () => {
    const resultPromise = showConfirmDialog("Proceed?");
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    dialog.close();
    // Second programmatic close should not dispatch a second event (polyfill
    // guards against double-close) and the Promise must already be settled.
    dialog.close();
    await expect(resultPromise).resolves.toBe(false);
  });

  it("resolves false when the native cancel (ESC) event fires", async () => {
    const resultPromise = showConfirmDialog("Proceed?");
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    dialog.dispatchEvent(new Event("cancel"));
    dialog.close();
    await expect(resultPromise).resolves.toBe(false);
  });

  it("rejects when showModal throws", async () => {
    HTMLDialogElement.prototype.showModal = function brokenShowModal(): void {
      throw new Error("showModal failed");
    };

    await expect(showConfirmDialog("Proceed?")).rejects.toThrow("showModal failed");
    expect(document.querySelector("dialog")).toBeNull();
  });
});

describe("showPromptDialog lifecycle", () => {
  beforeEach(async () => {
    document.body.innerHTML = "";
    installDialogPolyfill();
    resetI18nForTests();
    await initI18n({
      locale: "en",
      loadLocale: async (locale) => locale === "pseudo" ? pseudoCatalog : enCatalog,
    });
  });

  it("resolves the entered value on submit", async () => {
    const resultPromise = showPromptDialog({
      title: "Rename Project",
      label: "Project Name",
      initialValue: "Alpha",
      confirmLabel: "Rename",
      placeholder: "Project name",
      maxLength: 200,
    });
    const title = document.querySelector(".dialog__title");
    const input = document.getElementById("promptDialogInput");
    const confirmBtn = document.getElementById("promptDialogConfirm");
    if (!(title instanceof HTMLElement)) throw new Error("missing prompt title");
    if (!(input instanceof HTMLInputElement)) throw new Error("missing prompt input");
    if (!(confirmBtn instanceof HTMLButtonElement)) throw new Error("missing prompt confirm button");

    expect(title.textContent).toBe("Rename Project");
    expect(input.value).toBe("Alpha");
    input.value = "Beta";
    confirmBtn.click();

    await expect(resultPromise).resolves.toBe("Beta");
  });

  it("resolves null when the cancel button is pressed", async () => {
    const resultPromise = showPromptDialog();
    const cancelBtn = document.getElementById("promptDialogCancel");
    if (!(cancelBtn instanceof HTMLButtonElement)) throw new Error("missing prompt cancel button");

    cancelBtn.click();
    await expect(resultPromise).resolves.toBeNull();
  });

  it("resolves null when the close (x) button is pressed", async () => {
    const resultPromise = showPromptDialog();
    const closeBtn = document.getElementById("promptDialogClose");
    if (!(closeBtn instanceof HTMLButtonElement)) throw new Error("missing prompt close button");

    closeBtn.click();
    await expect(resultPromise).resolves.toBeNull();
  });

  it("resolves null when the native cancel (ESC) event fires", async () => {
    const resultPromise = showPromptDialog();
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    dialog.dispatchEvent(new Event("cancel"));
    dialog.close();
    await expect(resultPromise).resolves.toBeNull();
  });

  it("resolves null when the dialog is closed externally", async () => {
    const resultPromise = showPromptDialog();
    const dialog = document.querySelector("dialog") as HTMLDialogElement;
    dialog.close();
    await expect(resultPromise).resolves.toBeNull();
  });

  it("rejects when showModal throws", async () => {
    HTMLDialogElement.prototype.showModal = function brokenShowModal(): void {
      throw new Error("showModal failed");
    };

    await expect(showPromptDialog()).rejects.toThrow("showModal failed");
    expect(document.querySelector("dialog")).toBeNull();
  });
});

describe("showToast", () => {
  type PopoverStub = { open: boolean; showPopover: ReturnType<typeof vi.fn>; hidePopover: ReturnType<typeof vi.fn> };

  // happy-dom has no Popover API. Emulate the parts showToast relies on: showPopover() throws when the
  // popover is already open (as browsers may), hidePopover(), and the :popover-open pseudo-class.
  function installPopoverStub(showPopoverImpl?: () => void): PopoverStub {
    const stub: PopoverStub = {
      open: false,
      showPopover: vi.fn(showPopoverImpl ?? (() => {
        if (stub.open) throw new DOMException("Popover is already open", "InvalidStateError");
        stub.open = true;
      })),
      hidePopover: vi.fn(() => {
        stub.open = false;
      }),
    };
    const nativeMatches = HTMLElement.prototype.matches;
    Object.assign(toastEl, {
      showPopover: stub.showPopover,
      hidePopover: stub.hidePopover,
      matches(selector: string): boolean {
        return selector === ":popover-open" ? stub.open : nativeMatches.call(this, selector);
      },
    });
    return stub;
  }

  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = "";
    document.body.appendChild(toastEl);
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    for (const key of ["showPopover", "hidePopover", "matches"]) {
      delete (toastEl as unknown as Record<string, unknown>)[key];
    }
    toastEl.textContent = "";
    toastEl.classList.remove("toast--show");
  });

  it("shows a second toast for its own full lifetime", () => {
    const popover = installPopoverStub();

    showToast("First");
    expect(toastEl.textContent).toBe("First");
    expect(toastEl.classList.contains("toast--show")).toBe(true);
    expect(popover.open).toBe(true);

    vi.advanceTimersByTime(1500);
    expect(() => showToast("Second")).not.toThrow();
    expect(toastEl.textContent).toBe("Second");
    expect(popover.open).toBe(true);
    // Re-shown so it is placed above anything opened in the top layer since the first toast.
    expect(popover.showPopover).toHaveBeenCalledTimes(2);

    // The first toast's timer would have fired here.
    vi.advanceTimersByTime(1000);
    expect(toastEl.textContent).toBe("Second");
    expect(toastEl.classList.contains("toast--show")).toBe(true);
    expect(popover.open).toBe(true);

    vi.advanceTimersByTime(1499);
    expect(toastEl.classList.contains("toast--show")).toBe(true);

    vi.advanceTimersByTime(1);
    expect(toastEl.classList.contains("toast--show")).toBe(false);
    expect(popover.open).toBe(false);
  });

  it("falls back to the plain toast when showPopover fails", () => {
    installPopoverStub(() => {
      throw new DOMException("Not supported", "NotSupportedError");
    });

    expect(() => showToast("Tag rejected")).not.toThrow();
    expect(toastEl.textContent).toBe("Tag rejected");
    expect(toastEl.classList.contains("toast--show")).toBe(true);

    vi.advanceTimersByTime(2500);
    expect(toastEl.classList.contains("toast--show")).toBe(false);
  });

  it("works without the Popover API", () => {
    expect(() => showToast("Saved")).not.toThrow();
    expect(toastEl.textContent).toBe("Saved");
    expect(toastEl.classList.contains("toast--show")).toBe(true);

    vi.advanceTimersByTime(2500);
    expect(toastEl.classList.contains("toast--show")).toBe(false);
  });
});

describe("sanitizePostAuthNext", () => {
  it("preserves valid internal paths and queries", async () => {
    const { sanitizePostAuthNext } = await import("./utils.js");
    expect(sanitizePostAuthNext("/")).toBe("/");
    expect(sanitizePostAuthNext("/dashboard")).toBe("/dashboard");
    expect(sanitizePostAuthNext("/board-slug")).toBe("/board-slug");
    expect(sanitizePostAuthNext("/board-slug/t/123")).toBe("/board-slug/t/123");
    expect(sanitizePostAuthNext("/board-slug?tag=x")).toBe("/board-slug?tag=x");
    expect(sanitizePostAuthNext("/dashboard?tab=mine&view=1")).toBe("/dashboard?tab=mine&view=1");
    expect(sanitizePostAuthNext("  /projects?from=auth  ")).toBe("/projects?from=auth");
  });

  it("falls back to / for absolute, protocol-relative, and dangerous schemes", async () => {
    const { sanitizePostAuthNext } = await import("./utils.js");
    expect(sanitizePostAuthNext("https://example.com")).toBe("/");
    expect(sanitizePostAuthNext("http://example.com")).toBe("/");
    expect(sanitizePostAuthNext("//example.com")).toBe("/");
    expect(sanitizePostAuthNext("//example.com/path")).toBe("/");
    expect(sanitizePostAuthNext("javascript:alert(1)")).toBe("/");
    expect(sanitizePostAuthNext("data:text/html,hi")).toBe("/");
    expect(sanitizePostAuthNext("")).toBe("/");
    expect(sanitizePostAuthNext(null)).toBe("/");
    expect(sanitizePostAuthNext(undefined)).toBe("/");
    expect(sanitizePostAuthNext("dashboard")).toBe("/");
  });

  it("rejects backslash, encoding, traversal, and fragment escape variants", async () => {
    const { sanitizePostAuthNext } = await import("./utils.js");
    expect(sanitizePostAuthNext("/foo\\bar")).toBe("/");
    expect(sanitizePostAuthNext("/foo\nbar")).toBe("/");
    expect(sanitizePostAuthNext("/foo\rbar")).toBe("/");
    expect(sanitizePostAuthNext("/foo\0bar")).toBe("/");
    expect(sanitizePostAuthNext("/foo#bar")).toBe("/");
    expect(sanitizePostAuthNext("%2f%2f")).toBe("/");
    expect(sanitizePostAuthNext("/%2f%2fevil.example/path")).toBe("/");
    expect(sanitizePostAuthNext("%2e%2e")).toBe("/");
    expect(sanitizePostAuthNext("/../../etc/passwd")).toBe("/");
    expect(sanitizePostAuthNext("/foo/./bar")).toBe("/");
    expect(sanitizePostAuthNext("/foo/../bar")).toBe("/");
    expect(sanitizePostAuthNext("/foo://bar")).toBe("/");
  });

  it("preserves encoded query values that look external without treating them as the destination", async () => {
    const { sanitizePostAuthNext } = await import("./utils.js");
    const oauthNext =
      "/oauth/authorize?response_type=code&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&return_to=https://attacker.example&state=a%2Bb";
    expect(sanitizePostAuthNext(oauthNext)).toBe(oauthNext);
  });
});

describe("redirectAfterAuth", () => {
  it("navigates only after sanitizing next (password/2FA post-auth boundary)", async () => {
    const replaceSpy = vi.spyOn(window.location, "replace").mockImplementation(() => {});
    const { redirectAfterAuth } = await import("./utils.js");

    redirectAfterAuth("/dashboard?tab=mine");
    expect(replaceSpy).toHaveBeenLastCalledWith(expect.stringMatching(/^\/dashboard\?tab=mine&_=\d+$/));

    redirectAfterAuth("https://evil.example/phish");
    expect(replaceSpy).toHaveBeenLastCalledWith(expect.stringMatching(/^\/\?_=\d+$/));

    redirectAfterAuth("//evil.example");
    expect(replaceSpy).toHaveBeenLastCalledWith(expect.stringMatching(/^\/\?_=\d+$/));

    redirectAfterAuth("javascript:alert(1)");
    expect(replaceSpy).toHaveBeenLastCalledWith(expect.stringMatching(/^\/\?_=\d+$/));

    replaceSpy.mockRestore();
  });
});
