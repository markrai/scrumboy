// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AgendaEvent, Board } from '../types.js';
import {
  AGENDA_COLUMN_KEY,
  AGENDA_DEFAULT_LANE_COLOR,
  APPLE_CALENDAR_URL,
  GOOGLE_CALENDAR_URL,
  AGENDA_HOUR_HEIGHT_MOBILE_PX,
  AGENDA_HOUR_HEIGHT_PX,
  AGENDA_SMART_NOW_OFFSET_FRACTION,
  agendaDayWindow,
  agendaHourHeightPx,
  agendaInitialFocusMinute,
  agendaLaneColor,
  agendaNowMinute,
  agendaSmartFocusMinute,
  applyAgendaScrollAfterRender,
  bindAgendaLaneScrollInteractions,
  bindAgendaNowLine,
  buildAgendaColumnHtml,
  captureAgendaListScroll,
  flushAgendaInitialScroll,
  layoutAgendaTimedEvents,
  renderAgendaEventCard,
  syncAgendaNowLine,
  syncOpenBoardAgendaLayout,
} from './board-agenda.js';
import { getBoardColumns, visibleBoardLaneCount } from './board-rendering.js';
import { setAgendaStartOfDayPreference } from '../core/agenda-start-of-day-preferences.js';
import { setAgendaNowLinePreference } from '../core/agenda-now-line-preferences.js';
import { setBoard } from '../state/mutations.js';
import { buildMobileTabsInnerHtml } from './mobile-lane-tabs.js';
import enCatalog from '../i18n/locales/en.json';

function agendaBoard(): Board {
  return {
    project: { id: 1, name: 'Alpha', slug: 'alpha', dominantColor: '#123456' },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
    ],
    columns: { backlog: [], doing: [] },
    agenda: {
      enabled: true,
      timezone: 'UTC',
      stale: true,
      error: null,
      events: [
        {
          id: '3:pickup:1',
          sourceId: 3,
          calendarName: 'Family',
          title: 'Pickup',
          startsAt: '2026-08-17T20:00:00Z',
          endsAt: '2026-08-17T20:30:00Z',
          allDay: false,
          location: 'School',
          provider: 'ics_feed',
        },
      ],
    },
  };
}

