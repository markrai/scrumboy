// @vitest-environment happy-dom
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../i18n/index.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../i18n/index.js')>()),
  t: (key: string) => key,
}));

import { NO_PRIORITY_FILTER_VALUE } from '../types.js';
import {
  buildFilterPanelSectionHtml,
  buildFilterPanelShellHtml,
  buildPriorityFilterOptions,
  buildSprintFilterOptions,
  renderFilterOptionsHtml,
} from './filter-panel-markup.js';
import { buildFilterPanelHtml, buildSprintFilterSectionHtml } from './board-rendering.js';

const hostile = '"><img src=x onerror=alert(1)>';

function host(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  return el;
}

describe('shared filter panel markup', () => {
  it('builds only Scheduled and Unscheduled sprint options', () => {
    const options = buildSprintFilterOptions();
    expect(options.map((o) => [o.value, o.label, o.i18nKey])).toEqual([
      ['scheduled', 'board.filters.scheduled', 'board.filters.scheduled'],
      ['unscheduled', 'board.filters.unscheduled', 'board.filters.unscheduled'],
    ]);
    expect(options.some((o) => o.value === '' || o.i18nKey === 'board.filters.allSprints')).toBe(false);
  });

  it('builds priority options with and without No Priority', () => {
    const tiers = [{ key: 'high', name: 'High', color: '#ff0000' }];
    expect(buildPriorityFilterOptions(tiers, { includeNoPriority: true }).map((o) => o.value)).toEqual(['', NO_PRIORITY_FILTER_VALUE, 'high']);
    expect(buildPriorityFilterOptions(tiers, { includeNoPriority: false }).map((o) => o.value)).toEqual(['', 'high']);
  });

  it('renders options with the caller attribute, active and sprint-state classes, and escaping', () => {
    const html = renderFilterOptionsHtml([
      { value: '', label: 'All', i18nKey: 'board.filters.allSprints' },
      { value: '7', label: hostile, sprintState: 'ACTIVE' },
      { value: 'p"x', label: 'P', color: '#00ff00' },
      { value: 'bad', label: 'Bad', color: 'url(evil)' },
    ], '7', { attr: 'data-x-option' });
    expect(html).toContain('class="search-filter-option is-active search-filter-option--active-sprint" data-x-option="7"');
    expect(html).toContain('data-x-option="" data-i18n-text="board.filters.allSprints"');
    expect(html).not.toContain('aria-pressed');
    const root = host(html);
    expect(root.querySelector('img')).toBeNull();
    expect(root.querySelector('[data-x-option="7"]')?.textContent).toBe(hostile);
    const tier = Array.from(root.querySelectorAll('[data-x-option]')).find((o) => o.getAttribute('data-x-option') === 'p"x')!;
    expect(tier.getAttribute('style')).toBe('border-color: #00ff00; background: #00ff0020;');
    expect(root.querySelector('[data-x-option="bad"]')?.hasAttribute('style')).toBe(false);
    expect(html).not.toContain('url(evil)');
  });

  it('can expose selection through aria-pressed', () => {
    const root = host(renderFilterOptionsHtml([{ value: '', label: 'All' }, { value: '1', label: 'One' }], '1', { attr: 'data-y', ariaPressed: true }));
    expect(root.querySelector('[data-y=""]')?.getAttribute('aria-pressed')).toBe('false');
    expect(root.querySelector('[data-y="1"]')?.getAttribute('aria-pressed')).toBe('true');
  });

  it('renders the shell with caller ids, a hidden panel, and an optional hidden toggle', () => {
    const root = host(buildFilterPanelShellHtml({
      toggleId: 'tId',
      panelId: 'pId',
      active: true,
      sectionsHtml: buildFilterPanelSectionHtml({ labelKey: 'board.filters.priority', optionsHtml: '<button type="button">x</button>', sectionAttr: 'data-s' }),
    }));
    const toggle = root.querySelector<HTMLButtonElement>('#tId')!;
    expect(toggle.classList.contains('search-filter-toggle--active')).toBe(true);
    expect(toggle.getAttribute('aria-controls')).toBe('pId');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(toggle.hidden).toBe(false);
    const panel = root.querySelector<HTMLElement>('#pId')!;
    expect(panel.hidden).toBe(true);
    expect(panel.querySelector('[data-s] .search-filter-panel__label')?.textContent).toBe('board.filters.priority');

    const hidden = host(buildFilterPanelShellHtml({ toggleId: 't', panelId: 'p', active: false, sectionsHtml: '', toggleHidden: true }));
    expect(hidden.querySelector<HTMLButtonElement>('#t')!.hidden).toBe(true);
  });
});

