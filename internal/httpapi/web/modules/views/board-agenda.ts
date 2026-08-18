import type { AgendaEvent, Board } from '../types.js';
import { escapeHTML } from '../utils.js';
import { t } from '../i18n/index.js';
import { getBoard, getMobileTab } from '../state/selectors.js';
import {
  AGENDA_START_OF_DAY_MAX_MINUTE,
  agendaStartOfDayMinutes,
} from '../core/agenda-start-of-day-preferences.js';

export const AGENDA_COLUMN_KEY = 'agenda';
export const AGENDA_HOUR_HEIGHT_PX = 48;
export const AGENDA_HOUR_HEIGHT_MOBILE_PX = 96;
export const AGENDA_HOUR_HEIGHT_MOBILE_MQ = '(max-width: 767px)';
export const AGENDA_MIN_VISIBLE_MINUTES = 15;
export const AGENDA_DAY_MINUTES = 1440;

export function agendaHourHeightPx(): number {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return AGENDA_HOUR_HEIGHT_PX;
  }
  try {
    return window.matchMedia(AGENDA_HOUR_HEIGHT_MOBILE_MQ).matches
      ? AGENDA_HOUR_HEIGHT_MOBILE_PX
      : AGENDA_HOUR_HEIGHT_PX;
  } catch {
    return AGENDA_HOUR_HEIGHT_PX;
  }
}

export type AgendaDayWindow = {
  startMinute: number;
  endMinute: number;
};

export type AgendaTimedLayout = {
  event: AgendaEvent;
  startMinute: number;
  endMinute: number;
  column: number;
  columnCount: number;
};

type CivilParts = {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
};

export function isAgendaEnabled(board: Board): boolean {
  return !!board.agenda?.enabled;
}

export function agendaEvents(board: Board): AgendaEvent[] {
  return board.agenda?.events ?? [];
}

export function agendaLaneTitle(board: Board): string {
  const custom = typeof board.agenda?.title === 'string' ? board.agenda.title.trim() : '';
  return custom || t('board.agenda.title');
}

export function renderAgendaEventCard(
  event: AgendaEvent,
  timezone: string,
  options?: { extraClass?: string; style?: string },
): string {
  const timeLabel = formatAgendaEventTime(event, timezone);
  const location = event.location ? `<div class="muted">${escapeHTML(event.location)}</div>` : '';
  const badge = renderAgendaHostBadge(event.hostKind);
  const extraClass = options?.extraClass ? ` ${options.extraClass}` : '';
  const styleAttr = options?.style ? ` style="${escapeHTML(options.style)}"` : '';
  return `
    <article class="card card--agenda${extraClass}"${styleAttr} data-agenda-event-id="${escapeHTML(event.id)}">
      <div class="card__agenda-copy">
        <div class="card__title">${escapeHTML(event.title || '')}</div>
        ${timeLabel ? `<div class="muted card__agenda-meta">${escapeHTML(timeLabel)}</div>` : ''}
      </div>
      ${location}
      ${badge}
    </article>
  `;
}

function renderAgendaHostBadge(hostKind: string | undefined): string {
  if (hostKind === 'google') {
    const label = escapeHTML(t('board.agenda.badge.google'));
    return `<span class="card__agenda-badge card__agenda-badge--google" role="img" aria-label="${label}"><img src="/assets/calendar/google.webp" alt=""></span>`;
  }
  if (hostKind === 'apple') {
    const label = escapeHTML(t('board.agenda.badge.apple'));
    return `<span class="card__agenda-badge card__agenda-badge--apple" role="img" aria-label="${label}"><img src="/assets/calendar/apple.webp" alt=""></span>`;
  }
  return '';
}

function civilPartsFromDate(date: Date, timezone: string): CivilParts | null {
  if (Number.isNaN(date.getTime())) return null;
  const options: Intl.DateTimeFormatOptions = {
    timeZone: timezone || 'UTC',
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  };
  let parts: Intl.DateTimeFormatPart[];
  try {
    parts = new Intl.DateTimeFormat('en-US', options).formatToParts(date);
  } catch {
    try {
      parts = new Intl.DateTimeFormat('en-US', { ...options, timeZone: 'UTC' }).formatToParts(date);
    } catch {
      return null;
    }
  }
  const read = (type: Intl.DateTimeFormatPartTypes): number => {
    const raw = parts.find((part) => part.type === type)?.value;
    const n = raw ? Number.parseInt(raw, 10) : NaN;
    return Number.isFinite(n) ? n : NaN;
  };
  const year = read('year');
  const month = read('month');
  const day = read('day');
  let hour = read('hour');
  const minute = read('minute');
  const second = read('second');
  if (![year, month, day, hour, minute, second].every((n) => Number.isFinite(n))) return null;
  if (hour === 24) hour = 0;
  hour = Math.min(23, Math.max(0, hour));
  return { year, month, day, hour, minute, second };
}

