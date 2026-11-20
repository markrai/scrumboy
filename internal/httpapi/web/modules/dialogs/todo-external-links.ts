import { apiFetch } from '../api.js';
import { initializeTodoGitHubItems, resetTodoGitHubItems, getPendingTodoGitHubURLs, commitPendingTodoGitHubItems } from './todo-github-items.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import { showToast } from '../utils.js';
import { getTodoFormPermissions } from './todo-permissions.js';

type ExternalResource = {
  id: number;
  connectionId: number;
  kind: string;
  externalId: string;
  name: string;
  url: string;
  availability?: 'available' | 'unavailable';
};

let generation = 0;
let lifecycleBound = false;
let stagedResources: ExternalResource[] = [];

function renderExternalResources(slug: string, localId: number | null, linked: ExternalResource[], available: ExternalResource[], editable: boolean): void {
  const container = document.getElementById('externalResourcesChips');
  const controls = document.getElementById('externalResourceControls');
  const picker = document.getElementById('externalResourcePicker') as HTMLSelectElement | null;
  const add = document.getElementById('addExternalResourceBtn') as HTMLButtonElement | null;
  if (!container || !controls || !picker || !add) return;

  container.replaceChildren();
  const linkedIds = new Set(linked.map(item => item.id));

  for (const item of linked) {
    const chip = document.createElement('span');
    chip.className = 'tag-chip';
    const anchor = document.createElement('a');
    anchor.className = 'tag-chip-link';
    anchor.href = item.url;
    anchor.target = '_blank';
    anchor.rel = 'noopener noreferrer';
    anchor.textContent = item.name;
    chip.append(anchor);
    if (item.availability === 'unavailable') {
      chip.classList.add('tag-chip--unavailable');
      const marker = document.createElement('span');
      marker.className = 'tag-chip-unavailable';
      marker.textContent = ' (Unavailable)';
      marker.title = 'Repository is no longer accessible through the GitHub App';
      chip.append(marker);
    }

    if (editable) {
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'tag-chip-remove';
      remove.textContent = '×';
      remove.setAttribute('aria-label', t('todo.externalResources.remove'));
      remove.addEventListener('click', async () => {
        if (localId === null) {
          stagedResources = stagedResources.filter(resource => resource.id !== item.id);
          renderExternalResources(slug, null, stagedResources, available, editable);
          return;
        }
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${encodeURIComponent(slug)}/todos/${localId}/integrations/${item.id}`, { method: 'DELETE' });
          await loadExternalResources(slug, localId, editable);
        } catch (err) {
          showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
        }
      });
      chip.append(remove);
    }
    container.append(chip);
  }

  picker.replaceChildren();
  for (const item of available.filter(resource => !linkedIds.has(resource.id) && resource.availability !== 'unavailable')) {
    const option = document.createElement('option');
    option.value = String(item.id);
    option.textContent = item.name;
    picker.append(option);
  }

  const canAdd = editable && picker.options.length > 0;
  controls.hidden = !canAdd;
  picker.disabled = !canAdd;
  add.disabled = !canAdd;
  add.onclick = async () => {
    const id = Number(picker.value);
    if (!id) return;
    if (localId === null) {
      const selected = available.find(resource => resource.id === id && resource.availability !== 'unavailable');
      if (!selected) return;
      stagedResources = [...stagedResources, selected];
      renderExternalResources(slug, null, stagedResources, available, editable);
      return;
    }
    try {
      recordLocalMutation();
      await apiFetch(`/api/board/${encodeURIComponent(slug)}/todos/${localId}/integrations`, {
        method: 'POST',
        body: JSON.stringify({ resourceId: id }),
      });
      await loadExternalResources(slug, localId, editable);
    } catch (err) {
      showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
    }
  };

  if (localId === null && linked.length > 0) {
    const hint = document.createElement('p');
    hint.className = 'muted';
    hint.textContent = 'Selected repositories will be linked when the Todo is saved.';
    container.append(hint);
  }
  if (linked.length === 0) {
    const empty = document.createElement('span');
    empty.className = 'muted';
    empty.textContent = t('todo.externalResources.empty');
    container.append(empty);
  }
}

async function loadExternalResources(slug: string, localId: number, editable: boolean): Promise<void> {
  const request = ++generation;
  const path = `/api/board/${encodeURIComponent(slug)}`;
  const [linked, available] = await Promise.all([
    apiFetch<ExternalResource[]>(`${path}/todos/${localId}/integrations`),
    apiFetch<ExternalResource[]>(`${path}/integrations/resources`),
  ]);
  if (request !== generation) return;
  renderExternalResources(slug, localId, Array.isArray(linked) ? linked : [], Array.isArray(available) ? available : [], editable);
}

export async function initializeTodoExternalLinks(slug: string, localId: number, archived: boolean): Promise<void> {
  const field = document.getElementById('todoExternalResourcesField');
  if (!field) return;
  field.style.display = '';
  const editable = !archived && getTodoFormPermissions().canEditLinks;
  await loadExternalResources(slug, localId, editable);
  void initializeTodoGitHubItems(slug, localId, editable).catch(err => {
    resetTodoGitHubItems();
    showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
  });
}

export async function initializeTodoExternalLinksForCreate(slug: string): Promise<void> {
  const field = document.getElementById('todoExternalResourcesField');
  if (!field) return;
  field.style.display = '';
  stagedResources = [];
  const request = ++generation;
  const base = '/api/board/' + encodeURIComponent(slug);
  const available = await apiFetch<ExternalResource[]>(base + '/integrations/resources');
  if (request !== generation) return;
  renderExternalResources(slug, null, stagedResources, Array.isArray(available) ? available : [], true);
  void initializeTodoGitHubItems(slug, null, true).catch(err => {
    resetTodoGitHubItems();
    showToast(apiErrorMessage(err, { fallbackKey: 'todo.externalResources.failed' }));
  });
}

export function pendingTodoExternalLinksSignature(): string {
  return JSON.stringify({
    resources: stagedResources.map(resource => resource.id),
    githubURLs: getPendingTodoGitHubURLs(),
  });
}

export async function commitPendingTodoExternalLinks(slug: string, localId: number): Promise<number> {
  const base = '/api/board/' + encodeURIComponent(slug) + '/todos/' + localId;
  let failed = 0;
  for (const resource of stagedResources) {
    try {
      recordLocalMutation();
      await apiFetch(base + '/integrations', {
        method: 'POST',
        body: JSON.stringify({ resourceId: resource.id }),
      });
    } catch {
      failed++;
    }
  }
  failed += await commitPendingTodoGitHubItems(slug, localId);
  return failed;
}

export function resetTodoExternalLinks(): void {
  resetTodoGitHubItems();
  stagedResources = [];
  ++generation;
  const field = document.getElementById('todoExternalResourcesField');
  if (field) field.style.display = 'none';
  document.getElementById('externalResourcesChips')?.replaceChildren();
  const add = document.getElementById('addExternalResourceBtn') as HTMLButtonElement | null;
  if (add) add.onclick = null;
}

export function bindTodoExternalLinksLifecycle(): void {
  if (lifecycleBound) return;
  const dialog = document.getElementById('todoDialog');
  if (!dialog) return;
  lifecycleBound = true;
  dialog.addEventListener('close', resetTodoExternalLinks);
}
