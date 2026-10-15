/**
 * Pure, dependency-light builders for the search filter popover shared by the
 * member board and the public read-only board. They emit presentation only:
 * callers choose the option attribute names (and therefore which controller can
 * ever react to a click), so the authenticated filter actions in
 * board-filters.ts and the public query updates stay separate.
 *
 * Must not import member state, the member filter controller, or anything that
 * performs requests.
 */
import { escapeHTML, sanitizeHexColor } from '../utils.js';
import { t } from '../i18n/index.js';
import { NO_PRIORITY_FILTER_VALUE } from '../types.js';
/**
 * Sprint options: All, optionally Scheduled/Unscheduled, then one option per
 * sprint keyed by sprint number. Sprints whose names collide get a
 * "Name (number)" label. `identity` names the sprint's unique key for
 * de-duplication explicitly: member sprints use their internal ID, public
 * sprints (which carry no internal ID) use their project-local number.
 */
export function buildSprintFilterOptions(sprints, opts) {
    const out = [
        { value: '', label: t('board.filters.allSprints'), i18nKey: 'board.filters.allSprints' },
    ];
    if (opts.includeScheduled) {
        out.push({ value: 'scheduled', label: t('board.filters.scheduled'), i18nKey: 'board.filters.scheduled' }, { value: 'unscheduled', label: t('board.filters.unscheduled'), i18nKey: 'board.filters.unscheduled' });
    }
    const nameCount = new Map();
    for (const sprint of sprints)
        nameCount.set(sprint.name, (nameCount.get(sprint.name) ?? 0) + 1);
    const seen = new Set();
    for (const sprint of sprints) {
        const key = opts.identity(sprint);
        if (seen.has(key))
            continue;
        seen.add(key);
        const label = (nameCount.get(sprint.name) ?? 0) > 1 ? `${sprint.name} (${sprint.number})` : sprint.name;
        out.push({ value: String(sprint.number), label, sprintState: sprint.state ?? '' });
    }
    return out;
}
/** Priority options: All, optionally No Priority, then each named tier. */
export function buildPriorityFilterOptions(tiers, opts) {
    const out = [
        { value: '', label: t('board.filters.allPriorities'), i18nKey: 'board.filters.allPriorities' },
    ];
    if (opts.includeNoPriority) {
        out.push({ value: NO_PRIORITY_FILTER_VALUE, label: t('board.filters.noPriority'), i18nKey: 'board.filters.noPriority' });
    }
    for (const tier of tiers)
        out.push({ value: tier.key, label: tier.name, color: tier.color ?? null });
    return out;
}
function sprintStateClass(state) {
    if (state === 'ACTIVE')
        return ' search-filter-option--active-sprint';
    if (state === 'CLOSED')
        return ' search-filter-option--closed-sprint';
    if (state === 'PLANNED')
        return ' search-filter-option--planned-sprint';
    return '';
}
/**
 * Renders option buttons. `attr` is the data attribute carrying each value;
 * it is the only hook a controller can match, so member and public panels
 * must pass distinct attributes. `ariaPressed` exposes the selection state to
 * assistive technology for panels whose controller keeps it in sync.
 */
export function renderFilterOptionsHtml(options, current, opts) {
    const attr = escapeHTML(opts.attr);
    return options
        .map((option) => {
        const active = current === option.value;
        const cls = `search-filter-option${active ? ' is-active' : ''}${sprintStateClass(option.sprintState)}`;
        const i18n = option.i18nKey ? ` data-i18n-text="${escapeHTML(option.i18nKey)}"` : '';
        const pressed = opts.ariaPressed ? ` aria-pressed="${active ? 'true' : 'false'}"` : '';
        const safe = option.color ? sanitizeHexColor(option.color) : null;
        const style = safe ? ` style="border-color: ${safe}; background: ${safe}20;"` : '';
        return `<button type="button" class="${cls}" ${attr}="${escapeHTML(option.value)}"${i18n}${pressed}${style}>${escapeHTML(option.label)}</button>`;
    })
        .join('');
}
/** One labelled group inside the popover. `sectionAttr` is an optional bare marker attribute. */
export function buildFilterPanelSectionHtml(args) {
    const marker = args.sectionAttr ? ` ${escapeHTML(args.sectionAttr)}` : '';
    return `
      <div class="search-filter-panel__section"${marker}>
        <div class="search-filter-panel__label" data-i18n-text="${escapeHTML(args.labelKey)}">${escapeHTML(t(args.labelKey))}</div>
        ${args.optionsHtml}
      </div>`;
}
/** The chevron toggle on the search input. */
export function buildFilterToggleHtml(args) {
    const label = escapeHTML(t('board.filters.openFilters'));
    return `
    <button
      type="button"
      class="search-filter-toggle${args.active ? ' search-filter-toggle--active' : ''}"
      id="${escapeHTML(args.toggleId)}"
      aria-expanded="false"
      aria-haspopup="true"
      aria-controls="${escapeHTML(args.panelId)}"
      aria-label="${label}"
      title="${label}"
      data-i18n-aria-label="board.filters.openFilters"
      data-i18n-title="board.filters.openFilters"${args.hidden ? '\n      hidden' : ''}
    >
      <svg width="10" height="6" viewBox="0 0 10 6" fill="none" aria-hidden="true" focusable="false">
        <path d="M1 1L5 5L9 1" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </button>`;
}
/** Toggle plus the (initially hidden) popover containing the given sections. */
export function buildFilterPanelShellHtml(args) {
    return `${buildFilterToggleHtml({ toggleId: args.toggleId, panelId: args.panelId, active: args.active, hidden: args.toggleHidden })}
    <div class="search-filter-panel" id="${escapeHTML(args.panelId)}" hidden>
      ${args.sectionsHtml}
    </div>
  `;
}
