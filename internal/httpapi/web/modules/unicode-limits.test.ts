// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  bindCodePointLimit,
  bindCodePointLimitsIn,
  codePointLength,
  exceedsCodePointLimit,
  getCodePointLimitHandle,
  reportCodePointLimitsIn,
  truncateCodePoints,
} from "./unicode-limits.js";
import { initI18n, resetI18nForTests } from "./i18n/index.js";

describe("unicode-limits", () => {
  const cleanups: Array<() => void> = [];
  beforeEach(async () => {
    resetI18nForTests();
    await initI18n({
      locale: "en",
      loadLocale: async () => ({
        "validation.codePointLimit": "Use at most {max} characters.",
      }),
    });
  });
  afterEach(() => {
    while (cleanups.length) cleanups.pop()?.();
    document.body.replaceChildren();
    resetI18nForTests();
  });

  it("counts ASCII, CJK, supplementary-plane emoji, combining marks, and mixed text", () => {
    expect(codePointLength("abc")).toBe(3);
    expect(codePointLength("漢字漢")).toBe(3);
    expect(codePointLength("😀")).toBe(1);
    expect(codePointLength("a😀b")).toBe(3);
    expect(codePointLength("e\u0301")).toBe(2);
    expect(codePointLength("名😀e\u0301")).toBe(4);
    expect("😀".length).toBe(2);
    expect(codePointLength("😀")).toBe(1);
  });

  it("exceedsCodePointLimit uses code points, not UTF-16 units", () => {
    expect(exceedsCodePointLimit("😀😀", 2)).toBe(false);
    expect(exceedsCodePointLimit("😀😀😀", 2)).toBe(true);
    expect(exceedsCodePointLimit(stringsOf("漢", 200), 200)).toBe(false);
    expect(exceedsCodePointLimit(stringsOf("漢", 201), 200)).toBe(true);
  });

  it("truncateCodePoints never splits a supplementary-plane character", () => {
    expect(truncateCodePoints("ab😀cd", 3)).toBe("ab😀");
    expect(truncateCodePoints("😀😀😀", 2)).toBe("😀😀");
    expect(truncateCodePoints("漢字", 1)).toBe("漢");
  });

  it("bindCodePointLimit removes maxlength, keeps over-limit paste, and sets a localized message", () => {
    const input = document.createElement("input");
    input.maxLength = 4;
    input.value = "ab";
    document.body.appendChild(input);
    const onValidityChange = vi.fn();
    const handle = bindCodePointLimit(input, 2, { onValidityChange });
    cleanups.push(() => handle.disconnect());

    expect(input.hasAttribute("maxlength")).toBe(false);
    expect(handle.isValid()).toBe(true);

    input.value = "😀😀😀";
    input.dispatchEvent(new InputEvent("input", { bubbles: true, data: "😀😀😀" }));
    expect(input.value).toBe("😀😀😀");
    expect(handle.isValid()).toBe(false);
    expect(input.validationMessage).toContain("2");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(onValidityChange).toHaveBeenLastCalledWith(false);
  });

  it("does not erase unrelated custom validity when becoming valid", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const handle = bindCodePointLimit(input, 2);
    cleanups.push(() => handle.disconnect());

    input.value = "abcd";
    handle.sync();
    expect(handle.isValid()).toBe(false);

    input.setCustomValidity("unrelated");
    input.value = "ok";
    handle.sync();
    // We did not own the current message (caller overwrote ours), so leave it alone.
    expect(input.validationMessage).toBe("unrelated");
  });

  it("does not destroy unrelated custom validity when becoming over-limit", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const handle = bindCodePointLimit(input, 2);
    cleanups.push(() => handle.disconnect());

    input.setCustomValidity("required field");
    input.value = "abcd";
    handle.sync();
    expect(handle.isValid()).toBe(false);
    expect(input.validationMessage).toBe("required field");
  });

  it("relinquishes ownership when another validator replaces the owned over-limit message", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const handle = bindCodePointLimit(input, 2);
    cleanups.push(() => handle.disconnect());

    input.value = "abcd";
    handle.sync();
    expect(input.validationMessage).toContain("2");

    input.setCustomValidity("replaced by other validator");
    handle.sync();
    expect(handle.isValid()).toBe(false);
    expect(input.validationMessage).toBe("replaced by other validator");
  });

  it("disconnect clears only owned validity and leaves unrelated messages", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const handle = bindCodePointLimit(input, 2);
    input.value = "abcd";
    handle.sync();
    expect(input.validationMessage).toContain("2");

    input.setCustomValidity("unrelated");
    handle.disconnect();
    expect(input.validationMessage).toBe("unrelated");
    expect(getCodePointLimitHandle(input)).toBeNull();
  });

  it("re-binding preserves unrelated validation state", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const first = bindCodePointLimit(input, 2);
    input.setCustomValidity("from elsewhere");
    const second = bindCodePointLimit(input, 3);
    cleanups.push(() => second.disconnect());
    expect(getCodePointLimitHandle(input)).toBe(second);
    expect(first.isValid()).toBe(true);
    expect(input.validationMessage).toBe("from elsewhere");

    input.value = "abcd";
    input.dispatchEvent(new InputEvent("input", { bubbles: true }));
    expect(second.isValid()).toBe(false);
    expect(second.max).toBe(3);
    // Unrelated message still present; limiter must not overwrite it.
    expect(input.validationMessage).toBe("from elsewhere");
  });

  it("correcting an over-limit value restores a valid owned state", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const handle = bindCodePointLimit(input, 2);
    cleanups.push(() => handle.disconnect());

    input.value = "abcd";
    handle.sync();
    expect(handle.isValid()).toBe(false);
    expect(input.validationMessage).toContain("2");

    input.value = "ok";
    handle.sync();
    expect(handle.isValid()).toBe(true);
    expect(input.validationMessage).toBe("");
    expect(input.getAttribute("aria-invalid")).toBeNull();
  });

  it("reportCodePointLimitsIn binds unbound controls instead of unmanaged errors", () => {
    const root = document.createElement("form");
    root.innerHTML = `<input data-code-point-max="2" value="abcd" />`;
    document.body.appendChild(root);
    const input = root.querySelector("input") as HTMLInputElement;
    expect(getCodePointLimitHandle(input)).toBeNull();
    expect(reportCodePointLimitsIn(root)).toBe(false);
    const handle = getCodePointLimitHandle(input);
    expect(handle).not.toBeNull();
    cleanups.push(() => handle?.disconnect());
    expect(input.validationMessage).toContain("2");

    input.value = "ok";
    expect(reportCodePointLimitsIn(root)).toBe(true);
    expect(input.validationMessage).toBe("");
  });

  it("disabled and read-only controls do not block reportCodePointLimitsIn", () => {
    const root = document.createElement("form");
    root.innerHTML = `
      <input data-code-point-max="2" value="abcd" readonly />
      <input data-code-point-max="2" value="ok" />
    `;
    document.body.appendChild(root);
    expect(reportCodePointLimitsIn(root)).toBe(true);
    const readonly = root.querySelector("input[readonly]") as HTMLInputElement;
    cleanups.push(() => getCodePointLimitHandle(readonly)?.disconnect());
    cleanups.push(() => getCodePointLimitHandle(root.querySelectorAll("input")[1] as HTMLInputElement)?.disconnect());
  });

  it("defers validation during IME composition", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    const handle = bindCodePointLimit(input, 2);
    cleanups.push(() => handle.disconnect());

    input.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    input.value = "漢漢漢";
    input.dispatchEvent(new InputEvent("input", { bubbles: true, isComposing: true }));
    expect(handle.isValid()).toBe(false);
    input.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
    expect(input.value).toBe("漢漢漢");
    expect(handle.isValid()).toBe(false);
  });

  it("sync handles programmatic updates and existing over-limit values", () => {
    const ta = document.createElement("textarea");
    ta.value = stringsOf("a", 5);
    document.body.appendChild(ta);
    const handle = bindCodePointLimit(ta, 3);
    cleanups.push(() => handle.disconnect());
    expect(handle.isValid()).toBe(false);

    ta.value = "ok";
    handle.sync();
    expect(handle.isValid()).toBe(true);
  });

  it("reportCodePointLimitsIn blocks mutation boundaries when any control is over limit", () => {
    const root = document.createElement("form");
    root.innerHTML = `<input data-code-point-max="2" value="ab" /><textarea data-code-point-max="1">漢漢</textarea>`;
    document.body.appendChild(root);
    const handles = bindCodePointLimitsIn(root);
    cleanups.push(() => handles.forEach((h) => h.disconnect()));
    expect(reportCodePointLimitsIn(root)).toBe(false);
    (root.querySelector("textarea") as HTMLTextAreaElement).value = "漢";
    handles[1].sync();
    expect(reportCodePointLimitsIn(root)).toBe(true);
  });
});

function stringsOf(ch: string, n: number): string {
  return ch.repeat(n);
}
