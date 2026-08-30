// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from "vitest";
import { initI18n, resetI18nForTests } from "./i18n/index.js";
import { confirmDelete, showConfirmDialog, showPromptDialog } from "./utils.js";

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