describe('agenda virtual lane', () => {
  beforeEach(() => {
    localStorage.clear();
    setAgendaStartOfDayPreference('08:00');
  });

  afterEach(async () => {
    setBoard(null);
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
  });

  it('renders event cards without todo identifiers or drag handles', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const html = buildAgendaColumnHtml(agendaBoard(), null);
    expect(html).toContain('col--agenda');
    expect(html).not.toContain('class="col__count"');
    expect(html).toContain('card--agenda');
    expect(html).toContain('Pickup');
    expect(html).toContain('data-agenda-event-id="3:pickup:1"');
    const timeOpts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' };
    const start = new Date('2026-08-17T20:00:00Z').toLocaleTimeString(undefined, timeOpts);
    const end = new Date('2026-08-17T20:30:00Z').toLocaleTimeString(undefined, timeOpts);
    expect(html).toContain(`${start} - ${end}`);
    expect(html).not.toContain('>Family<');
    expect(html).not.toContain('data-todo-id');
    expect(html).not.toContain('data-todo-local-id');
    expect(html).not.toContain('card__drag-handle');
    expect(html).not.toContain('id="localId"');
    expect(html).not.toContain('data-status=');
    expect(html).toContain('Calendar may be out of date.');
    expect(html).toContain('Agenda');
    expect(html).not.toContain('data-i18n-text="board.agenda.title"');
  });

  it('applies the saved agenda lane color to the header, cards, and CSS variable', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { ...board.agenda!, color: '#aabbcc' };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('--agenda-lane-color:#aabbcc');
    expect(html).toContain('style="background:#aabbcc;"');
    expect(html).not.toContain(`style="background:${AGENDA_DEFAULT_LANE_COLOR};"`);
  });

  it('falls back to the default indigo when agenda color is missing or invalid', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const missing = buildAgendaColumnHtml(agendaBoard(), null);
    expect(missing).toContain(`--agenda-lane-color:${AGENDA_DEFAULT_LANE_COLOR}`);
    expect(missing).toContain(`style="background:${AGENDA_DEFAULT_LANE_COLOR};"`);

    const board = agendaBoard();
    board.agenda = { ...board.agenda!, color: 'indigo' };
    const invalid = buildAgendaColumnHtml(board, null);
    expect(invalid).toContain(`--agenda-lane-color:${AGENDA_DEFAULT_LANE_COLOR}`);
    expect(invalid).not.toContain('background:indigo');
  });

  it('uses the saved agenda color on the extra mobile tab', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { ...board.agenda!, color: '#aabbcc' };
    const html = buildMobileTabsInnerHtml([], {
      activeTabKey: AGENDA_COLUMN_KEY,
      extraTabs: [{ key: AGENDA_COLUMN_KEY, title: 'Agenda', color: agendaLaneColor(board), count: 1 }],
      laneLabel: () => 'Agenda 1',
    });
    expect(html).toContain('data-tab="agenda"');
    expect(html).toContain('background:#aabbcc');
    expect(html).not.toContain(`background:${AGENDA_DEFAULT_LANE_COLOR}`);
    expect(html).toContain('lucide-calendar-days');
    expect(html).toContain('aria-label="Agenda 1"');
    expect(html).toContain('</svg> <span class="mobile-tab__count">1</span>');
    expect(html).not.toMatch(/mobile-tab__text">Agenda/);
  });

  it('shows one mobile-tab count for all calendars that day', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      events: [
        { ...board.agenda!.events![0], id: '3:a:1', sourceId: 3, calendarName: 'Family' },
        {
          id: '4:b:1',
          sourceId: 4,
          calendarName: 'Work',
          title: 'Standup',
          startsAt: '2026-08-17T14:00:00Z',
          endsAt: '2026-08-17T14:30:00Z',
          allDay: false,
          location: '',
          provider: 'ics_feed',
        },
        {
          id: '4:c:1',
          sourceId: 4,
          calendarName: 'Work',
          title: 'Review',
          startsAt: '2026-08-17T16:00:00Z',
          endsAt: '2026-08-17T16:30:00Z',
          allDay: false,
          location: '',
          provider: 'ics_feed',
        },
      ],
    };
    const count = board.agenda.events!.length;
    const html = buildMobileTabsInnerHtml([], {
      activeTabKey: AGENDA_COLUMN_KEY,
      extraTabs: [{ key: AGENDA_COLUMN_KEY, title: 'Agenda', color: agendaLaneColor(board), count }],
      laneLabel: () => `Agenda ${count}`,
    });
    expect(count).toBe(3);
    expect(html).toContain('class="mobile-tab__count">3</span>');
    expect(html.match(/mobile-tab__count/g)?.length).toBe(1);
    expect(html).not.toContain('class="mobile-tab__count">1</span>');
    expect(html).not.toContain('class="mobile-tab__count">2</span>');
  });

  it('uses a custom agenda lane title instead of the default', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { ...board.agenda!, title: 'Team calendar' };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('Team calendar');
    expect(html).not.toContain('>Agenda<');
    expect(html).not.toContain('>Family<');
  });

  it('does not treat agenda as a workflow column', () => {
    const board = agendaBoard();
    expect(getBoardColumns(board).map((c) => c.key)).toEqual(['backlog', 'doing']);
    expect(visibleBoardLaneCount(board)).toBe(3);
  });

  it('renders all-day events without todo selection chrome', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const html = renderAgendaEventCard(
      {
        id: '3:holiday:1',
        sourceId: 3,
        calendarName: 'Family',
        title: 'Holiday',
        startsAt: '2026-08-17T00:00:00Z',
        endsAt: '2026-08-18T00:00:00Z',
        allDay: true,
        location: '',
        provider: 'ics_feed',
      },
      'UTC',
    );
    expect(html).toContain('Holiday');
    expect(html).toContain('All day');
    expect(html).toContain('card__agenda-copy');
    expect(html).toContain('card__title');
    expect(html).toContain('card__agenda-meta');
    expect(html).not.toContain('Family');
    expect(html).not.toContain('card__agenda-badge');
    expect(html).not.toContain(' - ');
    expect(html).not.toContain('card--selected');
    expect(html).not.toContain('checkbox');
    expect(AGENDA_COLUMN_KEY).toBe('agenda');
  });

  it('omits empty copy when first fetch failed with no events', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      enabled: true,
      timezone: 'UTC',
      stale: true,
      error: 'calendar feed too large',
      events: [],
    };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('calendar feed too large');
    expect(html).not.toContain('No events today.');
    expect(html).not.toContain('col__agenda-empty');
  });

  it('renders a 24-hour grid when a successful snapshot has no events today', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      enabled: true,
      timezone: 'UTC',
      stale: false,
      error: null,
      events: [],
    };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('agenda-day');
    expect(html.match(/class="agenda-hour"/g)?.length).toBe(24);
    expect(html).not.toContain('No events today.');
    expect(html).not.toContain('col__agenda-empty');
    expect(html).not.toContain('col__agenda-status');
  });

  it('shows last-good events together with a refresh error', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: true,
      error: 'calendar feed request failed',
    };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('calendar feed request failed');
    expect(html).toContain('Pickup');
    expect(html).not.toContain('No events today.');
  });

  it('renders Google and Apple host chips in the lane header, not on cards', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      events: [
        timedEvent('3:google-a:1', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', {
          sourceId: 3,
          calendarName: 'Family',
          hostKind: 'google',
          title: 'Pickup',
        }),
        timedEvent('3:google-b:1', '2026-08-17T21:00:00Z', '2026-08-17T21:30:00Z', {
          sourceId: 3,
          calendarName: 'Family',
          hostKind: 'google',
          title: 'Dinner',
        }),
        timedEvent('4:apple-a:1', '2026-08-17T18:00:00Z', '2026-08-17T18:30:00Z', {
          sourceId: 4,
          calendarName: 'Work',
          hostKind: 'apple',
          title: 'Standup',
        }),
        timedEvent('4:apple-b:1', '2026-08-17T19:00:00Z', '2026-08-17T19:30:00Z', {
          sourceId: 4,
          calendarName: 'Work',
          hostKind: 'apple',
          title: 'Retro',
        }),
        timedEvent('5:other-a:1', '2026-08-17T22:00:00Z', '2026-08-17T22:30:00Z', {
          sourceId: 5,
          calendarName: 'Outlook',
          hostKind: 'other',
          title: 'Doctor',
        }),
        timedEvent('6:missing-a:1', '2026-08-17T23:00:00Z', '2026-08-17T23:30:00Z', {
          sourceId: 6,
          calendarName: 'ICS',
          title: 'Flight',
        }),
        timedEvent('7:apple-c:1', '2026-08-17T17:00:00Z', '2026-08-17T17:30:00Z', {
          sourceId: 7,
          calendarName: 'Personal',
          hostKind: 'apple',
          title: 'Gym',
        }),
      ],
    };
    const html = buildAgendaColumnHtml(board, null);
    const headStart = html.indexOf('col__head--agenda');
    const listStart = html.indexOf('col__list');
    const head = html.slice(headStart, listStart === -1 ? undefined : listStart);
    expect(head).toContain('col__agenda-hosts');
    expect(head).toContain('/assets/calendar/google.webp');
    expect(head).toContain('col__agenda-host--google');
    expect(head).toContain(`href="${GOOGLE_CALENDAR_URL}"`);
    expect(head).toContain('/assets/calendar/apple.webp');
    expect(head).toContain('col__agenda-host--apple');
    expect(head).toContain(`href="${APPLE_CALENDAR_URL}"`);
    expect(head).toContain('target="_blank"');
    expect(head).toContain('rel="noopener noreferrer"');
    expect(head).not.toContain('role="img"');
    expect(head).toContain('alt=""');
    expect(head).toContain('aria-label="Family"');
    expect(head).toContain('aria-label="Work"');
    expect(head).toContain('aria-label="Outlook"');
    expect(head).toContain('lucide-calendar-days');
    expect(head).toContain('<span class="col__agenda-host col__agenda-host--other"');
    expect(head).not.toContain('col__agenda-host--other" href=');
    expect(head.match(/col__agenda-host--apple/g)?.length).toBe(2);
    expect(head.match(/col__agenda-host--google/g)?.length).toBe(1);
    expect(head.match(/col__agenda-host--other/g)?.length).toBe(2);
    expect(head).toContain('>2</span>');
    expect(head).toContain('>1</span>');
    expect(html).not.toContain('data-count-for="agenda"');
    expect(html).not.toContain('class="col__count"');
    expect(html).not.toContain('card__agenda-badge');
    const cardSlice = html.slice(html.indexOf('card--agenda'));
    expect(cardSlice).not.toContain('/assets/calendar/');
    expect(cardSlice).not.toContain('col__agenda-host');
  });

  it('omits host chips when there are no events today', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { ...board.agenda!, events: [] };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('col__agenda-hosts');
    expect(html).not.toContain('col__agenda-host--');
    expect(html).not.toContain('data-count-for="agenda"');
    expect(html).not.toContain('class="col__count"');
  });
});

