// Settings > Custom fields (maintainers): define, list and delete a project's
// typed custom fields. Values are edited per todo in the todo dialog.
import { apiFetch } from '../api.js';
import { getSlug } from '../state/selectors.js';
import { escapeHTML, showConfirmDialog, showToast } from '../utils.js';
import { apiErrorMessageOrRaw, t } from '../i18n/index.js';
import { invalidateCustomFieldDefs, loadCustomFieldDefs, type CustomFieldDef } from './todo-custom-fields.js';

const TYPES: ReadonlyArray<CustomFieldDef['type']> = ['text', 'url', 'number'];

export function renderCustomFieldsTabShell(): string {
  return `<div class="settings-section" id="settingsCustomFields">
    <div class="settings-section__title" data-i18n-text="settings.customFields.title">${escapeHTML(t('settings.customFields.title'))}</div>
    <div class="settings-section__description muted" data-i18n-text="settings.customFields.description">${escapeHTML(t('settings.customFields.description'))}</div>
    <div id="customFieldsList" class="muted">${escapeHTML(t('settings.customFields.loading'))}</div>
    <div class="settings-priorities-add-row" style="display:flex; gap:8px; align-items:center; margin-top:12px; flex-wrap:wrap;">
      <input class="input" type="text" maxlength="100" id="customFieldName" placeholder="${escapeHTML(t('settings.customFields.namePlaceholder'))}" aria-label="${escapeHTML(t('settings.customFields.nameLabel'))}" />
      <select class="select" id="customFieldType" aria-label="${escapeHTML(t('settings.customFields.typeLabel'))}">
        ${TYPES.map((type) => `<option value="${type}">${escapeHTML(t(`settings.customFields.type.${type}`))}</option>`).join('')}
      </select>
      <button type="button" class="btn btn--small" id="customFieldAdd">${escapeHTML(t('settings.customFields.add'))}</button>
    </div>
  </div>`;
}

function renderList(container: HTMLElement, defs: readonly CustomFieldDef[]): void {
  if (defs.length === 0) {
    container.innerHTML = `<div class="muted">${escapeHTML(t('settings.customFields.empty'))}</div>`;
    return;
  }
  container.innerHTML = defs
    .map(
      (def) => `<div class="settings-priorities-row" style="display:flex; gap:8px; align-items:center; margin-bottom:8px; flex-wrap:wrap;" data-custom-field-key="${escapeHTML(def.key)}">
      <span style="flex:1; min-width:8em;">${escapeHTML(def.name)} <span class="muted">(${escapeHTML(def.key)}, ${escapeHTML(t(`settings.customFields.type.${def.type}`))})</span></span>
      <button type="button" class="btn btn--ghost btn--small" data-custom-field-delete="${escapeHTML(def.key)}">${escapeHTML(t('settings.customFields.deleteAction'))}</button>
    </div>`,
    )
    .join('');
}

export function bindCustomFieldsTabInteractions(options: { signal: AbortSignal }): void {
  const { signal } = options;
  const slug = getSlug();
  const list = document.getElementById('customFieldsList');
  if (!slug || !list) return;

  const refresh = async () => {
    const defs = await loadCustomFieldDefs(slug, { force: true });
    if (getSlug() !== slug || !list.isConnected) return;
    renderList(list, defs);
  };
  void refresh();

  const nameInput = document.getElementById('customFieldName') as HTMLInputElement | null;
  const typeSelect = document.getElementById('customFieldType') as HTMLSelectElement | null;
  const add = async () => {
    const name = nameInput?.value.trim() ?? '';
    if (!name) {
      nameInput?.focus();
      return;
    }
    try {
      await apiFetch(`/api/board/${encodeURIComponent(slug)}/custom-fields`, {
        method: 'POST',
        body: JSON.stringify({ name, type: typeSelect?.value ?? 'text' }),
      });
      if (nameInput) nameInput.value = '';
      invalidateCustomFieldDefs(slug);
      await refresh();
    } catch (err) {
      showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.customFields.saveFailed' }));
    }
  };
  document.getElementById('customFieldAdd')?.addEventListener('click', () => void add(), { signal });
  nameInput?.addEventListener(
    'keydown',
    (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        void add();
      }
    },
    { signal },
  );

  list.addEventListener(
    'click',
    (e) => {
      const btn = (e.target as HTMLElement).closest<HTMLElement>('[data-custom-field-delete]');
      const key = btn?.dataset.customFieldDelete;
      if (!key) return;
      void (async () => {
        const ok = await showConfirmDialog(
          t('settings.customFields.deleteConfirm.message', { key }),
          t('settings.customFields.deleteConfirm.title'),
          t('settings.customFields.deleteConfirm.confirm'),
        );
        if (!ok) return;
        try {
          await apiFetch(`/api/board/${encodeURIComponent(slug)}/custom-fields/${encodeURIComponent(key)}`, { method: 'DELETE' });
          invalidateCustomFieldDefs(slug);
          await refresh();
        } catch (err) {
          showToast(apiErrorMessageOrRaw(err, { fallbackKey: 'settings.customFields.saveFailed' }));
        }
      })();
    },
    { signal },
  );
}
