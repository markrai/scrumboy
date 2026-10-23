// Custom fields in the todo dialog and on cards. Definitions come from
// GET /api/board/{slug}/custom-fields; values travel as `customFields` on the
// todo payloads (key -> string | number, null clears on update).
import { apiFetch } from '../api.js';
const defsCache = new Map();
export function invalidateCustomFieldDefs(slug) {
    if (slug)
        defsCache.delete(slug);
    else
        defsCache.clear();
}
/** Loads a project's definitions; a failure yields no fields rather than blocking the dialog. */
export async function loadCustomFieldDefs(slug, opts) {
    if (!opts?.force) {
        const cached = defsCache.get(slug);
        if (cached)
            return cached;
    }
    try {
        const res = await apiFetch(`/api/board/${encodeURIComponent(slug)}/custom-fields`);
        const items = Array.isArray(res?.items) ? res.items : [];
        defsCache.set(slug, items);
        return items;
    }
    catch {
        return [];
    }
}
export function customFieldValueToInput(value) {
    return value == null ? '' : String(value);
}
/** Renders one labelled input per definition into `container` (hidden when there are none). */
export function renderCustomFieldInputs(container, defs, values, opts) {
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
        if (def.type === 'number')
            input.step = 'any';
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
export function readCustomFieldInputs(container) {
    const out = {};
    container?.querySelectorAll('input[data-cf-key]').forEach((input) => {
        out[input.dataset.cfKey ?? ''] = input.value.trim();
    });
    return out;
}
/** True when a browser-level constraint (bad URL, half-typed number) blocks submit. */
export function hasInvalidCustomFieldInput(container) {
    let invalid = false;
    container?.querySelectorAll('input[data-cf-key]').forEach((input) => {
        if (!input.checkValidity()) {
            if (!invalid)
                input.reportValidity?.();
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
export function resolveCustomFieldsForSubmit(mode, current, types, baseline) {
    const payload = {};
    for (const [key, raw] of Object.entries(current)) {
        const before = customFieldValueToInput(mode === 'edit' ? baseline?.[key] : undefined);
        if (raw === before)
            continue;
        if (raw === '') {
            if (mode === 'edit')
                payload[key] = null;
            continue;
        }
        if (types[key] === 'number') {
            const n = Number(raw);
            if (!Number.isFinite(n))
                continue;
            payload[key] = n;
        }
        else {
            payload[key] = raw;
        }
    }
    return Object.keys(payload).length > 0 ? payload : undefined;
}
/** Field types by key, read back from the rendered inputs. */
export function readCustomFieldTypes(container) {
    const out = {};
    container?.querySelectorAll('input[data-cf-key]').forEach((input) => {
        out[input.dataset.cfKey ?? ''] = input.dataset.cfType ?? 'text';
    });
    return out;
}
