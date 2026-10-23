// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import {
  hasInvalidCustomFieldInput,
  readCustomFieldInputs,
  readCustomFieldTypes,
  renderCustomFieldInputs,
  resolveCustomFieldsForSubmit,
  type CustomFieldDef,
} from './todo-custom-fields.js';
import { buildTodoPatchPayload } from './todo-submit.js';
import { customFieldBadgeLabel, renderMemberStoryCardContentHtml, renderStoryCardContentHtml } from '../views/story-card-content.js';

const defs: CustomFieldDef[] = [
  { key: 'mr', name: 'Merge <b>Request</b>', type: 'url' },
  { key: 'repo', name: 'Repo', type: 'text' },
  { key: 'points', name: 'Points', type: 'number' },
];

function mount(values?: Record<string, string | number>, disabled = false): HTMLElement {
  const el = document.createElement('div');
  document.body.append(el);
  renderCustomFieldInputs(el, defs, values, { disabled });
  return el;
}

describe('custom field inputs', () => {
  it('renders one typed input per definition and escapes names', () => {
    const el = mount({ mr: 'https://x.test/1', points: 3 });
    const inputs = el.querySelectorAll('input');
    expect(inputs).toHaveLength(3);
    expect(inputs[0].type).toBe('url');
    expect(inputs[2].type).toBe('number');
    expect(el.querySelector('b')).toBeNull();
    expect(el.textContent).toContain('Merge <b>Request</b>');
    expect(readCustomFieldInputs(el)).toEqual({ mr: 'https://x.test/1', repo: '', points: '3' });
    expect(readCustomFieldTypes(el)).toEqual({ mr: 'url', repo: 'text', points: 'number' });
  });

  it('hides the container when there are no definitions and honours disabled', () => {
    const empty = document.createElement('div');
    renderCustomFieldInputs(empty, [], null, { disabled: false });
    expect(empty.hidden).toBe(true);
    expect(mount({}, true).querySelector('input')?.disabled).toBe(true);
  });

  it('flags an invalid url input', () => {
    const el = mount();
    (el.querySelector('input[data-cf-key="mr"]') as HTMLInputElement).value = 'not a url';
    expect(hasInvalidCustomFieldInput(el)).toBe(true);
  });
});

describe('resolveCustomFieldsForSubmit', () => {
  const types = { mr: 'url', repo: 'text', points: 'number' } as const;

  it('sends only changed fields on edit, null for cleared, numbers as numbers', () => {
    const payload = resolveCustomFieldsForSubmit(
      'edit',
      { mr: 'https://x.test/1', repo: '', points: '4' },
      types,
      { mr: 'https://x.test/1', repo: 'old', points: 3 },
    );
    expect(payload).toEqual({ repo: null, points: 4 });
  });

  it('returns undefined when nothing changed', () => {
    expect(resolveCustomFieldsForSubmit('edit', { repo: 'a' }, types, { repo: 'a' })).toBeUndefined();
  });

  it('on create sends only non-empty values', () => {
    expect(resolveCustomFieldsForSubmit('create', { mr: '', repo: 'r', points: '2' }, types, null)).toEqual({ repo: 'r', points: 2 });
  });

  it('skips a non-finite number instead of sending NaN', () => {
    expect(resolveCustomFieldsForSubmit('create', { points: 'abc' }, types, null)).toBeUndefined();
  });
});

describe('payload and cards', () => {
  it('includes customFields in the patch payload only when present', () => {
    const base = { title: 't', body: '', tags: [] };
    expect(buildTodoPatchPayload(base)).not.toHaveProperty('customFields');
    expect(buildTodoPatchPayload({ ...base, customFields: { repo: null } })).toHaveProperty('customFields', { repo: null });
  });

  it('labels urls by host and escapes card badges', () => {
    expect(customFieldBadgeLabel('https://git.example.com/a/b/1')).toBe('git.example.com');
    expect(customFieldBadgeLabel(7)).toBe('7');
    const input = { localId: 1, title: 'T', tags: [], priority: null, points: null, due: null, customFields: { n: '<img src=x>' } };
    const html = renderMemberStoryCardContentHtml(input, { tagTone: 'plain', tooltips: false } as never, '');
    expect(html).toContain('card__field');
    expect(html).not.toContain('<img');
    const publicHtml = renderStoryCardContentHtml({ ...input, customFields: undefined }, { tagTone: 'plain', tooltips: false } as never);
    expect(publicHtml).not.toContain('card__field');
  });
});
