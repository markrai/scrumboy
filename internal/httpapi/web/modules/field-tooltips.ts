import { t } from './i18n/index.js';

function escapeHTML(s: string): string {
  return String(s)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

export const FIELD_TOOLTIP_MESSAGE_KEYS = {
  estimationPoints: 'tooltips.estimationPoints',
  sprintTodo: 'tooltips.sprintTodo',
  status: 'tooltips.status',
  linkedStories: 'tooltips.linkedStories',
  sprintName: 'tooltips.sprintName',
  sprintStart: 'tooltips.sprintStart',
  sprintEnd: 'tooltips.sprintEnd',
  sprintDefaultWeeks: 'tooltips.sprintDefaultWeeks',
  doneLane: 'tooltips.doneLane',
  workflowAddLane: 'tooltips.workflowAddLane',
  priorityAddTier: 'tooltips.priorityAddTier',
  priority: 'tooltips.priority',
  tags: 'tooltips.tags',
  boardSearch: 'tooltips.boardSearch',
  sprintFilterScheduled: 'tooltips.sprintFilterScheduled',
  sprintFilterUnscheduled: 'tooltips.sprintFilterUnscheduled',
  sprintFilterActive: 'tooltips.sprintFilterActive',
  voiceCommand: 'tooltips.voiceCommand',
  memberRole: 'tooltips.memberRole',
} as const;

export type FieldTooltipKey = keyof typeof FIELD_TOOLTIP_MESSAGE_KEYS;

function isFieldTooltipKey(value: string): value is FieldTooltipKey {
  return Object.prototype.hasOwnProperty.call(FIELD_TOOLTIP_MESSAGE_KEYS, value);
}

export function fieldTooltip(key: FieldTooltipKey): string {
  return t(FIELD_TOOLTIP_MESSAGE_KEYS[key]);
}

const dynamicFieldTooltips: Partial<Record<FieldTooltipKey, string>> = {};
for (const key of Object.keys(FIELD_TOOLTIP_MESSAGE_KEYS) as FieldTooltipKey[]) {
  Object.defineProperty(dynamicFieldTooltips, key, {
    enumerable: true,
    get: () => fieldTooltip(key),
  });
}

export const FIELD_TOOLTIPS = Object.freeze(dynamicFieldTooltips) as Readonly<Record<FieldTooltipKey, string>>;

export function titleAttr(tip: string): string {
  return ` title="${escapeHTML(tip)}"`;
}

export function fieldLabelHTML(label: string, tip: string, i18nKey?: string): string {
  const i18nAttr = i18nKey ? ` data-i18n-text="${escapeHTML(i18nKey)}"` : '';
  return `<div class="field__label"${titleAttr(tip)}${i18nAttr}>${escapeHTML(label)}</div>`;
}

/** Apply native title tooltips to elements matching selectors within root (defaults to document). */
export function applyFieldTooltips(
  bindings: Record<string, FieldTooltipKey | string>,
  root: ParentNode = document,
): void {
  for (const [selector, keyOrTip] of Object.entries(bindings)) {
    const el = root.querySelector(selector);
    if (!el) continue;
    const usesCatalogKey = isFieldTooltipKey(keyOrTip);
    const tip = usesCatalogKey ? fieldTooltip(keyOrTip) : keyOrTip;
    el.setAttribute('title', tip);
    if (usesCatalogKey) {
      el.setAttribute('data-i18n-title', FIELD_TOOLTIP_MESSAGE_KEYS[keyOrTip]);
    } else {
      el.removeAttribute('data-i18n-title');
    }
  }
}

export const TODO_DIALOG_TOOLTIPS: Record<string, FieldTooltipKey> = {
  '#todoEstimationField .field__label': 'estimationPoints',
  '#todoSprintField .field__label': 'sprintTodo',
  '#todoForm .field:has(#todoStatus) .field__label': 'status',
  '#todoLinksField .field__label': 'linkedStories',
  '#linksSearchInput': 'linkedStories',
  '#todoForm label.field:has(#todoTags) .field__label': 'tags',
  '#todoTags': 'tags',
};

export const BULK_EDIT_TOOLTIPS: Record<string, FieldTooltipKey> = {
  '#bulkEstimation': 'estimationPoints',
  '#bulkEditEstimationRow .bulk-edit-field__check span': 'estimationPoints',
  '#bulkSprint': 'sprintTodo',
  '#bulkEditSprintRow .bulk-edit-field__check span': 'sprintTodo',
  '#bulkStatus': 'status',
  '#bulkEditForm .bulk-edit-field:has(#bulkStatus) .bulk-edit-field__check span': 'status',
  '#bulkTagsInput': 'tags',
  '#bulkEditTagsRow .bulk-edit-field__check span': 'tags',
};