function civilDateKey(parts: CivilParts): string {
  const y = String(parts.year).padStart(4, '0');
  const m = String(parts.month).padStart(2, '0');
  const d = String(parts.day).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

function wallClockMinuteOfDay(parts: CivilParts): number {
  return parts.hour * 60 + parts.minute + parts.second / 60;
}

function compareCivilKeys(a: string, b: string): number {
  if (a === b) return 0;
  return a < b ? -1 : 1;
}

export function agendaMinuteOnDay(iso: string, timezone: string, todayKey: string): number | null {
  const date = new Date(iso);
  const parts = civilPartsFromDate(date, timezone);
  if (!parts) return null;
  const key = civilDateKey(parts);
  const cmp = compareCivilKeys(key, todayKey);
  if (cmp < 0) return 0;
  if (cmp > 0) return AGENDA_DAY_MINUTES;
  const minute = wallClockMinuteOfDay(parts);
  if (minute <= 0) return 0;
  if (minute >= AGENDA_DAY_MINUTES) return AGENDA_DAY_MINUTES;
  return minute;
}

export function agendaCivilTodayKey(timezone: string, now: Date = new Date()): string {
  const parts = civilPartsFromDate(now, timezone);
  if (!parts) return civilDateKey({ year: now.getUTCFullYear(), month: now.getUTCMonth() + 1, day: now.getUTCDate(), hour: 0, minute: 0, second: 0 });
  return civilDateKey(parts);
}

function timedRangeOnDay(
  event: AgendaEvent,
  timezone: string,
  todayKey: string,
): { startMinute: number; endMinute: number } | null {
  if (event.allDay) return null;
  const startMinute = agendaMinuteOnDay(event.startsAt, timezone, todayKey);
  const endMinute = agendaMinuteOnDay(event.endsAt, timezone, todayKey);
  if (startMinute == null || endMinute == null) return null;
  return { startMinute, endMinute };
}

function clampRangeToWindow(
  startMinute: number,
  endMinute: number,
  window: AgendaDayWindow,
): { startMinute: number; endMinute: number } | null {
  let start = Math.max(window.startMinute, Math.min(window.endMinute, startMinute));
  let end = Math.max(window.startMinute, Math.min(window.endMinute, endMinute));
  if (end - start < AGENDA_MIN_VISIBLE_MINUTES) {
    end = Math.min(window.endMinute, start + AGENDA_MIN_VISIBLE_MINUTES);
  }
  if (end <= start) return null;
  return { startMinute: start, endMinute: end };
}

export function agendaDayWindow(): AgendaDayWindow {
  return { startMinute: 0, endMinute: AGENDA_DAY_MINUTES };
}

export function agendaInitialFocusMinute(
  events: AgendaEvent[],
  timezone: string,
  preferredStartMinutes: number,
  now: Date = new Date(),
): number {
  const preferred = Math.min(
    AGENDA_START_OF_DAY_MAX_MINUTE,
    Math.max(0, preferredStartMinutes),
  );
  const todayKey = agendaCivilTodayKey(timezone, now);
  let earliest = Number.POSITIVE_INFINITY;
  for (const event of events) {
    const range = timedRangeOnDay(event, timezone, todayKey);
    if (!range) continue;
    if (range.startMinute < earliest) earliest = range.startMinute;
  }
  if (!Number.isFinite(earliest)) return preferred;
  return Math.min(preferred, Math.min(AGENDA_START_OF_DAY_MAX_MINUTE, Math.max(0, earliest)));
}

export function layoutAgendaTimedEvents(
  events: AgendaEvent[],
  timezone: string,
  window: AgendaDayWindow,
  now: Date = new Date(),
): AgendaTimedLayout[] {
  const todayKey = agendaCivilTodayKey(timezone, now);
  const prepared: AgendaTimedLayout[] = [];
  for (const event of events) {
    const range = timedRangeOnDay(event, timezone, todayKey);
    if (!range) continue;
    const clamped = clampRangeToWindow(range.startMinute, range.endMinute, window);
    if (!clamped) continue;
    prepared.push({
      event,
      startMinute: clamped.startMinute,
      endMinute: clamped.endMinute,
      column: 0,
      columnCount: 1,
    });
  }
  prepared.sort((a, b) => {
    if (a.startMinute !== b.startMinute) return a.startMinute - b.startMinute;
    if (a.endMinute !== b.endMinute) return a.endMinute - b.endMinute;
    return a.event.id.localeCompare(b.event.id);
  });

  const groups: AgendaTimedLayout[][] = [];
  let current: AgendaTimedLayout[] = [];
  let groupMaxEnd = Number.NEGATIVE_INFINITY;
  for (const item of prepared) {
    if (current.length === 0 || item.startMinute < groupMaxEnd) {
      current.push(item);
      if (item.endMinute > groupMaxEnd) groupMaxEnd = item.endMinute;
    } else {
      groups.push(current);
      current = [item];
      groupMaxEnd = item.endMinute;
    }
  }
  if (current.length > 0) groups.push(current);

  for (const group of groups) {
    const columnEnds: number[] = [];
    for (const item of group) {
      let assigned = -1;
      for (let i = 0; i < columnEnds.length; i++) {
        if (columnEnds[i] <= item.startMinute) {
          assigned = i;
          break;
        }
      }
      if (assigned < 0) {
        assigned = columnEnds.length;
        columnEnds.push(item.endMinute);
      } else {
        columnEnds[assigned] = item.endMinute;
      }
      item.column = assigned;
    }
    const columnCount = Math.max(1, columnEnds.length);
    for (const item of group) {
      item.columnCount = columnCount;
    }
  }
  return prepared;
}

function formatHourLabel(hour: number): string {
  const date = new Date(Date.UTC(2026, 5, 15, hour, 0, 0));
  try {
    return date.toLocaleTimeString(undefined, { hour: 'numeric', timeZone: 'UTC' });
  } catch {
    return String(hour);
  }
}

function renderTimedCard(layout: AgendaTimedLayout, timezone: string, window: AgendaDayWindow): string {
  const hourHeight = agendaHourHeightPx();
  const top = ((layout.startMinute - window.startMinute) / 60) * hourHeight;
  const height = Math.max(
    ((layout.endMinute - layout.startMinute) / 60) * hourHeight,
    8,
  );
  const widthPct = 100 / layout.columnCount;
  const leftPct = widthPct * layout.column;
  const style = `top:${top}px;height:${height}px;left:calc(${leftPct}% + 2px);width:calc(${widthPct}% - 4px)`;
  return renderAgendaEventCard(layout.event, timezone, { extraClass: 'card--agenda-timed', style });
}

function renderDayGrid(window: AgendaDayWindow, layouts: AgendaTimedLayout[], timezone: string): string {
  const hours: string[] = [];
  for (let minute = window.startMinute; minute < window.endMinute; minute += 60) {
    const hour = Math.floor(minute / 60) % 24;
    hours.push(
      `<div class="agenda-hour"><span class="agenda-hour__label muted">${escapeHTML(formatHourLabel(hour))}</span></div>`,
    );
  }
  const height = ((window.endMinute - window.startMinute) / 60) * agendaHourHeightPx();
  const cards = layouts.map((layout) => renderTimedCard(layout, timezone, window)).join('');
  return `
    <div class="agenda-day" style="height:${height}px">
      <div class="agenda-day__hours">${hours.join('')}</div>
      <div class="agenda-day__events">${cards}</div>
    </div>
  `;
}

export function buildAgendaColumnHtml(
  board: Board,
  activeMobileTab: string | null | undefined,
  now: Date = new Date(),
): string {
  if (!isAgendaEnabled(board)) return '';
  const events = agendaEvents(board);
  const timezone = board.agenda?.timezone || 'UTC';
  const isMobileActive = activeMobileTab === AGENDA_COLUMN_KEY;
  const stale = board.agenda?.stale || !!board.agenda?.error;
  const status = board.agenda?.error
    ? `<div class="muted col__agenda-status">${escapeHTML(board.agenda.error)}</div>`
    : stale
      ? `<div class="muted col__agenda-status" data-i18n-text="board.agenda.stale">${escapeHTML(t('board.agenda.stale'))}</div>`
      : '';
  const window = agendaDayWindow();
  const allDayEvents = events.filter((event) => event.allDay);
  const allDayHtml =
    allDayEvents.length > 0
      ? `<div class="agenda-allday">${allDayEvents.map((event) => renderAgendaEventCard(event, timezone)).join('')}</div>`
      : '';
  const timedLayouts = layoutAgendaTimedEvents(events, timezone, window, now);
  const timedHtml = renderDayGrid(window, timedLayouts, timezone);
  const title = escapeHTML(agendaLaneTitle(board));
  return `
    <section class="col col--agenda${isMobileActive ? ' col--mobile-active' : ''}" data-column="${AGENDA_COLUMN_KEY}" tabindex="-1">
      <div class="col__head col__head--agenda">
        <span class="col__title">${title}</span>
        <span class="col__count" data-count-for="${AGENDA_COLUMN_KEY}">${events.length}</span>
      </div>
      ${status}
      ${allDayHtml}
      <div class="col__list" id="list_${AGENDA_COLUMN_KEY}">
        ${timedHtml}
      </div>
    </section>
  `;
}

let agendaScrollBind: AbortController | null = null;

export function captureAgendaListScroll(): number | null {
  const list = document.getElementById(`list_${AGENDA_COLUMN_KEY}`);
  if (!(list instanceof HTMLElement)) return null;
  return list.scrollTop;
}

export function bindAgendaLaneScrollInteractions(list?: HTMLElement | null): void {
  agendaScrollBind?.abort();
  const el = list ?? document.getElementById(`list_${AGENDA_COLUMN_KEY}`);
  if (!(el instanceof HTMLElement)) {
    agendaScrollBind = null;
    return;
  }
  agendaScrollBind = new AbortController();
  const { signal } = agendaScrollBind;
  const col = el.closest('.col--agenda');
  let dragging = false;
  let lastY = 0;

  const endPan = (event: PointerEvent): void => {
    if (!dragging) return;
    dragging = false;
    if (typeof el.hasPointerCapture === 'function' && el.hasPointerCapture(event.pointerId)) {
      el.releasePointerCapture(event.pointerId);
    }
    col?.classList.remove('is-agenda-panning');
  };

  el.addEventListener(
    'pointerdown',
    (event: PointerEvent) => {
      if (event.pointerType !== 'mouse' || event.button !== 0) return;
      const gutter = 10;
      if (el.clientWidth > gutter && event.offsetX >= el.clientWidth - gutter) return;
      dragging = true;
      lastY = event.clientY;
      if (typeof el.setPointerCapture === 'function') {
        el.setPointerCapture(event.pointerId);
      }
      col?.classList.add('is-agenda-panning');
    },
    { signal },
  );

  el.addEventListener(
    'pointermove',
    (event: PointerEvent) => {
      if (!dragging) return;
      el.scrollTop -= event.clientY - lastY;
      lastY = event.clientY;
    },
    { signal },
  );

  el.addEventListener('pointerup', endPan, { signal });
  el.addEventListener('pointercancel', endPan, { signal });
}

export function applyAgendaScrollAfterRender(opts: {
  restoreScrollTop?: number | null;
  forceAutoFocus?: boolean;
  board?: Board | null;
  now?: Date;
} = {}): void {
  const list = document.getElementById(`list_${AGENDA_COLUMN_KEY}`);
  if (!(list instanceof HTMLElement)) {
    agendaScrollBind?.abort();
    agendaScrollBind = null;
    return;
  }
  if (!opts.forceAutoFocus && opts.restoreScrollTop != null) {
    list.scrollTop = opts.restoreScrollTop;
  } else {
    const board = opts.board ?? getBoard();
    if (board) {
      const events = agendaEvents(board);
      const timezone = board.agenda?.timezone || 'UTC';
      const focus = agendaInitialFocusMinute(events, timezone, agendaStartOfDayMinutes(), opts.now);
      list.scrollTop = (focus / 60) * agendaHourHeightPx();
    }
  }
  bindAgendaLaneScrollInteractions(list);
}

export function syncOpenBoardAgendaLayout(): void {
  const board = getBoard();
  const col = document.querySelector('.col--agenda');
  if (!board || !col) return;
  const html = buildAgendaColumnHtml(board, getMobileTab());
  if (!html) {
    agendaScrollBind?.abort();
    agendaScrollBind = null;
    col.remove();
    return;
  }
  col.outerHTML = html;
  applyAgendaScrollAfterRender({ forceAutoFocus: true, board });
}

function formatAgendaEventTime(event: AgendaEvent, timezone: string): string {
  if (event.allDay) {
    return t('board.agenda.allDay');
  }
  const startLabel = formatAgendaClockTime(event.startsAt, timezone);
  if (!startLabel) {
    return '';
  }
  const endLabel = formatAgendaClockTime(event.endsAt, timezone);
  return endLabel ? `${startLabel} - ${endLabel}` : startLabel;
}

function formatAgendaClockTime(iso: string, timezone: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return '';
  }
  const options: Intl.DateTimeFormatOptions = {
    hour: 'numeric',
    minute: '2-digit',
    timeZone: timezone || 'UTC',
  };
  try {
    return date.toLocaleTimeString(undefined, options);
  } catch {
    return date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  }
}
