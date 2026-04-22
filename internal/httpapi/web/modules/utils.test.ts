// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from "vitest";
import { confirmDelete } from "./utils.js";

function installDialogPolyfill(): void {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true;
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = false;
    },
  });
}

describe("confirmDelete", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    installDialogPolyfill();
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
