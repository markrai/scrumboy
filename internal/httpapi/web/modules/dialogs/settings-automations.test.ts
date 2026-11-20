// @vitest-environment happy-dom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';

const { requests, confirm, toast } = vi.hoisted(() => ({
  requests: vi.fn(),
  confirm: vi.fn(),
  toast: vi.fn(),
}));

vi.mock('../api.js', () => ({ apiFetch: requests }));
vi.mock('../utils.js', () => ({ showConfirmDialog: confirm, showToast: toast }));

import { bindAutomationTab, renderAutomationTabShell } from './settings-automations.js';

const base = '/api/board/my-project/integrations';
const version = '2026-11-19T12:00:00.000Z';

function rule(overrides: Record<string, unknown> = {}) {
  return {
    id: 7,
    projectId: 2,
    resourceId: 42,
    name: 'Merged PR notification',
    eventType: 'pull_request.merged',
    actionType: 'notify',
    enabled: true,
    updatedAt: version,
    executionApprovedAt: null,
    ...overrides,
  };
}

function fixtures(settings: {
  canManage?: boolean;
  rules?: any[];
  available?: boolean;
  repositories?: any[];
  actions?: any[];
  matches?: any[];
} = {}) {
  const rules = settings.rules ?? [rule()];
  requests.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
    if (path === base + '/resources') {
      return settings.repositories ?? [{ id: 42, name: 'org/repo', availability: 'available' }];
    }
    if (path === base + '/automation-rules' && !options?.method) {
      return { rules, executionAvailable: settings.available ?? false };
    }
    if (path === base + '/automation-matches?limit=30') {
      return { matches: settings.matches ?? [] };
    }
    if (path === base + '/automation-actions?limit=30') {
      return { actions: settings.actions ?? [] };
    }
    if (path.endsWith('/preview')) {
      return { ruleId: 7, matched: true, enabled: true, sampleOnly: true, executionAvailable: false, wouldExecute: false, reason: 'execution_not_approved' };
    }
    if (path.includes('/audit?limit=30')) {
      return { audit: [
        { transition: 'completed', createdAt: version },
        { transition: 'claimed', createdAt: version },
        { transition: 'queued', createdAt: version },
      ] };
    }
    if (options?.method === 'POST' && path === base + '/automation-rules') {
      rules.push(rule({ id: 8, enabled: false, name: JSON.parse(options.body || '{}').name }));
      return rules[rules.length - 1];
    }
    if (options?.method === 'POST' && path.endsWith('/execution-approval')) {
      rules[0] = rule({ executionApprovedAt: version });
      return rules[0];
    }
    if (options?.method === 'DELETE' && path.endsWith('/execution-approval')) {
      rules[0] = rule();
      return;
    }
    if (options?.method === 'PATCH') {
      rules[0] = rule({ ...JSON.parse(options.body || '{}'), executionApprovedAt: null });
      return rules[0];
    }
    if (options?.method === 'DELETE') {
      rules.splice(0, 1);
      return;
    }
    throw new Error('Unexpected request ' + path);
  });
}

function mount(canManage: boolean) {
  document.body.innerHTML = renderAutomationTabShell();
  const controller = new AbortController();
  bindAutomationTab({ slug: 'my-project', canManage, signal: controller.signal });
  return controller;
}

function buttons(label: string): HTMLButtonElement[] {
  return Array.from(document.querySelectorAll<HTMLButtonElement>('button'))
    .filter(item => item.textContent === label);
}

function findButton(label: string): HTMLButtonElement {
  const item = buttons(label)[0];
  if (!item) throw new Error('Missing control ' + label);
  return item;
}

