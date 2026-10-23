// Custom fields in the todo dialog and on cards. Definitions come from
// GET /api/board/{slug}/custom-fields; values travel as `customFields` on the
// todo payloads (key -> string | number, null clears on update).
import { apiFetch } from '../api.js';

export type CustomFieldType = 'text' | 'url' | 'number';
export type CustomFieldDef = { key: string; name: string; type: CustomFieldType };
export type CustomFieldValue = string | number;
export type CustomFieldValues = Record<string, CustomFieldValue>;
export type CustomFieldsPayload = Record<string, CustomFieldValue | null>;

const defsCache = new Map<string, CustomFieldDef[]>();

export function invalidateCustomFieldDefs(slug?: string): void {
  if (slug) defsCache.delete(slug);
  else defsCache.clear();
}

/** Loads a project's definitions; a failure yields no fields rather than blocking the dialog. */
export async function loadCustomFieldDefs(slug: string, opts?: { force?: boolean }): Promise<CustomFieldDef[]> {
  if (!opts?.force) {
    const cached = defsCache.get(slug);
    if (cached) return cached;
  }
  try {
    const res = await apiFetch<{ items?: CustomFieldDef[] }>(`/api/board/${encodeURIComponent(slug)}/custom-fields`);
    const items = Array.isArray(res?.items) ? res.items : [];
    defsCache.set(slug, items);
    return items;
  } catch {
    return [];
  }
}

export function customFieldValueToInput(value: CustomFieldValue | null | undefined): string {
  return value == null ? '' : String(value);
}

/** Renders one labelled input per definition into `container` (hidden when there are none). */
export function renderCustomFieldInputs(
  container: HTMLElement,
  defs: readonly CustomFieldDef[],
  values: Readonly<CustomFieldValues> | null | undefined,
  opts: { disabled: boolean },
): void {
  container.replaceChildren();
  container.hidden = defs.length === 0;
  for (const def of defs) {
    const label = document.createElement('label');
    label.className = 'field';
    const caption = document.createElement('div');
    caption.className = 'field__label';
    caption.textContent = def.name;
    const input = document.createElement('input');
    input.className = 'input';
    input.type = def.type === 'number' ? 'number' : def.type === 'url' ? 'url' : 'text';
    if (def.type === 'number') input.step = 'any';
    input.maxLength = 2000;
    input.name = `customField:${def.key}`;
    input.dataset.cfKey = def.key;
    input.dataset.cfType = def.type;
    input.value = customFieldValueToInput(values?.[def.key]);
    input.disabled = opts.disabled;
    label.append(caption, input);
    container.append(label);
  }
}

/** Current input strings keyed by field key. */
export function readCustomFieldInputs(container: ParentNode | null | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  container?.querySelectorAll<HTMLInputElement>('input[data-cf-key]').forEach((input) => {
    out[input.dataset.cfKey ?? ''] = input.value.trim();
  });
  return out;
}

/** True when a browser-level constraint (bad URL, half-typed number) blocks submit. */
export function hasInvalidCustomFieldInput(container: ParentNode | null | undefined): boolean {
  let invalid = false;
  container?.querySelectorAll<HTMLInputElement>('input[data-cf-key]').forEach((input) => {
    if (!input.checkValidity()) {
      if (!invalid) input.reportValidity?.();
      invalid = true;
    }
  });
  return invalid;
}

/**
 * Builds the `customFields` payload from changed inputs only, so an untouched
 * field is never rewritten. Returns undefined when nothing changed.
 * Create sends only non-empty values; edit sends null for a cleared field.
 */
export function resolveCustomFieldsForSubmit(
  mode: 'create' | 'edit',
  current: Readonly<Record<string, string>>,
  types: Readonly<Record<string, CustomFieldType>>,
  baseline: Readonly<CustomFieldValues> | null | undefined,
): CustomFieldsPayload | undefined {
  const payload: CustomFieldsPayload = {};
  for (const [key, raw] of Object.entries(current)) {
    const before = customFieldValueToInput(mode === 'edit' ? baseline?.[key] : undefined);
    if (raw === before) continue;
    if (raw === '') {
      if (mode === 'edit') payload[key] = null;
      continue;
    }
    if (types[key] === 'number') {
      const n = Number(raw);
      if (!Number.isFinite(n)) continue;
      payload[key] = n;
    } else {
      payload[key] = raw;
    }
  }
  return Object.keys(payload).length > 0 ? payload : undefined;
}

/** Field types by key, read back from the rendered inputs. */
export function readCustomFieldTypes(container: ParentNode | null | undefined): Record<string, CustomFieldType> {
  const out: Record<string, CustomFieldType> = {};
  container?.querySelectorAll<HTMLInputElement>('input[data-cf-key]').forEach((input) => {
    out[input.dataset.cfKey ?? ''] = (input.dataset.cfType as CustomFieldType) ?? 'text';
  });
  return out;
}
