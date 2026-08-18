import { escapeHTML } from '../utils.js';
import { t } from '../i18n/index.js';
import { getBoard, getMobileTab } from '../state/selectors.js';
import { AGENDA_START_OF_DAY_MAX_MINUTE, agendaStartOfDayMinutes, } from '../core/agenda-start-of-day-preferences.js';
import { isAgendaNowLineProminent } from '../core/agenda-now-line-preferences.js';
export const AGENDA_COLUMN_KEY = 'agenda';
export const AGENDA_HOUR_HEIGHT_PX = 48;
export const AGENDA_HOUR_HEIGHT_MOBILE_PX = 96;
export const AGENDA_HOUR_HEIGHT_MOBILE_MQ = '(max-width: 767px)';
export const AGENDA_MIN_VISIBLE_MINUTES = 15;
export const AGENDA_DAY_MINUTES = 1440;
function measureLaidOutAgendaHourHeightPx() {
    if (typeof document === 'undefined')
        return null;
    const hour = document.querySelector('.agenda-hour');
    if (hour instanceof HTMLElement) {
        const painted = hour.getBoundingClientRect().height;
        if (Number.isFinite(painted) && painted > 0)
            return painted;
    }
    const day = document.querySelector('.agenda-day');
    if (day instanceof HTMLElement) {
        const painted = day.offsetHeight / 24;
        if (Number.isFinite(painted) && painted > 0)
            return painted;
    }
    return null;
}
function fallbackAgendaHourHeightPx() {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
        return AGENDA_HOUR_HEIGHT_PX;
    }
    try {
        return window.matchMedia(AGENDA_HOUR_HEIGHT_MOBILE_MQ).matches
            ? AGENDA_HOUR_HEIGHT_MOBILE_PX
            : AGENDA_HOUR_HEIGHT_PX;
    }
    catch {
        return AGENDA_HOUR_HEIGHT_PX;
    }
}
export function agendaHourHeightPx() {
    return measureLaidOutAgendaHourHeightPx() ?? fallbackAgendaHourHeightPx();
}
export function isAgendaEnabled(board) {
    return !!board.agenda?.enabled;
}
export function agendaEvents(board) {
    return board.agenda?.events ?? [];
}
export function agendaLaneTitle(board) {
    const custom = typeof board.agenda?.title === 'string' ? board.agenda.title.trim() : '';
    return custom || t('board.agenda.title');
}
export function renderAgendaEventCard(event, timezone, options) {
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
function renderAgendaHostBadge(hostKind) {
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
function civilPartsFromDate(date, timezone) {
    if (Number.isNaN(date.getTime()))
        return null;
    const options = {
        timeZone: timezone || 'UTC',
        hourCycle: 'h23',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
    };
    let parts;
    try {
        parts = new Intl.DateTimeFormat('en-US', options).formatToParts(date);
    }
    catch {
        try {
            parts = new Intl.DateTimeFormat('en-US', { ...options, timeZone: 'UTC' }).formatToParts(date);
        }
        catch {
            return null;
        }
    }
    const read = (type) => {
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
    if (![year, month, day, hour, minute, second].every((n) => Number.isFinite(n)))
        return null;
    if (hour === 24)
        hour = 0;
    hour = Math.min(23, Math.max(0, hour));
    return { year, month, day, hour, minute, second };
}
function civilDateKey(parts) {
    const y = String(parts.year).padStart(4, '0');
    const m = String(parts.month).padStart(2, '0');
    const d = String(parts.day).padStart(2, '0');
    return `${y}-${m}-${d}`;
}
function wallClockMinuteOfDay(parts) {
    return parts.hour * 60 + parts.minute + parts.second / 60;
}
function compareCivilKeys(a, b) {
    if (a === b)
        return 0;
    return a < b ? -1 : 1;
}
export function agendaMinuteOnDay(iso, timezone, todayKey) {
    const date = new Date(iso);
    const parts = civilPartsFromDate(date, timezone);
    if (!parts)
        return null;
    const key = civilDateKey(parts);
    const cmp = compareCivilKeys(key, todayKey);
    if (cmp < 0)
        return 0;
    if (cmp > 0)
        return AGENDA_DAY_MINUTES;
    const minute = wallClockMinuteOfDay(parts);
    if (minute <= 0)
        return 0;
    if (minute >= AGENDA_DAY_MINUTES)
        return AGENDA_DAY_MINUTES;
    return minute;
}
export function agendaCivilTodayKey(timezone, now = new Date()) {
    const parts = civilPartsFromDate(now, timezone);
    if (!parts)
        return civilDateKey({ year: now.getUTCFullYear(), month: now.getUTCMonth() + 1, day: now.getUTCDate(), hour: 0, minute: 0, second: 0 });
    return civilDateKey(parts);
}
export function agendaNowMinute(timezone, now = new Date()) {
    const parts = civilPartsFromDate(now, timezone);
    if (!parts)
        return null;
    const minute = wallClockMinuteOfDay(parts);
    if (minute < 0)
        return 0;
    if (minute > AGENDA_DAY_MINUTES)
        return AGENDA_DAY_MINUTES;
    return minute;
}
function timedRangeOnDay(event, timezone, todayKey) {
    if (event.allDay)
        return null;
    const startMinute = agendaMinuteOnDay(event.startsAt, timezone, todayKey);
    const endMinute = agendaMinuteOnDay(event.endsAt, timezone, todayKey);
    if (startMinute == null || endMinute == null)
        return null;
    return { startMinute, endMinute };
}
function clampRangeToWindow(startMinute, endMinute, window) {
    let start = Math.max(window.startMinute, Math.min(window.endMinute, startMinute));
    let end = Math.max(window.startMinute, Math.min(window.endMinute, endMinute));
    if (end - start < AGENDA_MIN_VISIBLE_MINUTES) {
        end = Math.min(window.endMinute, start + AGENDA_MIN_VISIBLE_MINUTES);
    }
    if (end <= start)
        return null;
    return { startMinute: start, endMinute: end };
}
export function agendaDayWindow() {
    return { startMinute: 0, endMinute: AGENDA_DAY_MINUTES };
}
export function agendaInitialFocusMinute(events, timezone, preferredStartMinutes, now = new Date()) {
    const preferred = Math.min(AGENDA_START_OF_DAY_MAX_MINUTE, Math.max(0, preferredStartMinutes));
    const todayKey = agendaCivilTodayKey(timezone, now);
    let earliest = Number.POSITIVE_INFINITY;
    for (const event of events) {
        const range = timedRangeOnDay(event, timezone, todayKey);
        if (!range)
            continue;
        if (range.startMinute < earliest)
            earliest = range.startMinute;
    }
    if (!Number.isFinite(earliest))
        return preferred;
    return Math.min(preferred, Math.min(AGENDA_START_OF_DAY_MAX_MINUTE, Math.max(0, earliest)));
}
export function layoutAgendaTimedEvents(events, timezone, window, now = new Date()) {
    const todayKey = agendaCivilTodayKey(timezone, now);
    const prepared = [];
    for (const event of events) {
        const range = timedRangeOnDay(event, timezone, todayKey);
        if (!range)
            continue;
        const clamped = clampRangeToWindow(range.startMinute, range.endMinute, window);
        if (!clamped)
            continue;
        prepared.push({
            event,
            startMinute: clamped.startMinute,
            endMinute: clamped.endMinute,
            column: 0,
            columnCount: 1,
        });
    }
    prepared.sort((a, b) => {
        if (a.startMinute !== b.startMinute)
            return a.startMinute - b.startMinute;
        if (a.endMinute !== b.endMinute)
            return a.endMinute - b.endMinute;
        return a.event.id.localeCompare(b.event.id);
    });
    const groups = [];
    let current = [];
    let groupMaxEnd = Number.NEGATIVE_INFINITY;
    for (const item of prepared) {
        if (current.length === 0 || item.startMinute < groupMaxEnd) {
            current.push(item);
            if (item.endMinute > groupMaxEnd)
                groupMaxEnd = item.endMinute;
        }
        else {
            groups.push(current);
            current = [item];
            groupMaxEnd = item.endMinute;
        }
    }
    if (current.length > 0)
        groups.push(current);
    for (const group of groups) {
        const columnEnds = [];
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
            }
            else {
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
function formatHourLabel(hour) {
    const date = new Date(Date.UTC(2026, 5, 15, hour, 0, 0));
    try {
        return date.toLocaleTimeString(undefined, { hour: 'numeric', timeZone: 'UTC' });
    }
    catch {
        return String(hour);
    }
}
function renderTimedCard(layout, timezone, window) {
    const startMin = layout.startMinute - window.startMinute;
    const spanMin = Math.max(layout.endMinute - layout.startMinute, 0);
    const widthPct = 100 / layout.columnCount;
    const leftPct = widthPct * layout.column;
    const style = `--agenda-start-min:${startMin};--agenda-span-min:${spanMin};left:calc(${leftPct}% + 2px);width:calc(${widthPct}% - 4px)`;
    return renderAgendaEventCard(layout.event, timezone, { extraClass: 'card--agenda-timed', style });
}
function renderDayGrid(window, layouts, timezone, now = new Date()) {
    const hours = [];
    for (let minute = window.startMinute; minute < window.endMinute; minute += 60) {
        const hour = Math.floor(minute / 60) % 24;
        hours.push(`<div class="agenda-hour"><span class="agenda-hour__label muted">${escapeHTML(formatHourLabel(hour))}</span></div>`);
    }
    const cards = layouts.map((layout) => renderTimedCard(layout, timezone, window)).join('');
    const todayKey = agendaCivilTodayKey(timezone, now);
    return `
    <div class="agenda-day" data-agenda-day="${escapeHTML(todayKey)}">
      <div class="agenda-day__hours">${hours.join('')}</div>
      <div class="agenda-day__events">${cards}</div>
      <div class="agenda-now-line" hidden aria-hidden="true"></div>
    </div>
  `;
}
export function buildAgendaColumnHtml(board, activeMobileTab, now = new Date()) {
    if (!isAgendaEnabled(board))
        return '';
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
    const allDayHtml = allDayEvents.length > 0
        ? `<div class="agenda-allday">${allDayEvents.map((event) => renderAgendaEventCard(event, timezone)).join('')}</div>`
        : '';
    const timedLayouts = layoutAgendaTimedEvents(events, timezone, window, now);
    const timedHtml = renderDayGrid(window, timedLayouts, timezone, now);
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
let agendaScrollBind = null;
let agendaNowBind = null;
let pendingInitialFocus = null;
let pendingFocusRaf = 0;
function abortAgendaNowLine() {
    agendaNowBind?.abort();
    agendaNowBind = null;
}
function agendaListIsLaidOut(list) {
    return list.clientHeight > 0;
}
function clearPendingInitialFocus() {
    pendingInitialFocus = null;
    if (pendingFocusRaf && typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
        window.cancelAnimationFrame(pendingFocusRaf);
    }
    pendingFocusRaf = 0;
}
function applyInitialFocusToList(list, board, now) {
    const events = agendaEvents(board);
    const timezone = board.agenda?.timezone || 'UTC';
    const focus = agendaInitialFocusMinute(events, timezone, agendaStartOfDayMinutes(), now);
    list.scrollTop = (focus / 60) * agendaHourHeightPx();
}
function schedulePendingInitialFocus() {
    if (pendingFocusRaf || !pendingInitialFocus)
        return;
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
        flushAgendaInitialScroll();
        return;
    }
    pendingFocusRaf = window.requestAnimationFrame(() => {
        pendingFocusRaf = 0;
        flushAgendaInitialScroll();
    });
}
/** Re-apply a deferred first snap once `#list_agenda` is laid out (e.g. mobile tab shown). */
export function flushAgendaInitialScroll() {
    if (!pendingInitialFocus)
        return;
    const list = document.getElementById(`list_${AGENDA_COLUMN_KEY}`);
    if (!(list instanceof HTMLElement)) {
        clearPendingInitialFocus();
        return;
    }
    if (!agendaListIsLaidOut(list))
        return;
    const board = pendingInitialFocus.board ?? getBoard();
    if (!board) {
        clearPendingInitialFocus();
        return;
    }
    applyInitialFocusToList(list, board, pendingInitialFocus.now);
    clearPendingInitialFocus();
}
export function captureAgendaListScroll() {
    const list = document.getElementById(`list_${AGENDA_COLUMN_KEY}`);
    if (!(list instanceof HTMLElement))
        return null;
    if (!agendaListIsLaidOut(list))
        return null;
    return list.scrollTop;
}
export function bindAgendaLaneScrollInteractions(list) {
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
    const endPan = (event) => {
        if (!dragging)
            return;
        dragging = false;
        if (typeof el.hasPointerCapture === 'function' && el.hasPointerCapture(event.pointerId)) {
            el.releasePointerCapture(event.pointerId);
        }
        col?.classList.remove('is-agenda-panning');
    };
    el.addEventListener('pointerdown', (event) => {
        if (event.pointerType !== 'mouse' || event.button !== 0)
            return;
        const gutter = 10;
        if (el.clientWidth > gutter && event.offsetX >= el.clientWidth - gutter)
            return;
        dragging = true;
        lastY = event.clientY;
        if (typeof el.setPointerCapture === 'function') {
            el.setPointerCapture(event.pointerId);
        }
        col?.classList.add('is-agenda-panning');
    }, { signal });
    el.addEventListener('pointermove', (event) => {
        if (!dragging)
            return;
        el.scrollTop -= event.clientY - lastY;
        lastY = event.clientY;
    }, { signal });
    el.addEventListener('pointerup', endPan, { signal });
    el.addEventListener('pointercancel', endPan, { signal });
}
export function applyAgendaNowLineAppearance(line) {
    const el = line ?? document.querySelector('.agenda-now-line');
    if (!(el instanceof HTMLElement))
        return;
    el.classList.toggle('agenda-now-line--prominent', isAgendaNowLineProminent());
}
export function syncAgendaNowLine(opts = {}) {
    const line = document.querySelector('.agenda-now-line');
    const day = line?.closest('.agenda-day');
    if (!(line instanceof HTMLElement) || !(day instanceof HTMLElement))
        return;
    applyAgendaNowLineAppearance(line);
    const board = opts.board ?? getBoard();
    const timezone = board?.agenda?.timezone || 'UTC';
    const now = opts.now ?? new Date();
    const todayKey = agendaCivilTodayKey(timezone, now);
    const gridDay = day.getAttribute('data-agenda-day');
    const minute = agendaNowMinute(timezone, now);
    if (!gridDay || gridDay !== todayKey || minute == null) {
        line.hidden = true;
        return;
    }
    line.hidden = false;
    line.style.setProperty('--agenda-now-min', String(minute));
}
export function bindAgendaNowLine(opts = {}) {
    abortAgendaNowLine();
    const line = document.querySelector('.agenda-now-line');
    if (!(line instanceof HTMLElement))
        return;
    agendaNowBind = new AbortController();
    const { signal } = agendaNowBind;
    syncAgendaNowLine(opts);
    const timer = window.setInterval(() => {
        syncAgendaNowLine({ board: opts.board ?? getBoard() });
    }, 1000);
    signal.addEventListener('abort', () => window.clearInterval(timer));
}
export function applyAgendaScrollAfterRender(opts = {}) {
    const list = document.getElementById(`list_${AGENDA_COLUMN_KEY}`);
    if (!(list instanceof HTMLElement)) {
        clearPendingInitialFocus();
        agendaScrollBind?.abort();
        agendaScrollBind = null;
        abortAgendaNowLine();
        return;
    }
    if (!opts.forceAutoFocus && opts.restoreScrollTop != null) {
        clearPendingInitialFocus();
        list.scrollTop = opts.restoreScrollTop;
    }
    else {
        const board = opts.board ?? getBoard();
        if (board) {
            applyInitialFocusToList(list, board, opts.now);
            if (!agendaListIsLaidOut(list)) {
                pendingInitialFocus = { board, now: opts.now };
                schedulePendingInitialFocus();
            }
            else {
                clearPendingInitialFocus();
            }
        }
    }
    bindAgendaLaneScrollInteractions(list);
    bindAgendaNowLine({ board: opts.board, now: opts.now });
}
export function syncOpenBoardAgendaLayout() {
    const board = getBoard();
    const col = document.querySelector('.col--agenda');
    if (!board || !col)
        return;
    const html = buildAgendaColumnHtml(board, getMobileTab());
    if (!html) {
        agendaScrollBind?.abort();
        agendaScrollBind = null;
        abortAgendaNowLine();
        col.remove();
        return;
    }
    col.outerHTML = html;
    applyAgendaScrollAfterRender({ forceAutoFocus: true, board });
}
function formatAgendaEventTime(event, timezone) {
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
function formatAgendaClockTime(iso, timezone) {
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) {
        return '';
    }
    const options = {
        hour: 'numeric',
        minute: '2-digit',
        timeZone: timezone || 'UTC',
    };
    try {
        return date.toLocaleTimeString(undefined, options);
    }
    catch {
        return date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
    }
}
