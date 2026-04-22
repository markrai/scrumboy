// @vitest-environment happy-dom
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { initModalOutsideClickClose } from "./modal-outside-click.js";

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
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
}

describe("initModalOutsideClickClose", () => {
  beforeAll(() => {
    installDialogPolyfill();
    initModalOutsideClickClose();
  });

  beforeEach(() => {
    document.body.innerHTML = "";
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("does not auto-close a dialog that opened mid-gesture (new topmost vs pointerdown)", () => {
    const base = document.createElement("dialog");
    base.id = "baseWallLike";
    base.textContent = "no dialog__form";
    document.body.appendChild(base);
    base.showModal();

    const outside = document.createElement("div");
    outside.id = "outsideTarget";
    document.body.appendChild(outside);

    outside.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));

    const overlay = document.createElement("dialog");
    overlay.id = "overlayConfirm";
    overlay.innerHTML = `<div class="dialog__form"><p>Confirm body</p></div>`;
    document.body.appendChild(overlay);
    overlay.showModal();

    outside.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(overlay.hasAttribute("open")).toBe(true);
  });

  it("still closes the topmost dialog on outside click when it was already top at pointerdown", () => {
    const dlg = document.createElement("dialog");
    dlg.innerHTML = `<div class="dialog__form"><p>Inside</p></div>`;
    document.body.appendChild(dlg);
    dlg.showModal();

    const outside = document.createElement("div");
    document.body.appendChild(outside);

    outside.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    outside.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(dlg.hasAttribute("open")).toBe(false);
  });
});
