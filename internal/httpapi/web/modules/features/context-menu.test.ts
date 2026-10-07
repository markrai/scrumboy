// @vitest-environment happy-dom
import { beforeAll, beforeEach, describe, expect, it } from "vitest";

import { setupContextMenuCloseHandler } from "./context-menu.js";

function visibleMenu(): HTMLElement {
  document.body.innerHTML = `<div id="contextMenu" class="context-menu" style="display: block;"></div>`;
  return document.getElementById("contextMenu") as HTMLElement;
}

function dispatchEscape(extra?: KeyboardEventInit): void {
  document.dispatchEvent(
    new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true, ...extra }),
  );
}

function observeClaim(): { claimed: () => boolean } {
  let claimed = false;
  document.addEventListener("keydown", (ev) => {
    claimed = ev.defaultPrevented;
  });
  return { claimed: () => claimed };
}

describe("board context menu ESC dismissal", () => {
  beforeAll(() => {
    setupContextMenuCloseHandler();
  });

  beforeEach(() => {
    document.body.innerHTML = "";
  });

  it("hides a visible menu when Escape is pressed", () => {
    const menu = visibleMenu();
    const claim = observeClaim();

    dispatchEscape();

    expect(menu.style.display).toBe("none");
    expect(claim.claimed()).toBe(true);
  });

  it("leaves a visible menu open for non-Escape keys", () => {
    const menu = visibleMenu();
    const claim = observeClaim();

    dispatchEscape({ key: "Enter" });

    expect(menu.style.display).toBe("block");
    expect(claim.claimed()).toBe(false);
  });

  it("ignores repeat Escape presses", () => {
    const menu = visibleMenu();
    const claim = observeClaim();

    dispatchEscape({ repeat: true });

    expect(menu.style.display).toBe("block");
    expect(claim.claimed()).toBe(false);
  });

  it("ignores Escape already consumed by another handler", () => {
    const menu = visibleMenu();
    const ev = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    ev.preventDefault();

    document.dispatchEvent(ev);

    expect(menu.style.display).toBe("block");
  });

  it("leaves an already-hidden menu alone without claiming the event", () => {
    document.body.innerHTML = `<div id="contextMenu" class="context-menu" style="display: none;"></div>`;
    const claim = observeClaim();

    dispatchEscape();

    expect((document.getElementById("contextMenu") as HTMLElement).style.display).toBe("none");
    expect(claim.claimed()).toBe(false);
  });

  it("lets native dialog cancel handling win", () => {
    const menu = visibleMenu();
    const claim = observeClaim();
    const dialog = document.createElement("dialog");
    dialog.setAttribute("open", "");
    document.body.appendChild(dialog);

    dispatchEscape();

    expect(menu.style.display).toBe("block");
    expect(claim.claimed()).toBe(false);
  });
});