function timedEvent(id: string, startsAt: string, endsAt: string, extras: Partial<AgendaEvent> = {}): AgendaEvent {
  return {
    id,
    sourceId: 3,
    calendarName: 'Family',
    title: extras.title || id,
    startsAt,
    endsAt,
    allDay: false,
    location: '',
    provider: 'ics_feed',
    ...extras,
  };
}

describe('agenda day window, focus, and timed layout', () => {
  const utcNoon = new Date('2026-08-17T12:00:00Z');

  it('always uses a 00:00-24:00 window', () => {
    expect(agendaDayWindow()).toEqual({ startMinute: 0, endMinute: 1440 });
  });

  it('focuses 08:00 when there are no timed events, including all-day only', () => {
    expect(agendaInitialFocusMinute([], 'UTC', 480, utcNoon)).toBe(480);
    const allDay = [{
      ...timedEvent('holiday', '2026-08-17T00:00:00Z', '2026-08-18T00:00:00Z'),
      allDay: true,
      title: 'Holiday',
    }];
    expect(agendaInitialFocusMinute(allDay, 'UTC', 480, utcNoon)).toBe(480);
  });

  it('opens at an earlier event than the preferred start', () => {
    const events = [timedEvent('early', '2026-08-17T06:00:00Z', '2026-08-17T06:30:00Z')];
    expect(agendaInitialFocusMinute(events, 'UTC', 480, utcNoon)).toBe(360);
  });

  it('opens at 07:30 when that is the earliest event before 08:00', () => {
    const events = [timedEvent('early', '2026-08-17T07:30:00Z', '2026-08-17T08:00:00Z')];
    expect(agendaInitialFocusMinute(events, 'UTC', 480, utcNoon)).toBe(450);
  });

  it('keeps the preferred start when the first event is later', () => {
    const events = [timedEvent('late', '2026-08-17T10:00:00Z', '2026-08-17T11:00:00Z')];
    expect(agendaInitialFocusMinute(events, 'UTC', 480, utcNoon)).toBe(480);
  });

  it('uses a custom preferred start when the first event is later', () => {
    const events = [timedEvent('late', '2026-08-17T10:00:00Z', '2026-08-17T11:00:00Z')];
    expect(agendaInitialFocusMinute(events, 'UTC', 540, utcNoon)).toBe(540);
  });

  it('uses a custom preferred start only until an earlier event exists', () => {
    const events = [timedEvent('early', '2026-08-17T07:00:00Z', '2026-08-17T07:30:00Z')];
    expect(agendaInitialFocusMinute(events, 'UTC', 540, utcNoon)).toBe(420);
  });

  it('treats an overnight leftover as beginning at 00:00', () => {
    const events = [timedEvent('night', '2026-08-16T22:00:00Z', '2026-08-17T02:00:00Z')];
    expect(agendaInitialFocusMinute(events, 'UTC', 480, utcNoon)).toBe(0);
  });

  it('maps the current wall clock to a fractional minute of day', () => {
    expect(agendaNowMinute('UTC', new Date('2026-08-17T12:00:00Z'))).toBe(720);
    expect(agendaNowMinute('UTC', new Date('2026-08-17T12:00:30Z'))).toBe(720.5);
  });

  describe('smart temporal focus', () => {
    const viewportMinutes = 360;

    it('keeps an early event as the floor before Start of day', () => {
      const events = [timedEvent('early', '2026-08-17T06:00:00Z', '2026-08-17T06:30:00Z')];
      const baseline = agendaInitialFocusMinute(events, 'UTC', 480, new Date('2026-08-17T06:30:00Z'));
      expect(baseline).toBe(360);
      expect(agendaSmartFocusMinute({
        baselineMinute: baseline,
        nowMinute: 390,
        viewportMinutes,
        isToday: true,
      })).toBe(360);
    });

    it('keeps Start of day when the first event is later and now is still before it', () => {
      const events = [timedEvent('late', '2026-08-17T10:00:00Z', '2026-08-17T11:00:00Z')];
      const baseline = agendaInitialFocusMinute(events, 'UTC', 480, new Date('2026-08-17T09:00:00Z'));
      expect(baseline).toBe(480);
      expect(agendaSmartFocusMinute({
        baselineMinute: baseline,
        nowMinute: 540,
        viewportMinutes,
        isToday: true,
      })).toBe(480);
    });

    it('advances in the afternoon so now sits one-third down the viewport', () => {
      const focus = agendaSmartFocusMinute({
        baselineMinute: 480,
        nowMinute: 840,
        viewportMinutes,
        isToday: true,
      });
      expect(focus).toBeGreaterThan(480);
      expect(focus).toBe(840 - viewportMinutes * AGENDA_SMART_NOW_OFFSET_FRACTION);
      expect(focus + viewportMinutes * AGENDA_SMART_NOW_OFFSET_FRACTION).toBe(840);
    });

    it('still follows current time in the afternoon when there are no events', () => {
      const baseline = agendaInitialFocusMinute([], 'UTC', 480, new Date('2026-08-17T16:00:00Z'));
      expect(baseline).toBe(480);
      expect(agendaSmartFocusMinute({
        baselineMinute: baseline,
        nowMinute: 960,
        viewportMinutes,
        isToday: true,
      })).toBe(960 - viewportMinutes * AGENDA_SMART_NOW_OFFSET_FRACTION);
    });

    it('clamps late-day focus to the bottom of the 24-hour canvas', () => {
      expect(agendaSmartFocusMinute({
        baselineMinute: 480,
        nowMinute: 1380,
        viewportMinutes,
        isToday: true,
      })).toBe(1440 - viewportMinutes);
    });

    it('uses the board timezone current minute, not a UTC wall clock', () => {
      const now = new Date('2026-08-17T18:00:00Z');
      expect(agendaNowMinute('America/New_York', now)).toBe(840);
      expect(agendaNowMinute('UTC', now)).toBe(1080);
      const boardTz = agendaSmartFocusMinute({
        baselineMinute: 480,
        nowMinute: agendaNowMinute('America/New_York', now),
        viewportMinutes,
        isToday: true,
      });
      const utcClock = agendaSmartFocusMinute({
        baselineMinute: 480,
        nowMinute: agendaNowMinute('UTC', now),
        viewportMinutes,
        isToday: true,
      });
      expect(boardTz).toBe(720);
      expect(utcClock).toBe(960);
      expect(boardTz).not.toBe(utcClock);
    });

    it('uses baseline focus when the rendered grid is not today', () => {
      expect(agendaSmartFocusMinute({
        baselineMinute: 480,
        nowMinute: 840,
        viewportMinutes,
        isToday: false,
      })).toBe(480);
    });
  });

  it('places overlapping events in two columns and a later cluster at full width', () => {
    const events = [
      timedEvent('A', '2026-08-17T09:00:00Z', '2026-08-17T10:00:00Z'),
      timedEvent('B', '2026-08-17T09:30:00Z', '2026-08-17T10:30:00Z'),
      timedEvent('C', '2026-08-17T15:00:00Z', '2026-08-17T16:00:00Z'),
    ];
    const window = agendaDayWindow();
    const layout = layoutAgendaTimedEvents(events, 'UTC', window, utcNoon);
    const byId = Object.fromEntries(layout.map((item) => [item.event.id, item]));
    expect(byId.A.columnCount).toBe(2);
    expect(byId.B.columnCount).toBe(2);
    expect(byId.A.column).not.toBe(byId.B.column);
    expect(byId.C.column).toBe(0);
    expect(byId.C.columnCount).toBe(1);
  });

  it('lets sequential events occupy full width', () => {
    const events = [
      timedEvent('A', '2026-08-17T10:00:00Z', '2026-08-17T11:00:00Z'),
      timedEvent('B', '2026-08-17T11:00:00Z', '2026-08-17T12:00:00Z'),
    ];
    const window = { startMinute: 600, endMinute: 720 };
    const layout = layoutAgendaTimedEvents(events, 'UTC', window, utcNoon);
    expect(layout).toHaveLength(2);
    expect(layout.every((item) => item.columnCount === 1 && item.column === 0)).toBe(true);
  });

  it('keeps all-day events out of timed packing', () => {
    const events = [
      {
        ...timedEvent('holiday', '2026-08-17T00:00:00Z', '2026-08-18T00:00:00Z'),
        allDay: true,
        title: 'Holiday',
      },
      timedEvent('A', '2026-08-17T10:00:00Z', '2026-08-17T11:00:00Z'),
    ];
    const window = { startMinute: 0, endMinute: 1440 };
    const layout = layoutAgendaTimedEvents(events, 'UTC', window, utcNoon);
    expect(layout.map((item) => item.event.id)).toEqual(['A']);
  });

  it('clamps overnight events to the visible day', () => {
    const event = timedEvent('night', '2026-08-17T22:00:00Z', '2026-08-18T02:00:00Z');
    const startDay = layoutAgendaTimedEvents(
      [event],
      'UTC',
      { startMinute: 0, endMinute: 1440 },
      new Date('2026-08-17T12:00:00Z'),
    );
    expect(startDay[0].startMinute).toBe(1320);
    expect(startDay[0].endMinute).toBe(1440);

    const nextDay = layoutAgendaTimedEvents(
      [event],
      'UTC',
      { startMinute: 0, endMinute: 1440 },
      new Date('2026-08-18T12:00:00Z'),
    );
    expect(nextDay[0].startMinute).toBe(0);
    expect(nextDay[0].endMinute).toBe(120);
  });

  it('positions spring-forward 03:00 at wall-clock minute 180, not elapsed-from-midnight', () => {
    const now = new Date('2026-03-08T16:00:00Z');
    const event = timedEvent('spring', '2026-03-08T07:00:00Z', '2026-03-08T08:00:00Z');
    const layout = layoutAgendaTimedEvents(
      [event],
      'America/New_York',
      { startMinute: 0, endMinute: 1440 },
      now,
    );
    expect(layout[0].startMinute).toBe(180);
    expect(layout[0].startMinute).not.toBe(120);
  });

  it('maps both fall-back 01:30 instants to the same wall-clock slot', () => {
    const now = new Date('2026-11-01T16:00:00Z');
    const events = [
      timedEvent('first', '2026-11-01T05:30:00Z', '2026-11-01T06:00:00Z'),
      timedEvent('second', '2026-11-01T06:30:00Z', '2026-11-01T07:00:00Z'),
    ];
    const layout = layoutAgendaTimedEvents(
      events,
      'America/New_York',
      { startMinute: 0, endMinute: 1440 },
      now,
    );
    expect(layout[0].startMinute).toBe(90);
    expect(layout[1].startMinute).toBe(90);
  });
});

