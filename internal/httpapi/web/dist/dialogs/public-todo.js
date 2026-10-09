/**
 * Read-only story detail for public boards. It uses only the public detail
 * and links endpoints, renders user content through textContent or the shared
 * sanitized Markdown path, and has no form, mutation control, or member data.
 * It is separate from #todoDialog so none of the editor's submit, archive,
 * delete, link-editing, or assignment handlers can be reached.
 */
import { fetchPublicTodo, fetchPublicTodoLinks, isPublicAbort, isPublicNotFound, } from '../public-board-api.js';
import { renderMarkdownPreviewInto } from '../markdown-preview.js';
import { sanitizeHexColor } from '../utils.js';
import { t } from '../i18n/index.js';
export const PUBLIC_TODO_DIALOG_ID = 'publicTodoDialog';
let dialogEl = null;
let generation = 0;
let controller = null;
let activeContext = null;
let openLocalId = null;
function ensureDialog() {
    if (dialogEl && dialogEl.isConnected)
        return dialogEl;
    const dialog = document.createElement('dialog');
    dialog.id = PUBLIC_TODO_DIALOG_ID;
    dialog.className = 'dialog public-todo-dialog';
    dialog.setAttribute('aria-labelledby', 'publicTodoDialogTitle');
    const form = document.createElement('div');
    form.className = 'dialog__form';
    const header = document.createElement('div');
    header.className = 'dialog__header';
    const title = document.createElement('div');
    title.className = 'dialog__title';
    title.id = 'publicTodoDialogTitle';
    const close = document.createElement('button');
    close.className = 'btn btn--ghost';
    close.type = 'button';
    close.setAttribute('data-public-todo-close', '');
    close.setAttribute('data-i18n-aria-label', 'common.close');
    close.textContent = '✕';
    close.addEventListener('click', () => dialog.close());
    header.append(title, close);
    const body = document.createElement('div');
    body.className = 'todo-dialog__body public-todo__body';
    body.setAttribute('data-public-todo-body', '');
    form.append(header, body);
    dialog.append(form);
    // The close event may be dispatched asynchronously. A late event for a
    // dialog that has since been reopened is ignored; programmatic closes clear
    // activeContext first so they never report a user close.
    dialog.addEventListener('close', () => {
        if (dialog.open)
            return;
        const ctx = activeContext;
        activeContext = null;
        controller?.abort();
        controller = null;
        generation++;
        openLocalId = null;
        ctx?.onClosedByUser();
    });
    document.body.appendChild(dialog);
    dialogEl = dialog;
    return dialog;
}
function parts(dialog) {
    return {
        title: dialog.querySelector('#publicTodoDialogTitle'),
        body: dialog.querySelector('[data-public-todo-body]'),
        close: dialog.querySelector('[data-public-todo-close]'),
    };
}
function field(labelKey, content) {
    const wrap = document.createElement('div');
    wrap.className = 'field public-todo__field';
    const label = document.createElement('div');
    label.className = 'field__label';
    label.setAttribute('data-i18n-text', labelKey);
    label.textContent = t(labelKey);
    wrap.append(label, content);
    return wrap;
}
function chip(text, colorValue, className) {
    const el = document.createElement('span');
    el.className = className;
    el.textContent = text;
    const safe = sanitizeHexColor(colorValue ?? undefined);
    if (safe) {
        el.style.borderColor = safe;
        el.style.background = `${safe}20`;
        el.style.color = safe;
    }
    return el;
}
async function renderBody(container, body, ctx) {
    container.className = 'todo-markdown-preview public-todo__notes';
    if (body.trim() === '') {
        container.classList.add('muted');
        container.setAttribute('data-i18n-text', 'publicBoard.story.noNotes');
        container.textContent = t('publicBoard.story.noNotes');
        return;
    }
    if (ctx.markdownEnabled) {
        try {
            await renderMarkdownPreviewInto(container, body, { mermaidEnabled: ctx.mermaidEnabled });
            return;
        }
        catch {
            // Markdown vendor unavailable: fall through to inert plain text.
        }
    }
    container.classList.add('public-todo__notes--plain');
    container.textContent = body;
}
function renderLinks(links, ctx) {
    if (links.length === 0)
        return null;
    const list = document.createElement('div');
    list.className = 'public-todo__links';
    for (const link of links) {
        const item = document.createElement('span');
        item.className = 'tag-chip';
        item.setAttribute('data-link-direction', link.direction);
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'tag-chip-link';
        btn.setAttribute('data-public-link-open', String(link.localId));
        btn.textContent = `#${link.localId} ${link.title}`;
        // Links come only from the public endpoint of this board; navigation stays
        // on the same slug and is re-authorized by the public detail endpoint.
        btn.addEventListener('click', () => ctx.onNavigateToStory(link.localId));
        item.append(btn);
        list.append(item);
    }
    return list;
}
export async function renderPublicTodoContent(container, todo, links, ctx) {
    container.replaceChildren();
    const notes = document.createElement('div');
    container.append(field('todo.fields.notes', notes));
    await renderBody(notes, todo.body, ctx);
    if (todo.tags.length > 0) {
        const tags = document.createElement('div');
        tags.className = 'public-todo__chips';
        for (const tag of todo.tags)
            tags.append(chip(tag.name, tag.color, 'tag'));
        container.append(field('todo.fields.tags', tags));
    }
    const priority = todo.priorityKey ? ctx.priorities[todo.priorityKey] : undefined;
    if (priority) {
        container.append(field('todo.fields.priority', chip(priority.name, priority.color, 'card__priority')));
    }
    if (ctx.showPoints && todo.estimationPoints != null) {
        const points = document.createElement('span');
        points.className = 'card__points';
        points.textContent = String(todo.estimationPoints);
        container.append(field('todo.fields.estimationPoints', points));
    }
    if (todo.sprintNumber != null) {
        const sprint = ctx.sprints.find((s) => s.number === todo.sprintNumber);
        const value = document.createElement('span');
        value.textContent = sprint ? `${sprint.name} (${sprint.number})` : `#${todo.sprintNumber}`;
        container.append(field('todo.fields.sprint', value));
    }
    const linksEl = renderLinks(links, ctx);
    if (linksEl)
        container.append(field('todo.fields.linkedStories', linksEl));
}
function setLoading(dialog, localId) {
    const { title, body } = parts(dialog);
    title.textContent = `#${localId}`;
    const loading = document.createElement('div');
    loading.className = 'muted';
    loading.setAttribute('role', 'status');
    loading.setAttribute('data-i18n-text', 'publicBoard.loading');
    loading.textContent = t('publicBoard.loading');
    body.replaceChildren(loading);
}
function closeProgrammatically() {
    activeContext = null;
    if (dialogEl?.open)
        dialogEl.close();
}
/**
 * Opens (or switches) the read-only story view. Failures close the dialog so a
 * stale or partial story is never left on screen.
 */
