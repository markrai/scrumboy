// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';

const {
  selectorState,
  showToastMock,
  emitMock,
  appendAssignedMock,
  incrementUnreadMock,
  playAssignmentSoundMock,
  desktopNotificationMock,
} = vi.hoisted(() => ({
  selectorState: {
    authStatusAvailable: true,
    projectId: null as number | null,
    user: { id: 11 } as { id: number } | null,
  },
  showToastMock: vi.fn(),
  emitMock: vi.fn(),
  appendAssignedMock: vi.fn(),
  incrementUnreadMock: vi.fn(),
  playAssignmentSoundMock: vi.fn(),
  desktopNotificationMock: vi.fn(),
}));

vi.mock('../state/selectors.js', () => ({
  getAuthStatusAvailable: () => selectorState.authStatusAvailable,
  getProjectId: () => selectorState.projectId,
  getUser: () => selectorState.user,
}));

vi.mock('../utils.js', () => ({
  showToast: showToastMock,
}));

vi.mock('../events.js', () => ({
  emit: emitMock,
}));

vi.mock('./assignmentNotify.js', () => ({
  playAssignmentSound: playAssignmentSoundMock,
  showAssignmentDesktopNotification: desktopNotificationMock,
}));

vi.mock('./notifications.js', () => ({
  appendTodoAssignedNotification: appendAssignedMock,
  incrementUnread: incrementUnreadMock,
}));

vi.mock('./sse-client.js', () => ({
  isSseDebugEnabled: () => false,
  SseConnectionManager: class {
    open(): void {}
    stop(): void {}
    restartRequested(): void {}
  },
}));

vi.mock('./foreground-resume.js', () => ({
  scheduleResumeResync: vi.fn(),
}));

const en = enCatalog as Record<string, string>;
const de = deCatalog as Record<string, string>;

type RealtimeModule = typeof import('./realtime.js');
type I18nModule = typeof import('../i18n/index.js');

async function loadSubject(locale: 'en' | 'de' = 'en'): Promise<{ realtime: RealtimeModule; i18n: I18nModule }> {
  vi.resetModules();
  const i18n = await import('../i18n/index.js');
  i18n.resetI18nForTests();
  await i18n.initI18n({
    locale,
    loadLocale: vi.fn(async (nextLocale: 'en' | 'de') => (nextLocale === 'de' ? de : en)),
  });
  const realtime = await import('./realtime.js');
  realtime.__resetRealtimeSeenEventIdsForTest();
  return { realtime, i18n };
}

function creatorActivityEvent(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'creator-activity-1',
    type: 'todo.creator_activity',
    projectId: 7,
    projectSlug: 'delivery-slug',
    payload: {
      todoId: 81,
      localId: 5,
      title: 'Committed title',
      activityReason: 'todo_updated',
    },
    ...overrides,
  };
}

describe('creator activity realtime handling', () => {
  beforeEach(() => {
    selectorState.authStatusAvailable = true;
    selectorState.projectId = null;
    selectorState.user = { id: 11 };
    showToastMock.mockReset();
    emitMock.mockReset();
    appendAssignedMock.mockReset();
    incrementUnreadMock.mockReset();
    playAssignmentSoundMock.mockReset();
    desktopNotificationMock.mockReset();
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it('shows exactly one localized toast and no assignment or unread side effects', async () => {
    const { realtime } = await loadSubject('en');

    realtime.__handleIncomingRealtimeMessageForTest(JSON.stringify(creatorActivityEvent()));

    expect(showToastMock).toHaveBeenCalledTimes(1);
    expect(showToastMock).toHaveBeenCalledWith(en['realtime.creatorActivity'].replace('{title}', 'Committed title'));
    expect(appendAssignedMock).not.toHaveBeenCalled();
    expect(incrementUnreadMock).not.toHaveBeenCalled();
    expect(playAssignmentSoundMock).not.toHaveBeenCalled();
    expect(desktopNotificationMock).not.toHaveBeenCalled();
  });

  it('uses the localized todo fallback when the committed title is absent', async () => {
    const { realtime } = await loadSubject('en');
    const event = creatorActivityEvent({
      payload: {
        todoId: 81,
        localId: 5,
        activityReason: 'todo_moved',
      },
    });

    realtime.__handleIncomingRealtimeMessageForTest(JSON.stringify(event));

    expect(showToastMock).toHaveBeenCalledWith(
      en['realtime.creatorActivity'].replace('{title}', en['realtime.todoFallback']),
    );
  });

  it('ignores malformed, unrelated, and internal workflow events', async () => {
    const { realtime } = await loadSubject('en');
    const events: unknown[] = [
      creatorActivityEvent({ id: 'missing-todo', payload: { localId: 5, activityReason: 'todo_updated' } }),
      creatorActivityEvent({ id: 'missing-local', payload: { todoId: 81, activityReason: 'todo_updated' } }),
      creatorActivityEvent({ id: 'missing-slug', projectSlug: '' }),
      creatorActivityEvent({ id: 'wrong-reason', payload: { todoId: 81, localId: 5, activityReason: 'unknown' } }),
      { id: 'unrelated', type: 'refresh_needed', projectId: 7 },
      { id: 'request', type: 'todo.creator_notification_requested', projectId: 7 },
      { id: 'authorized', type: 'todo.creator_notification_recipient_authorized', projectId: 7 },
    ];

    realtime.__handleIncomingRealtimeMessageForTest('{');
    for (const event of events) {
      realtime.__handleIncomingRealtimeMessageForTest(JSON.stringify(event));
    }

    expect(showToastMock).not.toHaveBeenCalled();
  });

  it('resolves creator activity copy from the current locale at event time', async () => {
    const { realtime, i18n } = await loadSubject('en');
    realtime.__handleIncomingRealtimeMessageForTest(JSON.stringify(creatorActivityEvent({ id: 'english' })));
    expect(showToastMock).toHaveBeenLastCalledWith(en['realtime.creatorActivity'].replace('{title}', 'Committed title'));

    await i18n.setLocale('de');
    realtime.__handleIncomingRealtimeMessageForTest(JSON.stringify(creatorActivityEvent({ id: 'german' })));

    expect(showToastMock).toHaveBeenCalledTimes(2);
    expect(showToastMock).toHaveBeenLastCalledWith(de['realtime.creatorActivity'].replace('{title}', 'Committed title'));
  });
});