describe('agenda day grid HTML', () => {
  const now = new Date('2026-08-17T12:00:00Z');
  const originalMatchMedia = window.matchMedia;
  const originalGetComputedStyle = window.getComputedStyle;

  function stubHourHeightMedia(mobile: boolean): void {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: (query: string) => ({
        matches: mobile && query.includes('max-width: 767px'),
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  }

  function stubLaidOutHourHeight(px: number): void {
    document.querySelectorAll('.agenda-hour').forEach((el) => {
      Object.defineProperty(el, 'getBoundingClientRect', {
        configurable: true,
        value: () => ({
          x: 0,
          y: 0,
          top: 0,
          left: 0,
          right: 0,
          bottom: px,
          width: 0,
          height: px,
          toJSON: () => ({}),
        }),
      });
    });
  }

  function stubListClientHeight(list: HTMLElement, height: number): void {
    Object.defineProperty(list, 'clientHeight', {
      configurable: true,
      get: () => height,
    });
  }

  function trackScrollTop(list: HTMLElement): { get assigned(): number } {
    let assigned = -1;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => assigned,
      set: (value: number) => {
        assigned = Number(value);
      },
    });
    return {
      get assigned() {
        return assigned;
      },
    };
  }

  function expectedSmartScrollTop(opts: {
    baselineMinute: number;
    nowMinute: number | null;
    viewportPx: number;
    hourPx: number;
    isToday?: boolean;
  }): number {
    const focus = agendaSmartFocusMinute({
      baselineMinute: opts.baselineMinute,
      nowMinute: opts.nowMinute,
      viewportMinutes: (opts.viewportPx / opts.hourPx) * 60,
      isToday: opts.isToday ?? true,
    });
    return (focus / 60) * opts.hourPx;
  }

  function stubComputedHourVar(px: number): void {
    const original = originalGetComputedStyle.bind(window);
    window.getComputedStyle = ((elt: Element, pseudoElt?: string | null) => {
      const style = original(elt, pseudoElt);
      const getPropertyValue = style.getPropertyValue.bind(style);
      Object.defineProperty(style, 'getPropertyValue', {
        configurable: true,
        value: (name: string) => (name === '--agenda-hour-height' ? `${px}px` : getPropertyValue(name)),
      });
      return style;
    }) as typeof window.getComputedStyle;
  }

  beforeEach(() => {
    localStorage.clear();
    setAgendaStartOfDayPreference('08:00');
  });

  afterEach(async () => {
    vi.useRealTimers();
    document.body.innerHTML = '';
    flushAgendaInitialScroll();
    bindAgendaNowLine();
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: originalMatchMedia,
    });
    window.getComputedStyle = originalGetComputedStyle;
    setBoard(null);
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
  });

  it('places timed cards with inline top/height and all-day events above the grid', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [
        {
          ...timedEvent('holiday', '2026-08-17T00:00:00Z', '2026-08-18T00:00:00Z'),
          allDay: true,
          title: 'Holiday',
        },
        timedEvent('Pickup', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', { title: 'Pickup' }),
      ],
    };
    const html = buildAgendaColumnHtml(board, null, now);
    expect(html).toContain('agenda-allday');
    expect(html).toContain('Holiday');
    expect(html.indexOf('agenda-allday')).toBeLessThan(html.indexOf('agenda-day'));
    expect(html).toContain('agenda-day');
    expect(html).toContain('agenda-hour');
    expect(html).toContain('agenda-now-line');
    expect(html).toContain('data-agenda-day="2026-08-17"');
    expect(html).toContain('card--agenda-timed');
    expect(html).toContain('card__agenda-copy');
    expect(html).toContain('card__title');
    expect(html).toContain('card__agenda-meta');
    expect(html).toContain('--agenda-start-min:');
    expect(html).toContain('--agenda-span-min:');
    expect(html).not.toMatch(/style="[^"]*top:\d/);
    expect(html).not.toContain("card.replace");
    expect(html).toContain('Pickup');
    expect(html).not.toContain('col__agenda-empty');
  });

  it('keeps a 1-hour timed card as 60 minutes without baking pixel slot height', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    stubHourHeightMedia(false);
    expect(agendaHourHeightPx()).toBe(AGENDA_HOUR_HEIGHT_PX);
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Standup', '2026-08-17T06:00:00Z', '2026-08-17T07:00:00Z', { title: 'Standup' })],
    };
    const html = buildAgendaColumnHtml(board, null, now);
    expect(html).toContain('--agenda-start-min:360');
    expect(html).toContain('--agenda-span-min:60');
    expect(html).not.toContain(`height:${AGENDA_HOUR_HEIGHT_PX}px`);
    expect(html).not.toMatch(/style="[^"]*top:\d/);
  });

  it('does not bake 96px slot pixels; mobile hour height stays a CSS variable', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    stubHourHeightMedia(true);
    expect(agendaHourHeightPx()).toBe(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Standup', '2026-08-17T06:00:00Z', '2026-08-17T07:00:00Z', { title: 'Standup' })],
    };
    const html = buildAgendaColumnHtml(board, null, now);
    expect(html).toContain('--agenda-span-min:60');
    expect(html).not.toContain(`height:${AGENDA_HOUR_HEIGHT_MOBILE_PX}px`);
    expect(html).not.toContain(`height:${AGENDA_HOUR_HEIGHT_PX}px`);
  });

  it('renders hour rows for an empty day grid and omits empty copy', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { enabled: true, timezone: 'UTC', stale: false, error: null, events: [] };
    const html = buildAgendaColumnHtml(board, null, now);
    expect(html).toContain('agenda-day');
    expect(html.match(/class="agenda-hour"/g)?.length).toBe(24);
    expect(html).not.toContain('No events today.');
    expect(html).not.toContain('col__agenda-empty');
  });

  it('still renders a 24-hour grid with all-day events and no timed events', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      enabled: true,
      timezone: 'UTC',
      stale: false,
      error: null,
      events: [
        {
          ...timedEvent('holiday', '2026-08-17T00:00:00Z', '2026-08-18T00:00:00Z'),
          allDay: true,
          title: 'Holiday',
        },
      ],
    };
    const html = buildAgendaColumnHtml(board, null, now);
    expect(html).toContain('Holiday');
    expect(html).toContain('agenda-allday');
    expect(html).toContain('agenda-day');
    expect(html.match(/class="agenda-hour"/g)?.length).toBe(24);
    expect(html).not.toContain('No events today.');
  });

  it('auto-focuses the timed list to the initial focus minute', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const morning = new Date('2026-08-17T09:00:00Z');
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T10:00:00Z', '2026-08-17T10:30:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, morning);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 288);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now: morning });
    expect(scroll.assigned).toBe((480 / 60) * AGENDA_HOUR_HEIGHT_PX);
  });

  it('auto-focuses using painted 96px hours even if the CSS variable still reads 48px', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    stubHourHeightMedia(false);
    stubComputedHourVar(AGENDA_HOUR_HEIGHT_PX);
    const earlyMorning = new Date('2026-08-17T06:30:00Z');
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T06:00:00Z', '2026-08-17T07:00:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('08:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, earlyMorning);
    stubLaidOutHourHeight(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    expect(agendaHourHeightPx()).toBe(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 400);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now: earlyMorning });
    expect(scroll.assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_MOBILE_PX);
    expect(scroll.assigned).not.toBe((360 / 60) * AGENDA_HOUR_HEIGHT_PX);
  });

  it('does not capture timed-list scroll when the lane is not laid out', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    stubListClientHeight(list, 0);
    list.scrollTop = 240;
    expect(captureAgendaListScroll()).toBeNull();
  });

  it('restores a previous timed-list scroll position after a rebuild', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    stubListClientHeight(list, 400);
    list.scrollTop = 240;
    const saved = captureAgendaListScroll();
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: saved });
    expect((document.getElementById('list_agenda') as HTMLElement).scrollTop).toBe(240);
  });

  it('flushes a pending first snap after a hidden lane becomes laid out', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    stubHourHeightMedia(false);
    stubComputedHourVar(AGENDA_HOUR_HEIGHT_PX);
    const earlyMorning = new Date('2026-08-17T06:30:00Z');
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T06:00:00Z', '2026-08-17T07:00:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('08:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, earlyMorning);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 0);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now: earlyMorning });
    expect(scroll.assigned).toBe(-1);
    stubLaidOutHourHeight(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    stubListClientHeight(list, 400);
    flushAgendaInitialScroll();
    expect(scroll.assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_MOBILE_PX);
  });

  it('scrolls to the new focus after a start-of-day change', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const morning = new Date('2026-08-17T07:00:00Z');
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('06:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, morning);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 288);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now: morning });
    expect(scroll.assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_PX);
  });

  it('recalculates start-of-day force-focus with afternoon smart current time', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const afternoon = new Date('2026-08-17T14:00:00Z');
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('08:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, afternoon);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 288);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now: afternoon });
    expect(scroll.assigned).toBe(expectedSmartScrollTop({
      baselineMinute: 480,
      nowMinute: 840,
      viewportPx: 288,
      hourPx: AGENDA_HOUR_HEIGHT_PX,
    }));
    expect(scroll.assigned).toBeGreaterThan((480 / 60) * AGENDA_HOUR_HEIGHT_PX);
  });

  it('uses baseline focus when the rendered grid is not today', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const gridDay = new Date('2026-08-17T12:00:00Z');
    const nextDayAfternoon = new Date('2026-08-18T14:00:00Z');
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, gridDay);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 288);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now: nextDayAfternoon });
    expect(scroll.assigned).toBe((480 / 60) * AGENDA_HOUR_HEIGHT_PX);
    expect(scroll.assigned).not.toBe(expectedSmartScrollTop({
      baselineMinute: 480,
      nowMinute: 840,
      viewportPx: 288,
      hourPx: AGENDA_HOUR_HEIGHT_PX,
    }));
  });

  it('keeps a manual scroll position across an ordinary board refresh', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    stubListClientHeight(list, 400);
    list.scrollTop = 999;
    const saved = captureAgendaListScroll();
    expect(saved).toBe(999);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: saved, board, now: new Date('2026-08-17T16:00:00Z') });
    expect((document.getElementById('list_agenda') as HTMLElement).scrollTop).toBe(999);
  });

  it('preserves scroll when syncing the open Agenda layout without forceAutoFocus', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { ...board.agenda!, color: '#6366F1' };
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    stubListClientHeight(list, 400);
    list.scrollTop = 240;
    board.agenda = { ...board.agenda!, color: '#aabbcc' };
    setBoard(board);
    syncOpenBoardAgendaLayout();
    const nextList = document.getElementById('list_agenda') as HTMLElement;
    expect(nextList).toBeTruthy();
    expect(nextList.scrollTop).toBe(240);
    expect(document.querySelector('.col--agenda')?.getAttribute('style')).toContain('--agenda-lane-color:#aabbcc');
    expect(document.querySelector('.col__head--agenda')?.getAttribute('style')).toContain('background:#aabbcc');
  });

  it('mouse drag pans the timed list and marks the lane as panning', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 100, board });
    const list = document.getElementById('list_agenda') as HTMLElement;
    let top = 100;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => top,
      set: (value: number) => {
        top = Number(value);
      },
    });
    list.dispatchEvent(new PointerEvent('pointerdown', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      button: 0,
      clientY: 80,
    }));
    expect(document.querySelector('.col--agenda')?.classList.contains('is-agenda-panning')).toBe(true);
    list.dispatchEvent(new PointerEvent('pointermove', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      clientY: 50,
    }));
    expect(top).toBe(130);
    list.dispatchEvent(new PointerEvent('pointerup', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
    }));
    expect(document.querySelector('.col--agenda')?.classList.contains('is-agenda-panning')).toBe(false);
  });

  it('does not start mouse pan from a header host link', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      events: [{ ...board.agenda!.events![0], hostKind: 'google' }],
    };
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 100, board });
    const list = document.getElementById('list_agenda') as HTMLElement;
    let top = 100;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => top,
      set: (value: number) => {
        top = Number(value);
      },
    });
    const badge = document.querySelector('a.col__agenda-host--google') as HTMLAnchorElement | null;
    expect(badge).toBeTruthy();
    expect(list.contains(badge)).toBe(false);
    badge!.dispatchEvent(new PointerEvent('pointerdown', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      button: 0,
      clientY: 80,
    }));
    expect(document.querySelector('.col--agenda')?.classList.contains('is-agenda-panning')).toBe(false);
    list.dispatchEvent(new PointerEvent('pointermove', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      clientY: 50,
    }));
    expect(top).toBe(100);
  });

  it('does not start mouse pan from an img nested inside an in-list anchor', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 100, board });
    const list = document.getElementById('list_agenda') as HTMLElement;
    let top = 100;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => top,
      set: (value: number) => {
        top = Number(value);
      },
    });
    const link = document.createElement('a');
    link.href = GOOGLE_CALENDAR_URL;
    const img = document.createElement('img');
    img.alt = '';
    link.appendChild(img);
    list.prepend(link);
    img.dispatchEvent(new PointerEvent('pointerdown', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      button: 0,
      clientY: 80,
    }));
    expect(document.querySelector('.col--agenda')?.classList.contains('is-agenda-panning')).toBe(false);
    list.dispatchEvent(new PointerEvent('pointermove', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      clientY: 50,
    }));
    expect(top).toBe(100);
  });

  it('does not pan the timed list from a touch pointer', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 100, board });
    const list = document.getElementById('list_agenda') as HTMLElement;
    let top = 100;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => top,
      set: (value: number) => {
        top = Number(value);
      },
    });
    list.dispatchEvent(new PointerEvent('pointerdown', {
      bubbles: true,
      pointerId: 2,
      pointerType: 'touch',
      button: 0,
      clientY: 80,
    }));
    list.dispatchEvent(new PointerEvent('pointermove', {
      bubbles: true,
      pointerId: 2,
      pointerType: 'touch',
      clientY: 50,
    }));
    expect(top).toBe(100);
    expect(document.querySelector('.col--agenda')?.classList.contains('is-agenda-panning')).toBe(false);
  });

  it('does not stack drag listeners across Agenda rebuilds', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 100, board });
    bindAgendaLaneScrollInteractions();
    bindAgendaLaneScrollInteractions();
    const list = document.getElementById('list_agenda') as HTMLElement;
    let top = 100;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => top,
      set: (value: number) => {
        top = Number(value);
      },
    });
    list.dispatchEvent(new PointerEvent('pointerdown', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      button: 0,
      clientY: 80,
    }));
    list.dispatchEvent(new PointerEvent('pointermove', {
      bubbles: true,
      pointerId: 1,
      pointerType: 'mouse',
      clientY: 50,
    }));
    expect(top).toBe(130);
  });

  it('positions the now-line on today and hides it on another civil day', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 0, board, now });
    const line = document.querySelector('.agenda-now-line') as HTMLElement;
    const minute = agendaNowMinute('UTC', now);
    expect(line.hidden).toBe(false);
    expect(line.style.getPropertyValue('--agenda-now-min')).toBe(String(minute));
    expect(line.classList.contains('agenda-now-line--prominent')).toBe(false);
    document.querySelector('.agenda-day')?.setAttribute('data-agenda-day', '2026-08-16');
    syncAgendaNowLine({ board, now });
    expect(line.hidden).toBe(true);
  });

  it('moves the now-line after one second', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-17T12:00:00Z'));
    const board = agendaBoard();
    setBoard(board);
    const clock = new Date();
    document.body.innerHTML = buildAgendaColumnHtml(board, null, clock);
    applyAgendaScrollAfterRender({ restoreScrollTop: 0, board, now: clock });
    const line = document.querySelector('.agenda-now-line') as HTMLElement;
    expect(line.style.getPropertyValue('--agenda-now-min')).toBe('720');
    vi.advanceTimersByTime(1000);
    expect(Number(line.style.getPropertyValue('--agenda-now-min'))).toBeCloseTo(
      agendaNowMinute('UTC', new Date())!,
      4,
    );
    expect(line.style.getPropertyValue('--agenda-now-min')).not.toBe('720');
  });

  it('does not mutate list scrollTop when the now-line ticks', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-17T12:00:00Z'));
    const board = agendaBoard();
    setBoard(board);
    const clock = new Date();
    document.body.innerHTML = buildAgendaColumnHtml(board, null, clock);
    const list = document.getElementById('list_agenda') as HTMLElement;
    const scroll = trackScrollTop(list);
    stubListClientHeight(list, 400);
    applyAgendaScrollAfterRender({ restoreScrollTop: 240, board, now: clock });
    expect(scroll.assigned).toBe(240);
    vi.advanceTimersByTime(5000);
    expect(scroll.assigned).toBe(240);
  });

  it('applies the prominent now-line class from the user preference', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    setBoard(board);
    setAgendaNowLinePreference('prominent');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    applyAgendaScrollAfterRender({ restoreScrollTop: 0, board, now });
    expect(document.querySelector('.agenda-now-line')?.classList.contains('agenda-now-line--prominent')).toBe(true);
  });
});
