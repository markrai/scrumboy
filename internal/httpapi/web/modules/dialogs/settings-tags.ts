import { apiFetch } from '../api.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { recordLocalMutation } from '../realtime/guard.js';
import {
  getSearch,
  getSettingsProjectId,
  getSlug,
  getSprintIdFromUrl,
  getTag,
  getTagColors,
  getUser,
} from '../state/selectors.js';
import { setTagColors } from '../state/mutations.js';
import { escapeHTML, sanitizeHexColor, showConfirmDialog, showToast } from '../utils.js';
import { apiErrorMessageOrRaw, t } from '../i18n/index.js';

/** Which Settings → Tag Colors list is showing; do not infer from settingsProjectId. */
export type TagSettingsScope = 'mine' | 'project' | 'board';

type BindTagTabInteractionsOptions = {
  signal: AbortSignal;
  scope: TagSettingsScope | null;
  rerender: () => Promise<void>;
};

let cachedTags: any[] | null = null;
let cachedTagsHTML: string | null = null;
let cachedTagsURL: string | null = null;
let activeTagSettingsScope: TagSettingsScope | null = null;

export function invalidateTagsCache(): void {
  cachedTags = null;
  cachedTagsHTML = null;
  cachedTagsURL = null;
}

async function applyTagColorSuccess(tagName: string, color: string | null): Promise<void> {
  try {
    const tagColors = { ...getTagColors() };
    if (color) {
      tagColors[tagName] = color;
    } else {
      delete tagColors[tagName];
    }
    setTagColors(tagColors);

    if (getUser()) {
      try {
        await apiFetch('/api/user/preferences', {
          method: 'PUT',
          body: JSON.stringify({ key: 'tagColors', value: JSON.stringify(tagColors) }),
        });
      } catch {
        // Ignore errors saving preferences.
      }
    }

    const clearBtn = document.querySelector(
      `.settings-color-clear[data-tag="${escapeHTML(tagName)}"]`
    );
    if (clearBtn) {
      (clearBtn as HTMLElement).style.display = color ? '' : 'none';
    }

    invalidateTagsCache();

    if (getSlug()) {
      await invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
    }

    showToast(t('settings.tagColors.toast.colorUpdated'));
  } catch (err: any) {
    showToast(apiErrorMessageOrRaw(err));
  }
}

function colorMutationURL(
  scope: TagSettingsScope,
  tagName: string,
  tagId: number | null | undefined
): string | null {
  if (scope === 'mine') {
    if (tagId == null || tagId <= 0) return null;
    return `/api/tags/mine/${tagId}/color`;
  }
  if (scope === 'board') {
    const slug = getSlug();
    if (!slug) return null;
    if (tagId != null && tagId > 0) {
      return `/api/board/${slug}/tags/id/${tagId}/color`;
    }
    return `/api/board/${slug}/tags/${encodeURIComponent(tagName)}/color`;
  }
  // project: durable project-scoped list (grouped name or board-scoped id).
  const projectId = getSettingsProjectId();
  if (!projectId) return null;
  if (tagId != null && tagId > 0) {
    return `/api/projects/${projectId}/tags/id/${tagId}/color`;
  }
  return `/api/projects/${projectId}/tags/${encodeURIComponent(tagName)}/color`;
}

function deleteMutationURL(
  scope: TagSettingsScope,
  tagName: string,
  tagId: number | undefined
): string | null {
  if (scope === 'mine') {
    if (tagId == null || tagId <= 0) return null;
    return `/api/tags/mine/${tagId}`;
  }
  if (scope === 'board') {
    const slug = getSlug();
    if (!slug) return null;
    return tagId != null
      ? `/api/board/${slug}/tags/id/${tagId}`
      : `/api/board/${slug}/tags/${encodeURIComponent(tagName)}`;
  }
  const projectId = getSettingsProjectId();
  if (!projectId) return null;
  return tagId != null
    ? `/api/projects/${projectId}/tags/id/${tagId}`
    : `/api/projects/${projectId}/tags/${encodeURIComponent(tagName)}`;
}

async function updateTagColor(
  tagName: string,
  tagId: number | null | undefined,
  color: string | null
): Promise<void> {
  const scope = activeTagSettingsScope;
  if (!scope) {
    showToast(t('settings.tagColors.toast.noProject'));
    return;
  }
  const url = colorMutationURL(scope, tagName, tagId);
  if (!url) {
    showToast(t('settings.tagColors.toast.noProject'));
    return;
  }
  try {
    recordLocalMutation();
    await apiFetch(url, {
      method: 'PATCH',
      body: JSON.stringify({ color }),
    });
    await applyTagColorSuccess(tagName, color);
  } catch (err: any) {
    showToast(apiErrorMessageOrRaw(err));
  }
}

async function deleteTag(
  tagName: string,
  tagId: number | undefined,
  rerender: () => Promise<void>
): Promise<void> {
  const scope = activeTagSettingsScope;
  if (!scope) {
    showToast(t('settings.tagColors.toast.noProject'));
    return;
  }
  const url = deleteMutationURL(scope, tagName, tagId);
  if (!url) {
    showToast(t('settings.tagColors.toast.noProject'));
    return;
  }

  try {
    recordLocalMutation();
    await apiFetch(url, { method: 'DELETE' });

    const tagColors = { ...getTagColors() };
    delete tagColors[tagName];
    setTagColors(tagColors);

    if (getUser()) {
      try {
        await apiFetch('/api/user/preferences', {
          method: 'PUT',
          body: JSON.stringify({ key: 'tagColors', value: JSON.stringify(tagColors) }),
        });
      } catch {
        // Ignore errors saving preferences.
      }
    }

    invalidateTagsCache();
    await rerender();

    if (getSlug()) {
      await invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
    }

    showToast(t('settings.tagColors.toast.deleted', { name: tagName }));
  } catch (err: any) {
    showToast(apiErrorMessageOrRaw(err));
  }
}

