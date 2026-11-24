import { apiFetch } from '../api.js';
import { showConfirmDialog, showToast } from '../utils.js';
const eventChoices = [
    ['issue.opened', 'Issue opened'],
    ['issue.closed', 'Issue closed'],
    ['pull_request.opened', 'Pull request opened'],
    ['pull_request.closed', 'Pull request closed without merge'],
    ['pull_request.merged', 'Pull request merged'],
];
function append(parent, tag, label, className) {
    const el = document.createElement(tag);
    if (label !== undefined)
        el.textContent = label;
    if (className)
        el.className = className;
    parent.append(el);
    return el;
}
function makeButton(parent, label, run, options) {
    const button = append(parent, 'button', label, 'btn' + (options?.danger ? ' btn--danger' : ''));
    button.type = 'button';
    button.disabled = !!options?.disabled;
    button.addEventListener('click', run);
    return button;
}
function labeledInput(parent, name, value = '') {
    const label = append(parent, 'label', name, 'field');
    const input = append(label, 'input');
    input.className = 'input';
    input.type = 'text';
    input.maxLength = 100;
    input.required = true;
    input.value = value;
    input.setAttribute('aria-label', name);
    return input;
}
function labeledSelect(parent, name, options, selected = '') {
    const label = append(parent, 'label', name, 'field');
    const select = append(label, 'select');
    select.className = 'select';
    select.setAttribute('aria-label', name);
    for (const [value, text] of options) {
        const option = append(select, 'option', text);
        option.value = value;
    }
    if (selected)
        select.value = selected;
    return select;
}
function validRules(value) {
    if (!value || typeof value !== 'object')
        return false;
    const result = value;
    if (typeof result.executionAvailable !== 'boolean' || !Array.isArray(result.rules))
        return false;
    return result.rules.every((item) => {
        if (!item || typeof item !== 'object')
            return false;
        const rule = item;
        return Number.isSafeInteger(rule.id) && rule.id > 0 &&
            Number.isSafeInteger(rule.resourceId) && rule.resourceId > 0 &&
            typeof rule.name === 'string' && typeof rule.eventType === 'string' &&
            rule.actionType === 'notify' && typeof rule.enabled === 'boolean' &&
            typeof rule.updatedAt === 'string';
    });
}
function validList(value, key) {
    if (!value || typeof value !== 'object')
        return false;
    return Array.isArray(value[key]);
}
function relativeDate(raw) {
    if (typeof raw !== 'string')
        return 'Unknown time';
    const parsed = Date.parse(raw);
    return Number.isFinite(parsed) ? new Date(parsed).toLocaleString() : 'Unknown time';
}
function eventLabel(value) {
    return eventChoices.find(([event]) => event === value)?.[1] || 'Unknown event';
}
function errorText(error) {
    const value = error;
    if (value?.status === 403 || value?.status === 404)
        return 'Permission changed. Refresh the project or ask a Maintainer.';
    if (value?.status === 409)
        return 'This rule changed or is unavailable. The latest settings have been reloaded.';
    return 'The request could not be confirmed. The latest server state has been reloaded.';
}
export function renderAutomationTabShell() {
    return '<section class="settings-automations" data-automation-root aria-label="Project automations"><p class="muted" role="status">Loading automations…</p></section>';
}
export function bindAutomationTab(options) {
    const root = document.querySelector('[data-automation-root]');
    if (!root)
        return;
    const { slug, canManage, signal } = options;
    const base = '/api/board/' + encodeURIComponent(slug) + '/integrations';
    let data = null;
    let loading = false;
    let failed = false;
    let forbidden = false;
    let busy = false;
    let generation = 0;
    let editingId = null;
    const previews = new Map();
    const live = () => !signal.aborted && root.isConnected;
    function render() {
        if (!live())
            return;
        root.replaceChildren();
        append(root, 'h3', 'Automations', 'settings-section__title');
        append(root, 'p', 'React to verified GitHub issue and pull-request events. Notifications are in-app records only; automations never edit cards, visibility or GitHub.', 'muted');
        const toolbar = append(root, 'div', undefined, 'settings-automations__toolbar');
        makeButton(toolbar, 'Refresh', () => { void load(); }, { disabled: busy || loading });
        if (loading && !data) {
            append(root, 'p', 'Loading automation rules…', 'muted').setAttribute('role', 'status');
            return;
        }
        if (forbidden) {
            append(root, 'p', 'Project automation settings are unavailable for this account.', 'muted').setAttribute('role', 'alert');
            return;
        }
        if (failed || !data) {
            append(root, 'p', 'Could not load automation settings. Retry to check the server again.', 'muted').setAttribute('role', 'alert');
            return;
        }
        const snapshot = data;
        if (snapshot.readiness && typeof snapshot.readiness.message === 'string') {
            const indicator = append(root, 'p', snapshot.readiness.message, 'settings-automations__notice');
            indicator.setAttribute('role', 'status');
            indicator.setAttribute('data-github-readiness', String(snapshot.readiness.state || 'unknown'));
        }
        append(root, 'p', 'Enabled rules create notifications for new matching GitHub events. Preview checks a rule without triggering an action.', 'settings-automations__notice');
        const repositories = snapshot.repositories.filter(repo => repo.availability === 'available');
        const repositoryNames = new Map(snapshot.repositories.map(repo => [repo.id, repo.name]));
        const choices = repositories.map(repo => [String(repo.id), repo.name]);
        if (canManage) {
            const create = append(root, 'section', undefined, 'settings-automations__section');
            append(create, 'h4', 'Create rule');
            if (!choices.length) {
                append(create, 'p', 'No available repositories are linked to this project. Link a repository in Settings → GitHub before creating a rule.', 'muted');
            }
            else {
                const form = append(create, 'form', undefined, 'settings-automations__form');
                const name = labeledInput(form, 'Rule name');
                const repo = labeledSelect(form, 'GitHub repository', choices);
                const event = labeledSelect(form, 'When', eventChoices);
                append(form, 'p', 'Action: record an in-app notification. New rules start disabled.', 'muted');
                const submit = append(form, 'button', 'Create Rule', 'btn');
                submit.type = 'submit';
                submit.disabled = busy;
                form.addEventListener('submit', (e) => {
                    e.preventDefault();
                    if (busy || !name.value.trim() || !Number(repo.value))
                        return;
                    void mutate(base + '/automation-rules', 'POST', {
                        name: name.value.trim(), resourceId: Number(repo.value),
                        eventType: event.value, actionType: 'notify',
                    }, 'Rule created. Enable it to start recording future matching events.');
                }, { signal });
            }
        }
        const rulesSection = append(root, 'section', undefined, 'settings-automations__section');
        append(rulesSection, 'h4', 'Rules');
        if (!snapshot.rules.length)
            append(rulesSection, 'p', 'No rules yet.', 'muted');
        for (const rule of snapshot.rules) {
            const row = append(rulesSection, 'article', undefined, 'settings-automations__rule');
            const heading = append(row, 'div', undefined, 'settings-automations__rule-head');
            append(heading, 'strong', rule.name);
            const status = append(heading, 'span', rule.enabled ? 'Enabled' : 'Disabled', 'settings-automations__status');
            status.setAttribute('data-enabled', String(rule.enabled));
            append(row, 'p', (repositoryNames.get(rule.resourceId) || 'Repository unavailable') + ' · ' + eventLabel(rule.eventType), 'muted');
            const controls = append(row, 'div', undefined, 'settings-automations__controls');
            makeButton(controls, 'Preview', () => { void preview(rule); }, { disabled: busy });
            if (canManage) {
                makeButton(controls, editingId === rule.id ? 'Cancel edit' : 'Edit', () => {
                    editingId = editingId === rule.id ? null : rule.id;
                    render();
                }, { disabled: busy });
                makeButton(controls, rule.enabled ? 'Disable' : 'Enable', () => {
                    void mutate(base + '/automation-rules/' + rule.id, 'PATCH', { enabled: !rule.enabled }, rule.enabled ? 'Rule disabled; pending notifications will be cancelled.' : 'Rule enabled; future matching GitHub events will create in-app notifications.');
                }, { disabled: busy });
                makeButton(controls, 'Delete', () => { void remove(rule); }, { disabled: busy, danger: true });
            }
            const previewText = previews.get(rule.id);
            if (previewText) {
                const previewEl = append(row, 'p', previewText, 'settings-automations__preview');
                previewEl.setAttribute('role', 'status');
            }
            if (canManage && editingId === rule.id) {
                const form = append(row, 'form', undefined, 'settings-automations__form');
                const name = labeledInput(form, 'Rule name', rule.name);
                const repo = labeledSelect(form, 'GitHub repository', choices, String(rule.resourceId));
                if (!repositories.some(item => item.id === rule.resourceId)) {
                    const unavailable = append(repo, 'option', repositoryNames.get(rule.resourceId) || 'Unavailable repository');
                    unavailable.value = String(rule.resourceId);
                    unavailable.disabled = true;
                    repo.value = unavailable.value;
                }
                const event = labeledSelect(form, 'When', eventChoices, rule.eventType);
                append(form, 'p', 'Saving an enabled rule starts a new version; pending actions for the old version are cancelled.', 'muted');
                const submit = append(form, 'button', 'Save changes', 'btn');
                submit.type = 'submit';
                submit.disabled = busy;
                form.addEventListener('submit', (e) => {
                    e.preventDefault();
                    if (busy || !name.value.trim() || !Number(repo.value))
                        return;
                    editingId = null;
                    void mutate(base + '/automation-rules/' + rule.id, 'PATCH', {
                        name: name.value.trim(), resourceId: Number(repo.value), eventType: event.value,
                    }, 'Rule updated. Future matching events will use the new configuration.');
                }, { signal });
            }
        }
        const activity = append(root, 'section', undefined, 'settings-automations__section');
        append(activity, 'h4', 'Notification history');
        append(activity, 'p', 'Completed means an in-app notification record was created. This does not currently display a browser pop-up, send email or push a notification.', 'muted');
        if (!snapshot.actions.length)
            append(activity, 'p', 'No notifications recorded. Check Recent matches to see whether GitHub delivered your event.', 'muted');
        for (const item of snapshot.actions) {
            const row = append(activity, 'div', undefined, 'settings-automations__activity');
            append(row, 'strong', eventLabel(item.eventType) + ' #' + item.itemNumber);
            append(row, 'span', ' · ' + item.status + ' · attempts: ' + item.attempts + ' · ' + relativeDate(item.completedAt || item.createdAt), 'muted');
            if (item.errorClass)
                append(row, 'span', ' · ' + item.errorClass, 'muted');
            const details = append(row, 'div', undefined, 'settings-automations__audit');
            makeButton(row, 'Audit details', () => { void showAudit(item.id, details); }, { disabled: busy });
        }
        const matches = append(root, 'section', undefined, 'settings-automations__section');
        append(matches, 'h4', 'Recent matches');
        append(matches, 'p', 'Matches come from real GitHub webhook deliveries. If your new issue does not appear here, verify the GitHub App Issues subscription and its webhook destination. A match does not guarantee a notification was created.', 'muted');
        if (!snapshot.matches.length)
            append(matches, 'p', 'No matching GitHub events recorded. Verify the GitHub App Issues/Pull request subscriptions and the webhook destination for this deployment.', 'muted');
        for (const item of snapshot.matches) {
            append(matches, 'div', eventLabel(item.eventType) + ' #' + item.itemNumber +
                ' · rule ' + item.ruleId + ' · ' + relativeDate(item.matchedAt), 'settings-automations__activity');
        }
    }
    async function load() {
        if (!live())
            return;
        const current = ++generation;
        loading = true;
        failed = false;
        forbidden = false;
        render();
        try {
            const [repositories, rules, matches, actions] = await Promise.all([
                apiFetch(base + '/resources', { signal }),
                apiFetch(base + '/automation-rules', { signal }),
                apiFetch(base + '/automation-matches?limit=30', { signal }),
                apiFetch(base + '/automation-actions?limit=30', { signal }),
            ]);
            if (!live() || current !== generation)
                return;
            if (!Array.isArray(repositories) || !validRules(rules) ||
                !validList(matches, 'matches') || !validList(actions, 'actions')) {
                throw new Error('Invalid automation API response');
            }
            const nextActions = actions.actions;
            if (data && nextActions.some(action => action.status === 'completed' &&
                !data.actions.some(previous => previous.id === action.id && previous.status === 'completed'))) {
                showToast('GitHub automation: new notification recorded in history.');
            }
            data = {
                repositories: repositories,
                rules: rules.rules,
                matches: matches.matches,
                actions: nextActions,
                readiness: rules.readiness,
            };
            previews.clear();
        }
        catch (error) {
            if (!live() || current !== generation)
                return;
            data = null;
            failed = true;
            const status = error?.status;
            forbidden = status === 403 || status === 404;
        }
        finally {
            if (live() && current === generation) {
                loading = false;
                render();
            }
        }
    }
    async function mutate(path, method, body, success) {
        if (busy || !live())
            return;
        busy = true;
        render();
        try {
            await apiFetch(path, { method, signal, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
            if (live())
                showToast(success);
        }
        catch (error) {
            if (live())
                showToast(errorText(error));
        }
        finally {
            busy = false;
            if (live())
                await load();
        }
    }
    async function preview(rule) {
        if (busy || !live())
            return;
        try {
            const response = await apiFetch(base + '/automation-rules/' + rule.id + '/preview', {
                method: 'POST', signal, body: JSON.stringify({ resourceId: rule.resourceId, eventType: rule.eventType }),
            });
            if (!live())
                return;
            if (!response || response.sampleOnly !== true || typeof response.matched !== 'boolean' ||
                response.wouldExecute !== false || typeof response.reason !== 'string') {
                throw new Error('Invalid preview response');
            }
            const status = response.reason === 'ready_for_new_events'
                ? 'Ready: future verified ' + eventLabel(rule.eventType).toLowerCase() + ' events will create in-app notifications.'
                : response.reason === 'rule_disabled' ? 'This event matches, but the rule is disabled. Enable it to receive notifications.'
                    : response.reason === 'resource_unavailable' ? 'The linked repository is unavailable. Reconnect it before testing.'
                        : 'This event does not match the rule.';
            previews.set(rule.id, status + ' This is a configuration check only; no GitHub event or notification was sent.');
            render();
        }
        catch (error) {
            if (live())
                showToast(errorText(error));
        }
    }
    async function remove(rule) {
        if (!canManage || busy)
            return;
        const confirmed = await showConfirmDialog('Delete “' + rule.name + '” and remove its stored matching history? This cannot be undone.', 'Delete automation rule', 'Delete', 'danger');
        if (confirmed && live()) {
            editingId = null;
            await mutate(base + '/automation-rules/' + rule.id, 'DELETE', undefined, 'Automation rule deleted.');
        }
    }
    async function showAudit(actionID, target) {
        if (!live() || !target.isConnected || target.dataset.loading === 'true')
            return;
        if (target.childElementCount) {
            target.replaceChildren();
            return;
        }
        target.dataset.loading = 'true';
        append(target, 'p', 'Loading audit…', 'muted');
        try {
            const response = await apiFetch(base + '/automation-actions/' + actionID + '/audit?limit=30', { signal });
            if (!live() || !target.isConnected)
                return;
            if (!response || !Array.isArray(response.audit))
                throw new Error('Invalid audit response');
            target.replaceChildren();
            if (!response.audit.length)
                append(target, 'p', 'No audit transitions available.', 'muted');
            for (const entry of response.audit) {
                append(target, 'p', entry.transition + ' · ' + relativeDate(entry.createdAt) +
                    (entry.errorClass ? ' · ' + entry.errorClass : ''), 'muted');
            }
        }
        catch (error) {
            if (live() && target.isConnected) {
                target.replaceChildren();
                append(target, 'p', 'Audit unavailable. Retry.', 'muted');
            }
        }
        finally {
            delete target.dataset.loading;
        }
    }
    void load();
    const poll = window.setInterval(() => {
        if (live() && !busy && !loading)
            void load();
    }, 10000);
    signal.addEventListener('abort', () => window.clearInterval(poll), { once: true });
}
