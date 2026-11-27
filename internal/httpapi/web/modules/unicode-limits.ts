/**
 * Unicode code-point limits that match Go's utf8.RuneCountInString.
 *
 * HTML maxlength / JS String.length count UTF-16 code units, so supplementary
 * plane characters (emoji) and some combining sequences disagree with the
 * server. Prefer these helpers over native maxlength for user-facing text
 * fields that the backend validates by rune count.
 */

import { hasI18nKey, t } from "./i18n/index.js";

const HANDLE_KEY = "__scrumboyCodePointLimit" as const;

type BoundElement = (HTMLInputElement | HTMLTextAreaElement) & {
  [HANDLE_KEY]?: CodePointLimitHandle;
};

export function codePointLength(value: string): number {
  return [...String(value ?? "")].length;
}

export function exceedsCodePointLimit(value: string, max: number): boolean {
  if (!Number.isFinite(max) || max < 0) return false;
  return codePointLength(value) > Math.trunc(max);
}

/**
 * Truncate to at most `max` Unicode code points. Intended for programmatic
 * normalization (seed titles, query clamps), never for silently cutting
 * live user input.
 */
export function truncateCodePoints(value: string, max: number): string {
  if (!Number.isFinite(max) || max < 0) return String(value ?? "");
  const points = [...String(value ?? "")];
  const limit = Math.trunc(max);
  return points.length > limit ? points.slice(0, limit).join("") : String(value ?? "");
}

export type CodePointLimitHandle = {
  /** Maximum Unicode code points allowed. */
  readonly max: number;
  /** Whether the current value is within the limit. */
  isValid(): boolean;
  /** Re-sync after programmatic value assignment. */
  sync(): void;
  /** Focus the control and show the browser validity UI when over limit. */
  report(): boolean;
  /** Remove listeners and clear only the validity state this binder owns. */
  disconnect(): void;
};

export type BindCodePointLimitOptions = {
  /**
   * Called whenever validity changes. The element stays editable when over
   * limit so pasted content is not truncated; callers may surface UI state.
   */
  onValidityChange?: (valid: boolean) => void;
  /** CustomValidity message when over limit (empty uses the localized default). */
  validationMessage?: string | (() => string);
};

function defaultValidationMessage(max: number): string {
  if (hasI18nKey("validation.codePointLimit")) {
    return t("validation.codePointLimit", { max });
  }
  return `Use at most ${max} characters.`;
}

function readValidationMessage(max: number, options?: BindCodePointLimitOptions): string {
  const raw = options?.validationMessage;
  if (typeof raw === "function") {
    const message = raw();
    if (message.trim()) return message;
  } else if (typeof raw === "string" && raw.trim()) {
    return raw;
  }
  return defaultValidationMessage(max);
}

/**
 * Enforce a Unicode code-point ceiling without native maxlength truncation.
 *
 * - Typing, paste, IME composition, and programmatic updates are measured in
 *   code points.
 * - Values that exceed the limit are kept intact (never sliced) and marked
 *   invalid via setCustomValidity / aria-invalid when this binder owns validity.
 * - Unrelated customValidity messages are preserved.
 * - Removes incompatible HTML maxlength so the browser cannot UTF-16-truncate.
 * - Re-binding the same element disconnects the previous handle first.
 */