export async function loadTagSettingsContent(
  tagsURL: string,
  scope: TagSettingsScope
): Promise<string> {
  activeTagSettingsScope = scope;
  if (cachedTagsURL === tagsURL && cachedTags !== null && cachedTagsHTML !== null) {
    return cachedTagsHTML;
  }

  try {
    const tags = await apiFetch<any[]>(tagsURL);
    tags.sort((a: any, b: any) => a.name.localeCompare(b.name));

    const tagColors: Record<string, string> = {};
    tags.forEach((tag: any) => {
      if (tag.color) {
        tagColors[tag.name] = tag.color;
      }
    });
    setTagColors(tagColors);

    const tagsHTML =
      tags.length === 0
        ? "<div class='muted' data-i18n-text=\"settings.tagColors.empty\">No tags yet. Create todos with tags to see them here.</div>"
        : tags
            .map((tag: any) => {
              const colorValue = sanitizeHexColor(tag.color, '#9CA3AF') || '#9CA3AF';
              // Grouped personal labels have no tagId and use name-based routes, so the
              // color picker is always usable; delete is driven by deleteScope.
              // Durable board-scoped entries (real tagId) require canUpdateColor so a
              // Viewer is not offered an enabled shared-color picker.
              const canDelete = tag.deleteScope ? tag.deleteScope !== 'none' : tag.canDelete === true;
              const hasTagId = tag.tagId != null && tag.tagId > 0;
              const canUpdateColor = tag.canUpdateColor !== false;
              const tagIdAttr = hasTagId ? ` data-tag-id="${String(tag.tagId)}"` : '';
              const colorDisabledAttr = canUpdateColor ? '' : ' disabled';
              return `
                <div class="settings-tag-item">
                  <span class="settings-tag-name" title="${escapeHTML(tag.name)}">${escapeHTML(tag.name)}</span>
                  <div class="settings-tag-color-controls">
                    <input
                      type="color"
                      class="settings-color-picker"
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      value="${colorValue}"
                      title="Tag color"
                      data-i18n-title="settings.tagColors.colorTitle"${colorDisabledAttr}
                    />
                    <button
                      class="btn btn--ghost btn--small settings-color-clear"
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      title="Clear color"
                      data-i18n-title="settings.tagColors.clearTitle"
                      data-i18n-text="settings.tagColors.clear"
                      ${!tag.color || !canUpdateColor ? 'style="display: none;"' : ''}${colorDisabledAttr}
                    >Clear</button>
                    ${
                      canDelete
                        ? `<button
                      class="btn btn--danger btn--small settings-tag-delete"
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      data-delete-scope="${tag.deleteScope === 'project' ? 'project' : 'mine'}"
                      title="Delete tag"
                      aria-label="Delete tag"
                      data-i18n-title="settings.tagColors.deleteTitle"
                      data-i18n-aria-label="settings.tagColors.deleteAriaLabel"
                    >✕</button>`
                        : ''
                    }
                  </div>
                </div>
              `;
            })
            .join('');

    cachedTags = tags;
    cachedTagsHTML = tagsHTML;
    cachedTagsURL = tagsURL;
    return tagsHTML;
  } catch (err) {
    invalidateTagsCache();
    throw err;
  }
}

export function bindTagTabInteractions(options: BindTagTabInteractionsOptions): void {
  if (!options.scope) return;
  activeTagSettingsScope = options.scope;

  document.querySelectorAll('.settings-color-picker').forEach((picker) => {
    picker.addEventListener(
      'change',
      async (e) => {
        const el = e.target as HTMLElement;
        const tagName = el.getAttribute('data-tag');
        const tagIdAttr = el.getAttribute('data-tag-id');
        const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
        const color = (el as HTMLInputElement).value;
        if (tagName) {
          await updateTagColor(tagName, Number.isNaN(tagId) ? undefined : tagId, color);
        }
      },
      { signal: options.signal }
    );
  });

  document.querySelectorAll('.settings-color-clear').forEach((btn) => {
    btn.addEventListener(
      'click',
      async (e) => {
        const el = e.target as HTMLElement;
        const tagName = el.getAttribute('data-tag');
        const tagIdAttr = el.getAttribute('data-tag-id');
        const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
        if (tagName) {
          await updateTagColor(tagName, Number.isNaN(tagId) ? undefined : tagId, null);
        }
      },
      { signal: options.signal }
    );
  });

  document.querySelectorAll('.settings-tag-delete').forEach((btn) => {
    btn.addEventListener(
      'click',
      async (e) => {
        const el = e.target as HTMLElement;
        const tagName = el.getAttribute('data-tag');
        const tagIdAttr = el.getAttribute('data-tag-id');
        const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
        const deleteScope = el.getAttribute('data-delete-scope');
        if (tagName) {
          const messageKey =
            deleteScope === 'project'
              ? 'settings.tagColors.deleteConfirm.messageProject'
              : 'settings.tagColors.deleteConfirm.message';
          const confirmed = await showConfirmDialog(
            t(messageKey, { name: tagName }),
            t('settings.tagColors.deleteConfirm.title')
          );
          if (!confirmed) return;
          await deleteTag(tagName, !Number.isNaN(tagId) ? tagId : undefined, options.rerender);
        }
      },
      { signal: options.signal }
    );
  });
}
