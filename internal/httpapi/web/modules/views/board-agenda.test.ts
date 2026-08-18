// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AgendaEvent, Board } from '../types.js';
import {
  AGENDA_COLUMN_KEY,
  AGENDA_HOUR_HEIGHT_MOBILE_PX,
  AGENDA_HOUR_HEIGHT_PX,
  agendaDayWindow,
  agendaHourHeightPx,
  agendaInitialFocusMinute,
  agendaNowMinute,
  applyAgendaScrollAfterRender,
  bindAgendaLaneScrollInteractions,
  bindAgendaNowLine,
  buildAgendaColumnHtml,
  captureAgendaListScroll,
  flushAgendaInitialScroll,
  layoutAgendaTimedEvents,
  renderAgendaEventCard,
  syncAgendaNowLine,
} from './board-agenda.js';
import { getBoardColumns, visibleBoardLaneCount } from './board-rendering.js';
import { setAgendaStartOfDayPreference } from '../core/agenda-start-of-day-preferences.js';
import { setAgendaNowLinePreference } from '../core/agenda-now-line-preferences.js';
import { setBoard } from '../state/mutations.js';
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
    expect(html).toContain('class="col__count"');
    expect(html).toContain('card--agenda');
    expect(html).toContain('Pickup');
    expect(html).toContain('data-agenda-event-id="3:pickup:1"');
    const timeOpts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' };
    const start = new Date('2026-08-17T20:00:00Z').toLocaleTimeString(undefined, timeOpts);
    const end = new Date('2026-08-17T20:30:00Z').toLocaleTimeString(undefined, timeOpts);
    expect(html).toContain(`${start} - ${end}`);
    expect(html).not.toContain('Family');
    expect(html).not.toContain('data-todo-id');
    expect(html).not.toContain('data-todo-local-id');
    expect(html).not.toContain('card__drag-handle');
    expect(html).not.toContain('id="localId"');
    expect(html).not.toContain('data-status=');
    expect(html).toContain('Calendar may be out of date.');
    expect(html).toContain('Agenda');
    expect(html).not.toContain('data-i18n-text="board.agenda.title"');
  });

  it('uses a custom agenda lane title instead of the default', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = { ...board.agenda!, title: 'Team calendar' };
    const html = buildAgendaColumnHtml(board, null);
    expect(html).toContain('Team calendar');
    expect(html).not.toContain('>Agenda<');
    expect(html).not.toContain('Family');
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

  it('renders Google and Apple host badges and omits a badge for other', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const google = renderAgendaEventCard(
      {
        id: '3:pickup:1',
        sourceId: 3,
        calendarName: 'Family',
        title: 'Pickup',
        startsAt: '2026-08-17T20:00:00Z',
        endsAt: '2026-08-17T20:30:00Z',
        allDay: false,
        location: '',
        provider: 'ics_feed',
        hostKind: 'google',
      },
      'UTC',
    );
    expect(google).toContain('/assets/calendar/google.webp');
    expect(google).toContain('card__agenda-badge--google');
    expect(google).toContain('role="img"');
    expect(google).toContain('alt=""');
    expect(google).toContain(`aria-label="${enCatalog['board.agenda.badge.google']}"`);
    expect(google).not.toContain('/assets/calendar/apple.webp');

    const apple = renderAgendaEventCard(
      {
        id: '3:pickup:1',
        sourceId: 3,
        calendarName: 'Family',
        title: 'Pickup',
        startsAt: '2026-08-17T20:00:00Z',
        endsAt: '2026-08-17T20:30:00Z',
        allDay: false,
        location: '',
        provider: 'ics_feed',
        hostKind: 'apple',
      },
      'UTC',
    );
    expect(apple).toContain('/assets/calendar/apple.webp');
    expect(apple).toContain('card__agenda-badge--apple');
    expect(apple).toContain('role="img"');
    expect(apple).toContain('alt=""');
    expect(apple).toContain(`aria-label="${enCatalog['board.agenda.badge.apple']}"`);
    expect(apple).not.toContain('/assets/calendar/google.webp');

    const other = renderAgendaEventCard(
      {
        id: '3:pickup:1',
        sourceId: 3,
        calendarName: 'Family',
        title: 'Pickup',
        startsAt: '2026-08-17T20:00:00Z',
        endsAt: '2026-08-17T20:30:00Z',
        allDay: false,
        location: '',
        provider: 'ics_feed',
        hostKind: 'other',
      },
      'UTC',
    );
    expect(other).not.toContain('card__agenda-badge');
    expect(other).not.toContain('/assets/calendar/');

    const missing = renderAgendaEventCard(
      {
        id: '3:pickup:1',
        sourceId: 3,
        calendarName: 'Family',
        title: 'Pickup',
        startsAt: '2026-08-17T20:00:00Z',
        endsAt: '2026-08-17T20:30:00Z',
        allDay: false,
        location: '',
        provider: 'ics_feed',
      },
      'UTC',
    );
    expect(missing).not.toContain('card__agenda-badge');
    expect(missing).not.toContain('/assets/calendar/');
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
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    let assigned = -1;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => assigned,
      set: (value: number) => {
        assigned = Number(value);
      },
    });
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now });
    expect(assigned).toBe((480 / 60) * AGENDA_HOUR_HEIGHT_PX);
  });

  it('auto-focuses using painted 96px hours even if the CSS variable still reads 48px', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    stubHourHeightMedia(false);
    stubComputedHourVar(AGENDA_HOUR_HEIGHT_PX);
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T06:00:00Z', '2026-08-17T07:00:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('08:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    stubLaidOutHourHeight(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    expect(agendaHourHeightPx()).toBe(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    const list = document.getElementById('list_agenda') as HTMLElement;
    let assigned = -1;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => assigned,
      set: (value: number) => {
        assigned = Number(value);
      },
    });
    stubListClientHeight(list, 400);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now });
    expect(assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_MOBILE_PX);
    expect(assigned).not.toBe((360 / 60) * AGENDA_HOUR_HEIGHT_PX);
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
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T06:00:00Z', '2026-08-17T07:00:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('08:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    let assigned = -1;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => assigned,
      set: (value: number) => {
        assigned = Number(value);
      },
    });
    stubListClientHeight(list, 0);
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now });
    expect(assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_PX);
    stubLaidOutHourHeight(AGENDA_HOUR_HEIGHT_MOBILE_PX);
    stubListClientHeight(list, 400);
    flushAgendaInitialScroll();
    expect(assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_MOBILE_PX);
  });

  it('scrolls to the new focus after a start-of-day change', async () => {
    const i18n = await import('../i18n/index.js');
    await i18n.initI18n({ locale: 'en', loadLocale: vi.fn(async () => enCatalog) });
    const board = agendaBoard();
    board.agenda = {
      ...board.agenda!,
      stale: false,
      error: null,
      events: [timedEvent('Pickup', '2026-08-17T20:00:00Z', '2026-08-17T20:30:00Z', { title: 'Pickup' })],
    };
    setBoard(board);
    setAgendaStartOfDayPreference('06:00');
    document.body.innerHTML = buildAgendaColumnHtml(board, null, now);
    const list = document.getElementById('list_agenda') as HTMLElement;
    let assigned = -1;
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => assigned,
      set: (value: number) => {
        assigned = Number(value);
      },
    });
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board, now });
    expect(assigned).toBe((360 / 60) * AGENDA_HOUR_HEIGHT_PX);
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