describe('project automations settings', () => {
  beforeEach(() => {
    requests.mockReset();
    confirm.mockReset().mockResolvedValue(true);
    toast.mockReset();
    document.body.replaceChildren();
  });
  afterEach(() => { document.body.replaceChildren(); });

  it('renders viewer-safe rules, previews, match and notification history', async () => {
    fixtures({
      actions: [{ id: 9, ruleId: 7, resourceId: 42, itemNumber: 51, eventType: 'pull_request.merged',
        status: 'completed', attempts: 1, createdAt: version, completedAt: version }],
      matches: [{ id: 8, ruleId: 7, resourceId: 42, itemNumber: 51, eventType: 'pull_request.merged', matchedAt: version }],
    });
    const controller = mount(false);
    await vi.waitFor(() => expect(document.querySelectorAll('.settings-automations__rule')).toHaveLength(1));
    expect(document.body.textContent).toContain('Execution is off at the server');
    expect(document.body.textContent).toContain('Notifications unavailable: server execution is off');
    expect(document.body.textContent).toContain('Notification history');
    expect(document.body.textContent).toContain('Recent matches');
    expect(buttons('Create Rule')).toHaveLength(0);
    expect(buttons('Enable')).toHaveLength(0);
    expect(buttons('Disable')).toHaveLength(0);
    expect(buttons('Approve execution')).toHaveLength(0);
    expect(buttons('Delete')).toHaveLength(0);
    findButton('Preview').click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('Sample-only preview: matched'));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules/7/preview', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ resourceId: 42, eventType: 'pull_request.merged' }),
    }));
    findButton('Audit details').click();
    await vi.waitFor(() => expect(document.body.textContent).toContain('queued'));
    expect(requests).toHaveBeenCalledWith(base + '/automation-actions/9/audit?limit=30', expect.any(Object));
    controller.abort();
  });

  it('escapes untrusted names and refuses malformed rule payloads', async () => {
    fixtures({
      rules: [rule({ name: '<img src=x onerror=alert(1)>' })],
      repositories: [{ id: 42, name: '<svg onload=alert(1)>', availability: 'available' }],
    });
    const controller = mount(false);
    await vi.waitFor(() => expect(document.querySelector('.settings-automations__rule')).not.toBeNull());
    expect(document.querySelector('img[src="x"]')).toBeNull();
    expect(document.querySelector('svg[onload]')).toBeNull();
    expect(document.body.textContent).toContain('<img src=x onerror=alert(1)>');
    controller.abort();

    requests.mockReset().mockResolvedValue({ rules: [{ id: '7' }], executionAvailable: true });
    const retry = mount(true);
    await vi.waitFor(() => expect(document.body.textContent).toContain('Could not load automation settings'));
    expect(buttons('Approve execution')).toHaveLength(0);
    retry.abort();
  });

  it('creates disabled rules and never auto-approves when enabling', async () => {
    fixtures({ rules: [] });
    const controller = mount(true);
    await vi.waitFor(() => expect(buttons('Create Rule')).toHaveLength(1));
    const name = document.querySelector<HTMLInputElement>('input[aria-label="Rule name"]');
    expect(name).not.toBeNull();
    name!.value = 'Review merged PRs';
    document.querySelector<HTMLFormElement>('.settings-automations__form')!.dispatchEvent(
      new Event('submit', { cancelable: true, bubbles: true }),
    );
    await vi.waitFor(() => expect(document.querySelectorAll('.settings-automations__rule')).toHaveLength(1));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ name: 'Review merged PRs', resourceId: 42,
        eventType: 'issue.opened', actionType: 'notify' }),
    }));
    expect(buttons('Approve execution')).toHaveLength(0);
    findButton('Enable').click();
    await vi.waitFor(() => expect(buttons('Disable')).toHaveLength(1));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules/8', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ enabled: true }),
    }));
    expect(requests.mock.calls.some(call => String(call[0]).endsWith('/execution-approval'))).toBe(false);
    controller.abort();
  });

  it('requires separate confirmation and passes the exact rule version to execution consent', async () => {
    fixtures({ available: true });
    const controller = mount(true);
    await vi.waitFor(() => expect(buttons('Approve execution')).toHaveLength(1));
    confirm.mockResolvedValueOnce(false);
    findButton('Approve execution').click();
    await vi.waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
    expect(requests.mock.calls.some(call => String(call[0]).endsWith('/execution-approval'))).toBe(false);

    findButton('Approve execution').click();
    await vi.waitFor(() => expect(buttons('Revoke approval')).toHaveLength(1));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules/7/execution-approval', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ confirm: 'APPROVE_NOTIFY_EXECUTION', ruleUpdatedAtMs: Date.parse(version) }),
    }));
    expect(document.body.textContent).toContain('Execution approved');
    findButton('Revoke approval').click();
    await vi.waitFor(() => expect(buttons('Approve execution')).toHaveLength(1));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules/7/execution-approval', expect.objectContaining({ method: 'DELETE' }));
    controller.abort();
  });

  it('edit and disable remain independent of approval and are confirmed by the server', async () => {
    fixtures({ available: true, rules: [rule({ executionApprovedAt: version })] });
    const controller = mount(true);
    await vi.waitFor(() => expect(buttons('Revoke approval')).toHaveLength(1));
    findButton('Edit').click();
    expect(document.querySelectorAll<HTMLInputElement>('input[aria-label="Rule name"]')).toHaveLength(2);
    const editors = document.querySelectorAll<HTMLFormElement>('.settings-automations__rule form');
    const name = editors[0].querySelector<HTMLInputElement>('input[aria-label="Rule name"]')!;
    name.value = 'Changed rule';
    editors[0].dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }));
    await vi.waitFor(() => expect(document.body.textContent).toContain('Changed rule'));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules/7', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ name: 'Changed rule', resourceId: 42, eventType: 'pull_request.merged' }),
    }));
    await vi.waitFor(() => expect(buttons('Revoke approval')).toHaveLength(0));
    findButton('Disable').click();
    await vi.waitFor(() => expect(buttons('Enable')).toHaveLength(1));
    expect(requests).toHaveBeenCalledWith(base + '/automation-rules/7', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ enabled: false }),
    }));
    controller.abort();
  });

  it('fails closed on API errors and ignores late loads after the view is aborted', async () => {
    requests.mockRejectedValueOnce(Object.assign(new Error('Forbidden'), { status: 403 }));
    const controller = mount(true);
    await vi.waitFor(() => expect(document.body.textContent).toContain('unavailable for this account'));
    expect(buttons('Create Rule')).toHaveLength(0);
    controller.abort();

    let release: (value: unknown) => void = () => {};
    requests.mockReset().mockImplementation(() => new Promise(resolve => { release = resolve; }));
    const latest = mount(true);
    const original = document.body.innerHTML;
    latest.abort();
    release([]);
    await Promise.resolve();
    await Promise.resolve();
    expect(document.body.innerHTML).toBe(original);
  });
});
