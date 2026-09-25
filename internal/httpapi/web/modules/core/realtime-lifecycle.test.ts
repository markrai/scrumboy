// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  anonymousRestartMock,
  restartRequestedMock,
  scheduleResumeResyncMock,
} = vi.hoisted(() => ({
  anonymousRestartMock: vi.fn(),
  restartRequestedMock: vi.fn(),
  scheduleResumeResyncMock: vi.fn(),
}));

vi.mock('../events.js', () => ({ emit: vi.fn() }));
vi.mock('../state/selectors.js', () => ({
  getAuthStatusAvailable: () => true,
  getProjectId: () => null,
  getUser: () => ({ id: 11 }),
}));
vi.mock('../utils.js', () => ({ showToast: vi.fn() }));
vi.mock('./assignmentNotify.js', () => ({
  playAssignmentSound: vi.fn(),
  showAssignmentDesktopNotification: vi.fn(),
}));
vi.mock('./notifications.js', () => ({
  appendTodoAssignedNotification: vi.fn(),
  incrementUnread: vi.fn(),
}));
vi.mock('./sse-client.js', () => ({
  isSseDebugEnabled: () => false,
  SseConnectionManager: class {
    open(): void {}
    stop(): void {}
    restartRequested(reason: string): void {
      restartRequestedMock(reason);
    }
  },
}));
vi.mock('./foreground-resume.js', () => ({
  scheduleResumeResync: scheduleResumeResyncMock,
}));
vi.mock('../i18n/index.js', () => ({ t: (key: string) => key }));

import {
  initForegroundLifecycle,
  NATIVE_FOREGROUND_EVENT,
  registerAnonymousSseRestart,
  startGlobalRealtime,
  stopGlobalRealtime,
} from './realtime.js';

function pageShow(persisted: boolean): Event {
  const event = new Event('pageshow');
  Object.defineProperty(event, 'persisted', { value: persisted });
  return event;
}

beforeEach(() => {
  anonymousRestartMock.mockReset();
  restartRequestedMock.mockReset();
  scheduleResumeResyncMock.mockReset();
  stopGlobalRealtime();
  startGlobalRealtime();
  registerAnonymousSseRestart(anonymousRestartMock);
  initForegroundLifecycle();
});

afterEach(() => {
  stopGlobalRealtime();
  vi.restoreAllMocks();
});

describe('C3.0 product foreground convergence', () => {
  it('routes native foreground to global SSE, anonymous SSE, and resume resync', () => {
    window.dispatchEvent(new Event(NATIVE_FOREGROUND_EVENT));

    expect(restartRequestedMock).toHaveBeenCalledOnce();
    expect(restartRequestedMock).toHaveBeenCalledWith('native-foreground');
    expect(anonymousRestartMock).toHaveBeenCalledWith('native-foreground');
    expect(scheduleResumeResyncMock).toHaveBeenCalledWith('native-foreground');
  });

  it('keeps visible, persisted pageshow, and online browser signals unchanged', () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');

    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(pageShow(true));
    window.dispatchEvent(new Event('online'));

    expect(restartRequestedMock.mock.calls.map(([reason]) => reason)).toEqual([
      'visibility',
      'pageshow-bfcache',
      'online',
    ]);
    expect(anonymousRestartMock.mock.calls.map(([reason]) => reason)).toEqual([
      'visibility',
      'pageshow-bfcache',
      'online',
    ]);
    expect(scheduleResumeResyncMock.mock.calls.map(([reason]) => reason)).toEqual([
      'visibility',
      'pageshow-bfcache',
      'online',
    ]);
  });

  it('ignores hidden visibility and ordinary pageshow signals', () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');

    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(pageShow(false));

    expect(restartRequestedMock).not.toHaveBeenCalled();
    expect(anonymousRestartMock).not.toHaveBeenCalled();
    expect(scheduleResumeResyncMock).not.toHaveBeenCalled();
  });

  it('attaches the convergence listeners only once', () => {
    initForegroundLifecycle();
    initForegroundLifecycle();

    window.dispatchEvent(new Event(NATIVE_FOREGROUND_EVENT));

    expect(restartRequestedMock).toHaveBeenCalledOnce();
    expect(anonymousRestartMock).toHaveBeenCalledOnce();
    expect(scheduleResumeResyncMock).toHaveBeenCalledOnce();
  });
});