export async function openPublicTodo(ctx, localId) {
    const dialog = ensureDialog();
    controller?.abort();
    const myController = new AbortController();
    controller = myController;
    const myGeneration = ++generation;
    activeContext = ctx;
    openLocalId = localId;
    setLoading(dialog, localId);
    const { close } = parts(dialog);
    close.setAttribute('aria-label', t('common.close'));
    if (!dialog.open)
        dialog.showModal();
    close.focus();
    let todo;
    let links;
    try {
        [todo, links] = await Promise.all([
            fetchPublicTodo(ctx.slug, localId, { signal: myController.signal }),
            fetchPublicTodoLinks(ctx.slug, localId, { signal: myController.signal }),
        ]);
    }
    catch (err) {
        if (myGeneration !== generation)
            return 'stale';
        if (isPublicAbort(err))
            return 'stale';
        closeProgrammatically();
        return isPublicNotFound(err) ? 'unavailable' : 'failed';
    }
    if (myGeneration !== generation || !dialog.open)
        return 'stale';
    const { title, body } = parts(dialog);
    title.textContent = `#${todo.localId} ${todo.title}`;
    await renderPublicTodoContent(body, todo, links, ctx);
    if (myGeneration !== generation)
        return 'stale';
    return 'opened';
}
export function getOpenPublicTodoLocalId() {
    return dialogEl?.open ? openLocalId : null;
}
/** Closes without invoking the user-close callback (navigation, revocation). */
export function closePublicTodo() {
    controller?.abort();
    controller = null;
    generation++;
    openLocalId = null;
    closeProgrammatically();
    dialogEl?.querySelector('[data-public-todo-body]')?.replaceChildren();
    const title = dialogEl?.querySelector('#publicTodoDialogTitle');
    if (title)
        title.textContent = '';
}
