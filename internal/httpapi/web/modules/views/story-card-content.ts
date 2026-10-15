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

export type StoryCardTag = { readonly name: string; readonly color: string | null };
export type StoryCardPriority = { readonly name: string; readonly color: string | null };
export type StoryCardDue = { readonly dueAt: string; readonly inDoneColumn: boolean };

export interface StoryCardContentInput {
  /** Project-local story number. */
  readonly localId: number;
  readonly title: string;
  /** Rendered in the given order. */
  readonly tags: readonly StoryCardTag[];
  readonly priority: StoryCardPriority | null;
  /** null hides the badge; callers apply their estimation-mode rule. */
  readonly points: number | null;
  readonly due: StoryCardDue | null;
}

export interface StoryCardContentOptions {
  /** 'accent' colors tag text with the tag color; 'theme' keeps the theme foreground. */
  readonly tagTone: 'accent' | 'theme';
  /** Field tooltips on the priority and points badges. */
  readonly tooltips: boolean;
}

function tagHtml(tag: StoryCardTag, tone: StoryCardContentOptions['tagTone']): string {
  const safe = sanitizeHexColor(tag.color ?? undefined);
  const text = tone === 'accent' ? safe : 'var(--text)';
  const style = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${text};"` : "";
  return `<span class="tag" ${style}>${escapeHTML(tag.name)}</span>`;
}

function dueHtml(due: StoryCardDue | null): string {
  if (!due?.dueAt) return "";
  const label = formatDueBadge(due.dueAt);
  if (!label) return "";
  const overdue = isOverdue(due.dueAt, due.inDoneColumn);
  const aria = escapeHTML(t("todo.due.cardLabel", { date: label }));
  return `<span class="card__due${overdue ? " card__due--overdue" : ""}" title="${aria}" aria-label="${aria}">${escapeHTML(label)}</span>`;
}

function priorityHtml(priority: StoryCardPriority | null, tooltips: boolean): string {
  if (!priority) return "";
  const color = sanitizeHexColor(priority.color ?? undefined);
  const style = color ? ` style="border-color:${color}; background:${color}20; color:${color};"` : "";
  const title = tooltips ? titleAttr(FIELD_TOOLTIPS.priority) : "";
  const name = escapeHTML(priority.name);
  return `<span class="card__priority"${style}${title} aria-label="${escapeHTML(t("todo.fields.priority"))}: ${name}">${name}</span>`;
}

function pointsHtml(points: number | null, tooltips: boolean): string {
  if (points == null) return "";
  const title = tooltips ? titleAttr(FIELD_TOOLTIPS.estimationPoints) : "";
  return `<span class="card__points"${title} aria-label="${escapeHTML(t("todo.fields.estimationPoints"))}" data-i18n-aria-label="todo.fields.estimationPoints">${escapeHTML(String(points))}</span>`;
}

function buildContentHtml(input: StoryCardContentInput, options: StoryCardContentOptions, trailingBadgeHtml: string): string {
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
export function renderStoryCardContentHtml(input: StoryCardContentInput, options: StoryCardContentOptions): string {
  return buildContentHtml(input, options, "");
}

/**
 * Member-only variant: appends already-rendered, trusted badge HTML (the
 * assignee avatar) after the points badge. Only the authenticated board
 * adapter may call this; public modules must use renderStoryCardContentHtml.
 */
export function renderMemberStoryCardContentHtml(
  input: StoryCardContentInput,
  options: StoryCardContentOptions,
  trustedMemberBadgeHtml: string,
): string {
  return buildContentHtml(input, options, trustedMemberBadgeHtml);
}
