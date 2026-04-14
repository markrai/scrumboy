import { apiFetch } from '../api.js';
import { invalidateBoard } from '../orchestration/board-refresh.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { getSearch, getSettingsProjectId, getSlug, getSprintIdFromUrl, getTag, getTagColors, getUser, } from '../state/selectors.js';
import { setTagColors } from '../state/mutations.js';
import { escapeHTML, sanitizeHexColor, showConfirmDialog, showToast } from '../utils.js';
let cachedTags = null;
let cachedTagsHTML = null;
let cachedTagsURL = null;
export function invalidateTagsCache() {
    cachedTags = null;
    cachedTagsHTML = null;
    cachedTagsURL = null;
}
async function applyTagColorSuccess(tagName, color) {
    try {
        const tagColors = { ...getTagColors() };
        if (color) {
            tagColors[tagName] = color;
        }
        else {
            delete tagColors[tagName];
        }
        setTagColors(tagColors);
        if (getUser()) {
            try {
                await apiFetch('/api/user/preferences', {
                    method: 'PUT',
                    body: JSON.stringify({ key: 'tagColors', value: JSON.stringify(tagColors) }),
                });
            }
            catch {
                // Ignore errors saving preferences.
            }
        }
        const clearBtn = document.querySelector(`.settings-color-clear[data-tag="${escapeHTML(tagName)}"]`);
        if (clearBtn) {
            clearBtn.style.display = color ? '' : 'none';
        }
        invalidateTagsCache();
        if (getSlug()) {
            await invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
        }
        showToast('Tag color updated');
    }
    catch (err) {
        showToast(err.message);
    }
}
async function updateTagColor(tagName, tagId, color) {
    const projectId = getSettingsProjectId();
    const slug = getSlug();
    const isDurable = !!projectId;
    if (isDurable) {
        if (tagId == null || tagId <= 0) {
            showToast('Cannot update color: tag ID missing');
            return;
        }
        const url = `/api/projects/${projectId}/tags/id/${tagId}/color`;
        try {
            recordLocalMutation();
            await apiFetch(url, {
                method: 'PATCH',
                body: JSON.stringify({ color }),
            });
            await applyTagColorSuccess(tagName, color);
        }
        catch (err) {
            showToast(err.message);
        }
        return;
    }
    if (slug) {
        let url;
        if (tagId != null && tagId > 0) {
            url = `/api/board/${slug}/tags/id/${tagId}/color`;
        }
        else {
            url = `/api/board/${slug}/tags/${encodeURIComponent(tagName)}/color`;
        }
        try {
            recordLocalMutation();
            await apiFetch(url, {
                method: 'PATCH',
                body: JSON.stringify({ color }),
            });
            await applyTagColorSuccess(tagName, color);
        }
        catch (err) {
            showToast(err.message);
        }
        return;
    }
    showToast('No project available');
}
async function deleteTag(tagName, tagId, rerender) {
    let url = null;
    const isDurableMode = !!getSettingsProjectId();
    if (getSlug()) {
        url =
            tagId != null
                ? `/api/board/${getSlug()}/tags/id/${tagId}`
                : `/api/board/${getSlug()}/tags/${encodeURIComponent(tagName)}`;
    }
    else if (isDurableMode) {
        if (tagId == null) {
            showToast('Cannot delete: tag ID missing');
            return;
        }
        url = `/api/projects/${getSettingsProjectId()}/tags/id/${tagId}`;
    }
    else {
        showToast('No project available');
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
            }
            catch {
                // Ignore errors saving preferences.
            }
        }
        invalidateTagsCache();
        await rerender();
        if (getSlug()) {
            await invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
        }
        showToast(`Tag "${tagName}" deleted`);
    }
    catch (err) {
        showToast(err.message);
    }
}
export async function loadTagSettingsContent(tagsURL) {
    if (cachedTagsURL === tagsURL && cachedTags !== null && cachedTagsHTML !== null) {
        return cachedTagsHTML;
    }
    try {
        const tags = await apiFetch(tagsURL);
        tags.sort((a, b) => a.name.localeCompare(b.name));
        const tagColors = {};
        tags.forEach((tag) => {
            if (tag.color) {
                tagColors[tag.name] = tag.color;
            }
        });
        setTagColors(tagColors);
        const isDurableProject = !!getSettingsProjectId();
        const tagsHTML = tags.length === 0
            ? "<div class='muted'>No tags yet. Create todos with tags to see them here.</div>"
            : tags
                .map((tag) => {
                const colorValue = sanitizeHexColor(tag.color, '#9CA3AF') || '#9CA3AF';
                const showDelete = tag.canDelete === true && tag.tagId != null;
                const hasTagId = tag.tagId != null && tag.tagId > 0;
                const colorDisabled = isDurableProject && !hasTagId;
                const tagIdAttr = hasTagId ? ` data-tag-id="${String(tag.tagId)}"` : '';
                return `
                <div class="settings-tag-item">
                  <span class="settings-tag-name" title="${escapeHTML(tag.name)}">${escapeHTML(tag.name)}</span>
                  <div class="settings-tag-color-controls">
                    <input
                      type="color"
                      class="settings-color-picker"
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      value="${colorValue}"
                      title="${colorDisabled ? 'Tag ID missing; cannot update color' : 'Tag color'}"
                      ${colorDisabled ? 'disabled' : ''}
                    />
                    <button
                      class="btn btn--ghost btn--small settings-color-clear"
                      data-tag="${escapeHTML(tag.name)}"${tagIdAttr}
                      title="Clear color"
                      ${!tag.color ? 'style="display: none;"' : ''}
                      ${colorDisabled ? 'disabled' : ''}
                    >Clear</button>
                    ${showDelete
                    ? `<button
                      class="btn btn--danger btn--small settings-tag-delete"
                      data-tag="${escapeHTML(tag.name)}"
                      data-tag-id="${String(tag.tagId)}"
                      title="Delete tag"
                      aria-label="Delete tag"
                    >✕</button>`
                    : ''}
                  </div>
                </div>
              `;
            })
                .join('');
        cachedTags = tags;
        cachedTagsHTML = tagsHTML;
        cachedTagsURL = tagsURL;
        return tagsHTML;
    }
    catch (err) {
        invalidateTagsCache();
        throw err;
    }
}
export function bindTagTabInteractions(options) {
    if (!options.hasProjectAccess)
        return;
    document.querySelectorAll('.settings-color-picker').forEach((picker) => {
        picker.addEventListener('change', async (e) => {
            const el = e.target;
            const tagName = el.getAttribute('data-tag');
            const tagIdAttr = el.getAttribute('data-tag-id');
            const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
            const color = el.value;
            if (tagName) {
                await updateTagColor(tagName, Number.isNaN(tagId) ? undefined : tagId, color);
            }
        }, { signal: options.signal });
    });
    document.querySelectorAll('.settings-color-clear').forEach((btn) => {
        btn.addEventListener('click', async (e) => {
            const el = e.target;
            const tagName = el.getAttribute('data-tag');
            const tagIdAttr = el.getAttribute('data-tag-id');
            const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
            if (tagName) {
                await updateTagColor(tagName, Number.isNaN(tagId) ? undefined : tagId, null);
            }
        }, { signal: options.signal });
    });
    document.querySelectorAll('.settings-tag-delete').forEach((btn) => {
        btn.addEventListener('click', async (e) => {
            const el = e.target;
            const tagName = el.getAttribute('data-tag');
            const tagIdAttr = el.getAttribute('data-tag-id');
            const tagId = tagIdAttr ? parseInt(tagIdAttr, 10) : undefined;
            if (tagName) {
                const confirmed = await showConfirmDialog(`Delete tag "${tagName}" from all projects? This will remove it from all todos.`, 'Delete Tag');
                if (!confirmed)
                    return;
                await deleteTag(tagName, !Number.isNaN(tagId) ? tagId : undefined, options.rerender);
            }
        }, { signal: options.signal });
    });
}
