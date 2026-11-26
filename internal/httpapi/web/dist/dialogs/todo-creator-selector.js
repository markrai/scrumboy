import { apiFetch } from '../api.js';
import { off, on } from '../events.js';
import { t } from '../i18n/index.js';
import { fetchProjectMembers } from '../members-cache.js';
import { setBoardMembers } from '../state/mutations.js';
import { getUser } from '../state/selectors.js';
let state = null;
const MEMBERS_UPDATED_EVENT = 'members-updated';
function positiveUserId(value) {
    const id = Number(value);
    return Number.isSafeInteger(id) && id > 0 ? id : null;
}
function stringValue(value) {
    return typeof value === 'string' ? value.trim() : '';
}
function memberCandidate(member) {
    const id = positiveUserId(member.userId);
    if (id == null)
        return null;
    return {
        id,
        name: stringValue(member.name),
        email: stringValue(member.email),
        isMember: true,
        isService: member.isService === true,
    };
}
function availableCandidate(user) {
    const id = positiveUserId(user.id);
    if (id == null)
        return null;
    return {
        id,
        name: stringValue(user.name),
        email: stringValue(user.email),
        isMember: false,
        isService: user.isService === true,
    };
}
export function mergeTodoCreatorCandidates(members, availableUsers) {
    const merged = [];
    const seen = new Set();
    for (const member of members) {
        const candidate = memberCandidate(member);
        if (!candidate || seen.has(candidate.id))
            continue;
        seen.add(candidate.id);
        merged.push(candidate);
    }
    for (const user of availableUsers) {
        const candidate = availableCandidate(user);
        if (!candidate || seen.has(candidate.id))
            continue;
        seen.add(candidate.id);
        merged.push(candidate);
    }
    return merged;
}
function creatorElements() {
    return {
        attribution: document.getElementById('todoDialogCreatedBy'),
        button: document.getElementById('todoCreatorEditBtn'),
        reset: document.getElementById('todoCreatorResetBtn'),
        popover: document.getElementById('todoCreatorSelector'),
        search: document.getElementById('todoCreatorSearch'),
        close: document.getElementById('todoCreatorSelectorClose'),
        status: document.getElementById('todoCreatorStatus'),
        options: document.getElementById('todoCreatorOptions'),
    };
}
function candidateName(candidate) {
    return candidate.name || candidate.email || t('todo.creator.userFallback', { id: candidate.id });
}
function effectiveCreatorId(current) {
    return current.pendingId ?? current.originalId;
}
function selectedCandidate(current) {
    const id = effectiveCreatorId(current);
    if (id == null)
        return null;
    return current.candidates.find((candidate) => candidate.id === id)
        ?? (current.originalCandidate?.id === id ? current.originalCandidate : null);
}
function renderAttribution(current) {
    const { attribution, button, reset } = creatorElements();
    if (attribution) {
        const candidate = selectedCandidate(current);
        attribution.textContent = current.todoLocalId == null
            ? ''
            : candidate
                ? t('todo.dialog.openedBy', { name: candidateName(candidate) })
                : effectiveCreatorId(current) == null
                    ? t('todo.creator.unrecordedAttribution')
                    : t('todo.creator.unresolvedAttribution');
    }
    if (button) {
        button.hidden = !current.canEdit;
        if (current.canEdit) {
            button.setAttribute('aria-label', t('todo.creator.edit'));
            button.setAttribute('title', t('todo.creator.edit'));
        }
        else {
            button.removeAttribute('aria-label');
            button.removeAttribute('title');
        }
        button.setAttribute('aria-expanded', current.open ? 'true' : 'false');
    }
    if (reset) {
        const changed = current.canEdit && current.pendingId != null;
        reset.hidden = !changed;
        if (changed) {
            reset.textContent = t('todo.creator.reset');
            reset.setAttribute('aria-label', t('todo.creator.resetLabel'));
            reset.setAttribute('title', t('todo.creator.resetLabel'));
        }
        else {
            reset.textContent = '';
            reset.removeAttribute('aria-label');
            reset.removeAttribute('title');
        }
    }
}
function filteredCandidates(current) {
    const query = current.query.trim().toLocaleLowerCase();
    if (!query)
        return current.candidates;
    return current.candidates.filter((candidate) => [candidate.name, candidate.email, String(candidate.id)]
        .some((value) => value.toLocaleLowerCase().includes(query)));
}
function optionId(current, candidate) {
    return `todoCreatorOption-${current.generation}-${candidate.id}`;
}
function renderOptions(current) {
    const { options, search, status, close, popover } = creatorElements();
    if (!options)
        return;
    if (!current.canEdit) {
        options.replaceChildren();
        if (status)
            status.textContent = '';
        if (search)
            search.setAttribute('aria-expanded', 'false');
        return;
    }
    if (search) {
        search.placeholder = t('todo.creator.searchPlaceholder');
        search.setAttribute('aria-label', t('todo.creator.searchLabel'));
        search.setAttribute('aria-expanded', current.open ? 'true' : 'false');
    }
    if (close)
        close.setAttribute('aria-label', t('common.close'));
    if (popover)
        popover.setAttribute('aria-label', t('todo.creator.selectorLabel'));
    const candidates = filteredCandidates(current);
    if (candidates.length === 0) {
        current.activeIndex = -1;
    }
    else if (current.activeIndex < 0 || current.activeIndex >= candidates.length) {
        const selectedIndex = candidates.findIndex((candidate) => candidate.id === effectiveCreatorId(current));
        current.activeIndex = selectedIndex >= 0 ? selectedIndex : 0;
    }
    const nodes = candidates.map((candidate, index) => {
        const option = document.createElement('div');
        option.id = optionId(current, candidate);
        option.className = 'todo-creator-option';
        option.setAttribute('role', 'option');
        option.setAttribute('aria-selected', candidate.id === effectiveCreatorId(current) ? 'true' : 'false');
        option.tabIndex = -1;
        option.dataset.userId = String(candidate.id);
        option.classList.toggle('todo-creator-option--active', index === current.activeIndex);
        const identity = document.createElement('span');
        identity.className = 'todo-creator-option__identity';
        const name = document.createElement('span');
        name.className = 'todo-creator-option__name';
        name.textContent = candidateName(candidate);
        identity.appendChild(name);
        if (candidate.email && candidate.email !== candidate.name) {
            const email = document.createElement('span');
            email.className = 'todo-creator-option__email muted';
            email.textContent = candidate.email;
            identity.appendChild(email);
        }
        const badges = document.createElement('span');
        badges.className = 'todo-creator-option__badges';
        const membership = document.createElement('span');
        membership.className = 'todo-creator-option__badge';
        membership.textContent = t(candidate.isMember ? 'todo.creator.member' : 'todo.creator.nonMember');
        badges.appendChild(membership);
        if (candidate.isService) {
            const service = document.createElement('span');
            service.className = 'todo-creator-option__badge';
            service.textContent = t('todo.creator.serviceAccount');
            badges.appendChild(service);
        }
        if (candidate.id === effectiveCreatorId(current)) {
            const selected = document.createElement('span');
            selected.className = 'todo-creator-option__selected';
            selected.textContent = `✓ ${t('todo.creator.current')}`;
            badges.appendChild(selected);
        }
        option.append(identity, badges);
        return option;
    });
    options.replaceChildren(...nodes);
    const active = candidates[current.activeIndex];
    if (search) {
        if (active)
            search.setAttribute('aria-activedescendant', optionId(current, active));
        else
            search.removeAttribute('aria-activedescendant');
    }
    if (status) {
        status.classList.toggle('todo-creator-selector__status--error', current.loadFailed);
        status.textContent = current.loading
            ? t('todo.creator.loading')
            : current.loadFailed
                ? t('todo.creator.loadFailed')
                : candidates.length === 0
                    ? t('todo.creator.noResults')
                    : '';
    }
}
function positionSelector(current) {
    if (!current.open)
        return;
    const { button, popover } = creatorElements();
    if (!button || !popover)
        return;
    const visualViewport = window.visualViewport;
    const viewportLeft = visualViewport?.offsetLeft ?? 0;
    const viewportTop = visualViewport?.offsetTop ?? 0;
    const viewportWidth = visualViewport?.width ?? Math.max(document.documentElement.clientWidth, window.innerWidth || 0);
    const viewportHeight = visualViewport?.height ?? Math.max(document.documentElement.clientHeight, window.innerHeight || 0);
    const viewportRight = viewportLeft + viewportWidth;
    const viewportBottom = viewportTop + viewportHeight;
    const width = Math.min(360, Math.max(240, viewportWidth - 16));
    popover.style.width = `${width}px`;
    popover.style.maxHeight = `${Math.max(112, viewportHeight - 16)}px`;
    const anchor = button.getBoundingClientRect();
    const measuredHeight = Math.min(popover.scrollHeight || 320, Math.max(112, viewportHeight - 16));
    const left = Math.min(Math.max(viewportLeft + 8, anchor.left + anchor.width / 2 - width / 2), Math.max(viewportLeft + 8, viewportRight - width - 8));
    const below = anchor.bottom + 8;
    const top = below + measuredHeight <= viewportBottom - 8
        ? below
        : Math.max(viewportTop + 8, Math.min(anchor.top - measuredHeight - 8, viewportBottom - measuredHeight - 8));
    popover.style.left = `${left}px`;
    popover.style.top = `${top}px`;
}
function currentContextMatches(current) {
    const activeUserId = positiveUserId(getUser()?.id);
    return state === current && current.isContextCurrent() && activeUserId === current.authUserId;
}
function clearSelectorSurface(current) {
    const { popover, search, status, options } = creatorElements();
    current.open = false;
    current.query = '';
    current.activeIndex = -1;
    current.isComposing = false;
    try {
        popover?.hidePopover?.();
    }
    catch {
    }
    if (popover) {
        popover.hidden = true;
        popover.classList.remove('todo-creator-selector--open', 'todo-creator-selector--fallback');
    }
    if (search) {
        search.value = '';
        search.removeAttribute('aria-activedescendant');
        search.setAttribute('aria-expanded', 'false');
    }
    if (status) {
        status.textContent = '';
        status.classList.remove('todo-creator-selector__status--error');
    }
    options?.replaceChildren();
}
function invalidateCreatorEditing(current) {
    if (state !== current)
        return;
    current.requestAbort?.abort();
    current.requestAbort = null;
    current.loadSequence += 1;
    current.authorizationSequence += 1;
    current.loading = false;
    current.loadFailed = false;
    current.availableLoaded = false;
    current.canEdit = false;
    current.pendingId = null;
    current.memberCandidates = [];
    current.candidates = [];
    current.bindingsAbort.abort();
    clearSelectorSurface(current);
    renderAttribution(current);
    renderOptions(current);
}
function invalidateIfStale(current) {
    if (!currentContextMatches(current)) {
        if (state === current)
            resetTodoCreatorSelector();
        return true;
    }
    if (current.canEdit && !current.isEditAuthorized()) {
        invalidateCreatorEditing(current);
        return true;
    }
    return false;
}
function authorizationStatus(error) {
    const status = Number(error?.status);
    return Number.isFinite(status) ? status : null;
}
async function refreshAfterMembershipUpdate(current, payload) {
    const projectId = positiveUserId(payload?.projectId);
    if (projectId !== current.projectId || !current.canEdit || invalidateIfStale(current))
        return;
    const sequence = ++current.authorizationSequence;
    try {
        const members = await fetchProjectMembers(projectId);
        if (!currentContextMatches(current) || sequence !== current.authorizationSequence)
            return;
        setBoardMembers(members);
        invalidateIfStale(current);
    }
    catch (error) {
        if (!currentContextMatches(current) || sequence !== current.authorizationSequence)
            return;
        if ([401, 403, 404].includes(authorizationStatus(error) ?? 0)) {
            invalidateCreatorEditing(current);
        }
    }
}
async function loadAvailableCandidates(current) {
    if (!current.canEdit || current.availableLoaded || current.loading || invalidateIfStale(current) || current.projectId == null)
        return;
    current.loading = true;
    current.loadFailed = false;
    current.requestAbort?.abort();
    current.requestAbort = new AbortController();
    const sequence = ++current.loadSequence;
    renderOptions(current);
    try {
        const users = await apiFetch(`/api/projects/${current.projectId}/available-users`, {
            signal: current.requestAbort.signal,
        });
        if (invalidateIfStale(current) || sequence !== current.loadSequence)
            return;
        current.candidates = mergeTodoCreatorCandidates(current.memberCandidates.map((candidate) => ({
            userId: candidate.id,
            name: candidate.name,
            email: candidate.email,
            role: '',
            isService: candidate.isService,
        })), Array.isArray(users) ? users : []);
        current.availableLoaded = true;
        current.loadFailed = false;
        renderAttribution(current);
    }
    catch (error) {
        if (invalidateIfStale(current) || sequence !== current.loadSequence)
            return;
        if (error instanceof DOMException && error.name === 'AbortError')
            return;
        if ([401, 403, 404].includes(authorizationStatus(error) ?? 0)) {
            invalidateCreatorEditing(current);
            return;
        }
        current.loading = false;
        current.loadFailed = true;
        renderOptions(current);
        positionSelector(current);
    }
    finally {
        if (!invalidateIfStale(current) && sequence === current.loadSequence) {
            current.loading = false;
            renderOptions(current);
            positionSelector(current);
        }
    }
}
function showSelector(current) {
    if (!current.canEdit || invalidateIfStale(current))
        return;
    const { popover, search, button } = creatorElements();
    if (!popover || !search || !button)
        return;
    current.open = true;
    current.query = '';
    current.activeIndex = current.candidates.findIndex((candidate) => candidate.id === effectiveCreatorId(current));
    search.value = '';
    popover.hidden = false;
    popover.classList.add('todo-creator-selector--open');
    try {
        popover.showPopover?.();
    }
    catch {
        popover.classList.add('todo-creator-selector--fallback');
    }
    renderAttribution(current);
    renderOptions(current);
    positionSelector(current);
    search.focus();
    void loadAvailableCandidates(current);
}
function hideSelector(current, restoreFocus) {
    const { popover, button, search } = creatorElements();
    current.open = false;
    try {
        popover?.hidePopover?.();
    }
    catch {
    }
    if (popover) {
        popover.hidden = true;
        popover.classList.remove('todo-creator-selector--open', 'todo-creator-selector--fallback');
    }
    search?.setAttribute('aria-expanded', 'false');
    renderAttribution(current);
    if (restoreFocus)
        button?.focus();
}
function selectCandidate(current, id) {
    if (!current.canEdit || invalidateIfStale(current))
        return;
    if (!current.candidates.some((candidate) => candidate.id === id))
        return;
    current.pendingId = id === current.originalId ? null : id;
    renderAttribution(current);
    renderOptions(current);
    hideSelector(current, true);
}
function moveActive(current, delta) {
    if (invalidateIfStale(current))
        return;
    const candidates = filteredCandidates(current);
    if (candidates.length === 0)
        return;
    current.activeIndex = current.activeIndex < 0
        ? 0
        : (current.activeIndex + delta + candidates.length) % candidates.length;
    renderOptions(current);
    document.getElementById(optionId(current, candidates[current.activeIndex]))?.scrollIntoView?.({ block: 'nearest' });
}
function moveActiveTo(current, index) {
    if (invalidateIfStale(current))
        return;
    const candidates = filteredCandidates(current);
    if (candidates.length === 0)
        return;
    current.activeIndex = Math.min(Math.max(index, 0), candidates.length - 1);
    renderOptions(current);
    document.getElementById(optionId(current, candidates[current.activeIndex]))?.scrollIntoView?.({ block: 'nearest' });
}
function restoreOriginalCreator(current) {
    if (!current.canEdit || invalidateIfStale(current))
        return;
    current.pendingId = null;
    renderAttribution(current);
    renderOptions(current);
}
function bindCreatorSelector(current) {
    const { button, reset, popover, search, close, options } = creatorElements();
    const signal = current.bindingsAbort.signal;
    button?.addEventListener('click', () => {
        if (current.open)
            hideSelector(current, false);
        else
            showSelector(current);
    }, { signal });
    button?.addEventListener('keydown', (event) => {
        if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp')
            return;
        event.preventDefault();
        showSelector(current);
        if (event.key === 'ArrowUp')
            moveActive(current, -1);
    }, { signal });
    reset?.addEventListener('click', () => restoreOriginalCreator(current), { signal });
    close?.addEventListener('click', () => hideSelector(current, true), { signal });
    search?.addEventListener('input', () => {
        if (invalidateIfStale(current))
            return;
        current.query = search.value;
        current.activeIndex = 0;
        renderOptions(current);
    }, { signal });
    search?.addEventListener('compositionstart', () => {
        current.isComposing = true;
    }, { signal });
    search?.addEventListener('compositionend', () => {
        current.isComposing = false;
    }, { signal });
    search?.addEventListener('keydown', (event) => {
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            moveActive(current, event.key === 'ArrowDown' ? 1 : -1);
            return;
        }
        if (event.key === 'Home' || event.key === 'End') {
            const candidates = filteredCandidates(current);
            if (candidates.length === 0)
                return;
            event.preventDefault();
            moveActiveTo(current, event.key === 'Home' ? 0 : candidates.length - 1);
            return;
        }
        if (event.key === 'Enter') {
            event.preventDefault();
            event.stopPropagation();
            if (event.isComposing || current.isComposing)
                return;
            const candidate = filteredCandidates(current)[current.activeIndex];
            if (!candidate)
                return;
            selectCandidate(current, candidate.id);
            return;
        }
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            hideSelector(current, true);
        }
    }, { signal });
    options?.addEventListener('click', (event) => {
        const option = event.target?.closest('[data-user-id]');
        const id = positiveUserId(option?.dataset.userId);
        if (id != null)
            selectCandidate(current, id);
    }, { signal });
    document.addEventListener('pointerdown', (event) => {
        if (!current.open || !(event.target instanceof Node))
            return;
        if (popover?.contains(event.target) || button?.contains(event.target))
            return;
        hideSelector(current, false);
    }, { signal });
    window.addEventListener('resize', () => positionSelector(current), { signal });
    window.addEventListener('scroll', () => positionSelector(current), { signal, capture: true });
    window.visualViewport?.addEventListener('resize', () => positionSelector(current), { signal });
    window.visualViewport?.addEventListener('scroll', () => positionSelector(current), { signal });
}
export function configureTodoCreatorSelector(options) {
    resetTodoCreatorSelector();
    const memberCandidates = mergeTodoCreatorCandidates(options.members, []);
    const originalId = positiveUserId(options.originalCreatorUserId);
    const originalCandidate = memberCandidates.find((candidate) => candidate.id === originalId) ?? null;
    const projectId = positiveUserId(options.projectId);
    const todoLocalId = positiveUserId(options.todoLocalId);
    const authUserId = positiveUserId(options.authUserId);
    const canEdit = options.canEdit && projectId != null && todoLocalId != null && authUserId != null && options.isEditAuthorized();
    let next;
    const authorizationHandler = (payload) => {
        void refreshAfterMembershipUpdate(next, payload);
    };
    next = {
        generation: options.generation,
        projectId,
        todoLocalId,
        authUserId,
        canEdit,
        originalId,
        originalCandidate,
        pendingId: null,
        memberCandidates,
        candidates: memberCandidates,
        availableLoaded: false,
        loading: false,
        loadFailed: false,
        loadSequence: 0,
        authorizationSequence: 0,
        requestAbort: null,
        bindingsAbort: new AbortController(),
        query: '',
        activeIndex: -1,
        isComposing: false,
        open: false,
        isContextCurrent: options.isContextCurrent,
        isEditAuthorized: options.isEditAuthorized,
        authorizationHandler,
    };
    state = next;
    on(MEMBERS_UPDATED_EVENT, authorizationHandler);
    bindCreatorSelector(next);
    renderAttribution(next);
    renderOptions(next);
}
export function getPendingTodoCreatorId() {
    const current = state;
    if (!current || invalidateIfStale(current))
        return null;
    return current.pendingId;
}
export function refreshTodoCreatorSelectorLocale() {
    const current = state;
    if (!current || invalidateIfStale(current))
        return;
    renderAttribution(current);
    renderOptions(current);
}
export function refreshTodoCreatorSelectorAuthorization() {
    const current = state;
    if (!current)
        return;
    invalidateIfStale(current);
}
export function resetTodoCreatorSelector() {
    const current = state;
    if (current) {
        off(MEMBERS_UPDATED_EVENT, current.authorizationHandler);
        current.requestAbort?.abort();
        current.bindingsAbort.abort();
        current.loadSequence += 1;
        current.authorizationSequence += 1;
        current.open = false;
        current.query = '';
        current.memberCandidates = [];
        current.candidates = [];
        current.pendingId = null;
        current.originalId = null;
        current.originalCandidate = null;
    }
    state = null;
    const { attribution, popover, button, reset, search, status, options } = creatorElements();
    try {
        popover?.hidePopover?.();
    }
    catch {
    }
    if (popover) {
        popover.hidden = true;
        popover.classList.remove('todo-creator-selector--open', 'todo-creator-selector--fallback');
    }
    if (attribution)
        attribution.textContent = '';
    if (options)
        options.replaceChildren();
    if (status) {
        status.textContent = '';
        status.classList.remove('todo-creator-selector__status--error');
    }
    if (search) {
        search.value = '';
        search.placeholder = '';
        search.removeAttribute('aria-activedescendant');
        search.removeAttribute('aria-label');
    }
    if (button) {
        button.hidden = true;
        button.removeAttribute('aria-label');
        button.removeAttribute('title');
    }
    if (reset) {
        reset.hidden = true;
        reset.textContent = '';
        reset.removeAttribute('aria-label');
        reset.removeAttribute('title');
    }
    button?.setAttribute('aria-expanded', 'false');
    search?.setAttribute('aria-expanded', 'false');
}
