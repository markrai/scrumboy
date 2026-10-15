/**
 * Shared inner content of a story card (.card__content), used by member board
 * cards, Wall story pins, and public read-only board cards.
 *
 * The input is a small presentation model with no internal IDs, member data, or
 * app state, and the output carries no identity or interaction attributes:
 * each consumer keeps its own outer wrapper (data-todo-id vs.
 * data-public-local-id, drag handle, selection, Wall geometry).
 */
import { formatDueBadge, isOverdue } from '../dialogs/todo-due.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import { t } from '../i18n/index.js';
import { escapeHTML, sanitizeHexColor } from '../utils.js';
function tagHtml(tag, tone) {
    const safe = sanitizeHexColor(tag.color ?? undefined);
    const text = tone === 'accent' ? safe : 'var(--text)';
    const style = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${text};"` : "";
    return `<span class="tag" ${style}>${escapeHTML(tag.name)}</span>`;
}
function dueHtml(due) {
    if (!due?.dueAt)
        return "";
    const label = formatDueBadge(due.dueAt);
    if (!label)
        return "";
    const overdue = isOverdue(due.dueAt, due.inDoneColumn);
    const aria = escapeHTML(t("todo.due.cardLabel", { date: label }));
    return `<span class="card__due${overdue ? " card__due--overdue" : ""}" title="${aria}" aria-label="${aria}">${escapeHTML(label)}</span>`;
}
function priorityHtml(priority, tooltips) {
    if (!priority)
        return "";
    const color = sanitizeHexColor(priority.color ?? undefined);
    const style = color ? ` style="border-color:${color}; background:${color}20; color:${color};"` : "";
    const title = tooltips ? titleAttr(FIELD_TOOLTIPS.priority) : "";
    const name = escapeHTML(priority.name);
    return `<span class="card__priority"${style}${title} aria-label="${escapeHTML(t("todo.fields.priority"))}: ${name}">${name}</span>`;
}
function pointsHtml(points, tooltips) {
    if (points == null)
        return "";
    const title = tooltips ? titleAttr(FIELD_TOOLTIPS.estimationPoints) : "";
    return `<span class="card__points"${title} aria-label="${escapeHTML(t("todo.fields.estimationPoints"))}" data-i18n-aria-label="todo.fields.estimationPoints">${escapeHTML(String(points))}</span>`;
}
function buildContentHtml(input, options, trailingBadgeHtml) {
    const tags = input.tags.map((tag) => tagHtml(tag, options.tagTone)).join("");
    const footerContent = dueHtml(input.due)
        + priorityHtml(input.priority, options.tooltips)
        + pointsHtml(input.points, options.tooltips)
        + trailingBadgeHtml;
    return `
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${escapeHTML(String(input.localId))}</span>
          <span class="card__title">${escapeHTML(input.title)}</span>
        </div>
        ${tags || footerContent ? `
  <div class="card__tags">
    <span class="card__tags-list">
      ${tags}
    </span>
    <span class="card__badges">
      ${footerContent}
    </span>
  </div>
` : ""}
      </div>
  `;
}
/** Story card content built only from escaped presentation data. */
export function renderStoryCardContentHtml(input, options) {
    return buildContentHtml(input, options, "");
}
/**
 * Member-only variant: appends already-rendered, trusted badge HTML (the
 * assignee avatar) after the points badge. Only the authenticated board
 * adapter may call this; public modules must use renderStoryCardContentHtml.
 */
export function renderMemberStoryCardContentHtml(input, options, trustedMemberBadgeHtml) {
    return buildContentHtml(input, options, trustedMemberBadgeHtml);
}