describe('shared filter panel module boundaries', () => {
  function importsOf(file: string): string[] {
    const source = readFileSync(resolve(process.cwd(), 'modules/views', file), 'utf8');
    return Array.from(source.matchAll(/from\s+'([^']+)'/g)).map((m) => m[1]);
  }

  it('shared modules stay dependency-light', () => {
    expect(importsOf('filter-panel-markup.ts').sort()).toEqual(['../i18n/index.js', '../types.js', '../utils.js']);
    expect(importsOf('filter-popover.ts')).toEqual([]);
  });

  it('public modules never import the member filter controller or member board modules', () => {
    for (const file of ['public-board.ts', 'public-board-rendering.ts']) {
      const imports = importsOf(file);
      for (const forbidden of ['./board-filters.js', './board-rendering.js', './board.js', '../state/board-filter-url.js']) {
        expect(imports, `${file} -> ${forbidden}`).not.toContain(forbidden);
      }
    }
  });
});

describe('member filter panel through the shared markup', () => {
  it('keeps the member sprint section, ids, and option attributes with only Scheduled/Unscheduled', () => {
    const root = host(buildSprintFilterSectionHtml('assigned', {
      sprints: [
        { id: 1, number: 1, name: 'Dup', state: 'CLOSED' },
        { id: 2, number: 2, name: 'Dup', state: 'ACTIVE' },
        { id: 2, number: 2, name: 'Dup', state: 'ACTIVE' },
      ],
    }));
    const section = root.querySelector('[data-sprint-filter-section]')!;
    expect(Array.from(section.querySelectorAll('[data-sprint-option]')).map((o) => [o.getAttribute('data-sprint-option'), o.textContent])).toEqual([
      ['scheduled', 'board.filters.scheduled'],
      ['unscheduled', 'board.filters.unscheduled'],
    ]);
    // Legacy "assigned" still selects Scheduled.
    expect(section.querySelector('.is-active')?.getAttribute('data-sprint-option')).toBe('scheduled');
    expect(section.querySelector('[aria-pressed], [data-public-sprint-option]')).toBeNull();
  });

  it('omits the member sprint section without sprints unless a sprint filter is active', () => {
    expect(buildSprintFilterSectionHtml(null, null)).toBe('');
    expect(buildSprintFilterSectionHtml(null, { sprints: [] })).toBe('');
    const root = host(buildSprintFilterSectionHtml('unscheduled', { sprints: [] }));
    expect(root.querySelector('[data-sprint-option="unscheduled"]')?.classList.contains('is-active')).toBe(true);
  });

  it('shows a numeric sprint deep link as one selected, state-styled option', () => {
    const sprints = Array.from({ length: 3 }, (_, i) => ({ id: 100 + i, number: i + 1, name: `S${i + 1}`, state: i === 1 ? 'CLOSED' : 'PLANNED' }));
    const known = host(buildSprintFilterSectionHtml('2', { sprints }));
    expect(Array.from(known.querySelectorAll('[data-sprint-option]')).map((o) => [o.getAttribute('data-sprint-option'), o.textContent])).toEqual([
      ['scheduled', 'board.filters.scheduled'],
      ['unscheduled', 'board.filters.unscheduled'],
      ['2', 'S2'],
    ]);
    const option = known.querySelector('[data-sprint-option="2"]')!;
    expect(option.classList.contains('is-active')).toBe(true);
    expect(option.classList.contains('search-filter-option--closed-sprint')).toBe(true);
    // Not yet loaded (or unknown): still visible and clearable by number.
    const pending = host(buildSprintFilterSectionHtml('9', null));
    expect(pending.querySelector('[data-sprint-option="9"]')?.textContent).toBe('#9');
  });

  it('keeps assignee, sort, and priority (with No Priority) under the member ids', () => {
    const root = host(buildFilterPanelHtml(null, null, [], null, NO_PRIORITY_FILTER_VALUE, [{ key: 'high', name: hostile, color: '#ff0000', position: 0 }]));
    expect(root.querySelector('#searchFilterToggle')?.classList.contains('search-filter-toggle--active')).toBe(true);
    expect(root.querySelector<HTMLElement>('#searchFilterPanel')?.hidden).toBe(true);
    expect(root.querySelectorAll('[data-assignee-option]').length).toBeGreaterThan(0);
    expect(root.querySelectorAll('[data-sort-option]').length).toBe(4);
    expect(Array.from(root.querySelectorAll('[data-priority-option]')).map((o) => o.getAttribute('data-priority-option'))).toEqual(['', NO_PRIORITY_FILTER_VALUE, 'high']);
    expect(root.querySelector(`[data-priority-option="${NO_PRIORITY_FILTER_VALUE}"]`)?.classList.contains('is-active')).toBe(true);
    expect(root.querySelector('[data-priority-option="high"]')?.textContent).toBe(hostile);
    expect(root.querySelector('img, [data-public-priority-option], #publicFilterToggle')).toBeNull();
  });
});