export function bindCodePointLimit(
  element: HTMLInputElement | HTMLTextAreaElement,
  max: number,
  options?: BindCodePointLimitOptions,
): CodePointLimitHandle {
  const bound = element as BoundElement;
  bound[HANDLE_KEY]?.disconnect();

  const limit = Math.max(0, Math.trunc(max));
  element.removeAttribute("maxlength");
  element.dataset.codePointMax = String(limit);

  let lastNotified: boolean | null = null;
  let composing = false;
  let ownsValidity = false;
  let ownedMessage = "";
  let ownsAriaInvalid = false;
  let disconnected = false;

  const clearOwnedValidity = () => {
    if (!ownsValidity) return;
    if (!element.validationMessage || element.validationMessage === ownedMessage) {
      element.setCustomValidity("");
    }
    ownsValidity = false;
    ownedMessage = "";
  };

  const clearOwnedAriaInvalid = () => {
    if (!ownsAriaInvalid) return;
    if (element.getAttribute("aria-invalid") === "true") {
      element.removeAttribute("aria-invalid");
    }
    ownsAriaInvalid = false;
  };

  const refresh = (): boolean => {
    if (disconnected) return !exceedsCodePointLimit(element.value, limit);
    const valid = !exceedsCodePointLimit(element.value, limit);
    if (valid) {
      clearOwnedValidity();
      clearOwnedAriaInvalid();
    } else {
      const current = element.validationMessage;
      if (ownsValidity && current && current !== ownedMessage) {
        // Another validator replaced our owned message; relinquish rather than overwrite.
        ownsValidity = false;
        ownedMessage = "";
        ownsAriaInvalid = false;
      } else if (!ownsValidity && current) {
        // Unrelated custom validity — leave it alone.
      } else {
        ownedMessage = readValidationMessage(limit, options);
        element.setCustomValidity(ownedMessage);
        ownsValidity = true;
        element.setAttribute("aria-invalid", "true");
        ownsAriaInvalid = true;
      }
    }
    if (lastNotified !== valid) {
      lastNotified = valid;
      options?.onValidityChange?.(valid);
    }
    return valid;
  };

  const onCompositionStart = () => {
    composing = true;
  };
  const onCompositionEnd = () => {
    composing = false;
    refresh();
  };
  const onInput = (event: Event) => {
    if (composing) return;
    if (event instanceof InputEvent && event.isComposing) return;
    refresh();
  };

  element.addEventListener("compositionstart", onCompositionStart);
  element.addEventListener("compositionend", onCompositionEnd);
  element.addEventListener("input", onInput);

  const handle: CodePointLimitHandle = {
    max: limit,
    isValid: () => !exceedsCodePointLimit(element.value, limit),
    sync: () => {
      refresh();
    },
    report: () => {
      const valid = refresh();
      if (!valid) element.reportValidity();
      return valid;
    },
    disconnect: () => {
      if (disconnected) return;
      disconnected = true;
      element.removeEventListener("compositionstart", onCompositionStart);
      element.removeEventListener("compositionend", onCompositionEnd);
      element.removeEventListener("input", onInput);
      clearOwnedValidity();
      clearOwnedAriaInvalid();
      delete element.dataset.codePointMax;
      if (bound[HANDLE_KEY] === handle) delete bound[HANDLE_KEY];
    },
  };

  bound[HANDLE_KEY] = handle;
  refresh();
  return handle;
}

export function getCodePointLimitHandle(
  element: HTMLInputElement | HTMLTextAreaElement | null | undefined,
): CodePointLimitHandle | null {
  if (!element) return null;
  return (element as BoundElement)[HANDLE_KEY] ?? null;
}

function ensureBound(
  element: HTMLInputElement | HTMLTextAreaElement,
  max: number,
): CodePointLimitHandle {
  return getCodePointLimitHandle(element) ?? bindCodePointLimit(element, max);
}

/**
 * Sync and report the first invalid editable code-point-limited control under
 * `root`. Unbound `[data-code-point-max]` controls receive a managed binder
 * rather than a persistent unmanaged customValidity. Disabled and read-only
 * controls are synced (to clear stale owned invalidity) but do not block.
 */
export function reportCodePointLimitsIn(root: ParentNode = document): boolean {
  const nodes = root.querySelectorAll?.("input[data-code-point-max], textarea[data-code-point-max]");
  let ok = true;
  let reported = false;
  nodes?.forEach((node) => {
    if (!(node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement)) return;
    const raw = node.getAttribute("data-code-point-max");
    const max = raw == null ? NaN : Number(raw);
    if (!Number.isFinite(max) || max < 0) return;
    const handle = ensureBound(node, Math.trunc(max));
    handle.sync();
    if (node.disabled || node.readOnly) return;
    if (!handle.isValid()) {
      ok = false;
      if (!reported) {
        handle.report();
        reported = true;
      }
    }
  });
  return ok;
}

/**
 * Bind every `[data-code-point-max]` input/textarea under `root`.
 * Re-binding replaces any previous handle on the same element.
 */
export function bindCodePointLimitsIn(
  root: ParentNode = document,
  options?: BindCodePointLimitOptions,
): CodePointLimitHandle[] {
  const handles: CodePointLimitHandle[] = [];
  const nodes = root.querySelectorAll?.("input[data-code-point-max], textarea[data-code-point-max]");
  nodes?.forEach((node) => {
    if (!(node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement)) return;
    const raw = node.getAttribute("data-code-point-max");
    const max = raw == null ? NaN : Number(raw);
    if (!Number.isFinite(max) || max < 0) return;
    handles.push(bindCodePointLimit(node, max, options));
  });
  return handles;
}
