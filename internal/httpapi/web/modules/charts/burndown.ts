import { RealBurndownPoint } from '../types.js';

/** uPlot is loaded via script tag and exposed globally */
declare global {
  interface Window {
    uPlot?: new (
      opts: any,
      data: any,
      targ: HTMLElement
    ) => {
      destroy: () => void;
      setSize?: (size: { width: number; height: number }) => void;
      setLegend?: (opts: { idx?: number | null; idxs?: Array<number | null> }) => void;
    };
  }
}

/** Sprint info for filtering burndown by sprint date range. */
export interface BurndownSprintFilter {
  name: string;
  plannedStartAt: number;
  plannedEndAt: number;
}

const MS_PER_DAY = 24 * 60 * 60 * 1000;

function toNumeric(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
}

function toTimestampMs(value: string | number): number | null {
  const ts = new Date(value).getTime();
  return Number.isFinite(ts) ? ts : null;
}

function formatShortDate(tsMs: number): string {
  return new Date(tsMs).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}

function resolveTimeDomainMs(
  chartData: RealBurndownPoint[],
  currentSprint?: BurndownSprintFilter | null
): { startMs: number; endMs: number; durationMs: number } | null {
  const firstDataMs = toTimestampMs(chartData[0]?.date ?? '');
  const lastDataMs = toTimestampMs(chartData[chartData.length - 1]?.date ?? '');
  if (firstDataMs == null || lastDataMs == null) {
    return null;
  }

  let startMs = firstDataMs;
  let endMs = lastDataMs;

  if (currentSprint) {
    const sprintStartMs = toTimestampMs(currentSprint.plannedStartAt);
    const sprintEndMs = toTimestampMs(currentSprint.plannedEndAt);
    if (sprintStartMs != null && sprintEndMs != null && sprintEndMs >= sprintStartMs) {
      startMs = sprintStartMs;
      endMs = sprintEndMs;
    }
  }

  if (endMs <= startMs) {
    endMs = startMs + MS_PER_DAY;
  }

  return {
    startMs,
    endMs,
    durationMs: Math.max(endMs - startMs, 1),
  };
}

/** Filter and sort data; optionally restrict to sprint range. */
function prepareChartData(
  data: RealBurndownPoint[],
  currentSprint?: BurndownSprintFilter | null,
  dataIsSprintScoped?: boolean
): RealBurndownPoint[] {
  let chartData = data
    .filter((p) => toTimestampMs(p.date) != null)
    .sort((a, b) => (toTimestampMs(a.date)! - toTimestampMs(b.date)!));

  // Skip date filter when data is already sprint-scoped (from backend sprint endpoint)
  if (currentSprint && !dataIsSprintScoped) {
    const startMs = toTimestampMs(currentSprint.plannedStartAt);
    const endMs = toTimestampMs(currentSprint.plannedEndAt);
    if (startMs != null && endMs != null) {
      chartData = chartData.filter((p) => {
        const t = toTimestampMs(p.date)!;
        return t >= startMs && t <= endMs;
      });
    }
  }

  return chartData;
}

/** Renders the chart wrapper HTML (header, nav, mount div). Does not draw the chart. */
export function renderRealBurndownChart(
  data: RealBurndownPoint[],
  currentSprint?: BurndownSprintFilter | null,
  sprintNav?: { canPrev: boolean; canNext: boolean },
  dataIsSprintScoped?: boolean
): string {
  const chartData = Array.isArray(data) && data.length > 0 ? prepareChartData(data, currentSprint, dataIsSprintScoped) : [];
  const validPoints = chartData.filter((p) => toNumeric(p.remainingPoints) != null);
  const validWork = chartData.filter((p) => toNumeric(p.remainingWork) != null);
  const hasDataToPlot = validPoints.length > 0 || validWork.length > 0;
  const domain = chartData.length > 0 ? resolveTimeDomainMs(chartData, currentSprint) : null;

  let dateRangeSubtitle: string;
  if (domain) {
    dateRangeSubtitle = currentSprint
      ? `${currentSprint.name} | ${formatShortDate(domain.startMs)} - ${formatShortDate(domain.endMs)}`
      : `${formatShortDate(domain.startMs)} - ${formatShortDate(domain.endMs)}`;
  } else if (currentSprint) {
    dateRangeSubtitle = `${currentSprint.name} | No data`;
  } else {
    dateRangeSubtitle = 'No data available';
  }

  let noDataMessage: string | null = null;
  if (!Array.isArray(data) || data.length === 0) {
    noDataMessage = 'No data available. Create some todos to see the burndown chart.';
  } else if (chartData.length === 0) {
    noDataMessage = 'No data for this sprint. Create some todos during the sprint to see the burndown chart.';
  } else if (!hasDataToPlot) {
    noDataMessage = 'No usable burndown data available.';
  }

  const subtitleWithNav =
    currentSprint && sprintNav
      ? `<div class="burndown-chart__subtitle-row">
          <button class="burndown-chart__nav-arrow" id="burndown-prev" ${!sprintNav.canPrev ? 'disabled' : ''} type="button" aria-label="Previous sprint">&#9664;</button>
          <div class="burndown-chart__subtitle muted">${dateRangeSubtitle}</div>
          <button class="burndown-chart__nav-arrow" id="burndown-next" ${!sprintNav.canNext ? 'disabled' : ''} type="button" aria-label="Next sprint">&#9654;</button>
        </div>`
      : `<div class="burndown-chart__subtitle muted">${dateRangeSubtitle}</div>`;

  const mountContent = noDataMessage
    ? `<div class="burndown-chart__no-data muted">${noDataMessage}</div>`
    : '';

  return `
    <div class="burndown-chart">
      <div class="burndown-chart__header">
        <div>
          <div class="burndown-chart__title">Real Burndown</div>
          ${subtitleWithNav}
        </div>
      </div>
      <div class="burndown-chart__container">
        <div id="burndown-uplot-mount" class="burndown-chart__uplot">${mountContent}</div>
      </div>
    </div>
  `;
}

let uplotInstance: InstanceType<NonNullable<typeof window.uPlot>> | null = null;

/** Destroys the current uPlot instance if any. Call before re-rendering the chart container. */
export function destroyBurndownChart(): void {
  if (uplotInstance) {
    try {
      uplotInstance.destroy();
    } catch (_) {
      /* noop */
    }
    uplotInstance = null;
  }
}

interface UplotDataBuildResult {
  x: number[];
  primarySeries: (number | null)[];
  idealSeries: (number | null)[];
  visibleCount: number;
  usePoints: boolean;
  lastValueIdx: number | null;
}

/**
 * Build uPlot AlignedData arrays.
 * - Uses remainingPoints when available, else remainingWork.
 * - Adds sprint/domain boundaries to X so the ideal line spans the full window.
 */
function buildUplotData(
  chartData: RealBurndownPoint[],
  initialScope: number,
  initialScopePoints: number | null,
  currentSprint?: BurndownSprintFilter | null
): UplotDataBuildResult {
  const domain = resolveTimeDomainMs(chartData, currentSprint);
  if (!domain) {
    return {
      x: [],
      primarySeries: [],
      idealSeries: [],
      visibleCount: 0,
      usePoints: false,
      lastValueIdx: null,
    };
  }

  const { startMs, endMs, durationMs } = domain;
  const startSec = Math.floor(startMs / 1000);
  const endSec = Math.ceil(endMs / 1000);

  const hasAnyPoints = chartData.some((p) => toNumeric(p.remainingPoints) != null);
  const usePoints = hasAnyPoints && initialScopePoints != null;
  const totalScope =
    usePoints && initialScopePoints != null ? initialScopePoints : initialScope;

  const pointBySecond = new Map<number, number | null>();
  for (const p of chartData) {
    const ts = toTimestampMs(p.date);
    if (ts == null) continue;
    const xSec = Math.round(ts / 1000);
    const value = usePoints ? toNumeric(p.remainingPoints) : toNumeric(p.remainingWork);
    pointBySecond.set(xSec, value);
  }

  const x = Array.from(new Set<number>([startSec, endSec, ...pointBySecond.keys()])).sort((a, b) => a - b);

  const primarySeries: (number | null)[] = [];
  const idealSeries: (number | null)[] = [];
  const slopePerDay = durationMs > 0 ? totalScope / (durationMs / MS_PER_DAY) : 0;

  let visibleCount = 0;
  let lastValueIdx: number | null = null;

  for (let i = 0; i < x.length; i++) {
    const xSec = x[i];
    let value = pointBySecond.has(xSec) ? pointBySecond.get(xSec)! : null;
    // Ensure primary series has start value when sprint has no progress yet
    if (value == null && i === 0 && xSec === startSec) {
      value = totalScope;
    }
    primarySeries.push(value);

    if (value != null) {
      visibleCount++;
      lastValueIdx = i;
    }

    const elapsedDays = (xSec * 1000 - startMs) / MS_PER_DAY;
    const idealRaw = totalScope - slopePerDay * elapsedDays;
    const idealValue = Math.max(0, Math.min(totalScope, idealRaw));
    idealSeries.push(Number.isFinite(idealValue) ? idealValue : null);
  }

  return {
    x,
    primarySeries,
    idealSeries,
    visibleCount,
    usePoints,
    lastValueIdx,
  };
}

/** Resolve CSS variable to a value usable by canvas (e.g. for dark mode). */
function getThemeColor(varName: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(varName).trim();
  return value || '#888';
}

function computeYExtents(alignedYSeries: Array<Array<number | null>>): { min: number; max: number } {
  const values: number[] = [];
  for (const series of alignedYSeries) {
    for (const v of series) {
      if (typeof v === 'number' && Number.isFinite(v)) {
        values.push(v);
      }
    }
  }

  if (values.length === 0) {
    return { min: -1, max: 1 };
  }

  let min = Math.min(...values);
  let max = Math.max(...values);

  // Flat-line padding so constant series do not collapse onto axis borders.
  if (min === max) {
    const pad = Math.max(Math.abs(min) * 0.1, 1);
    return { min: min - pad, max: max + pad };
  }

  // Keep a zero baseline for burndown while still giving top breathing room.
  min = Math.min(min, 0);
  const span = max - min;
  return { min, max: max + span * 0.08 };
}

/** Mounts a uPlot burndown chart into the given container. */
export function mountBurndownChart(
  container: HTMLElement,
  data: RealBurndownPoint[],
  currentSprint?: BurndownSprintFilter | null,
  dataIsSprintScoped?: boolean
): void {
  const uPlot = window.uPlot;
  if (!uPlot) {
    container.innerHTML = "<div class='muted'>Chart library not loaded.</div>";
    return;
  }

  destroyBurndownChart();

  const chartData = prepareChartData(data, currentSprint, dataIsSprintScoped);
  if (chartData.length === 0) {
    return; // renderRealBurndownChart already put a message in the container
  }

  const border = getThemeColor('--border');
  const text = getThemeColor('--text');
  const muted = getThemeColor('--muted');
  const accent = getThemeColor('--accent');

  const initialScope =
    toNumeric(chartData[0]?.initialScope) ?? 0;
  const initialScopePoints =
    toNumeric(chartData[0]?.initialScopePoints) != null
      ? (toNumeric(chartData[0]?.initialScopePoints) as number)
      : null;

  const {
    x,
    primarySeries,
    idealSeries,
    visibleCount,
    usePoints,
    lastValueIdx,
  } = buildUplotData(chartData, initialScope, initialScopePoints, currentSprint);

  // Compute footer values from the same data the chart uses
  const domain = resolveTimeDomainMs(chartData, currentSprint);
  const durationDays = domain ? Math.ceil(domain.durationMs / MS_PER_DAY) : 0;
  const remaining =
    lastValueIdx != null ? primarySeries[lastValueIdx] : null;
  const totalScope =
    usePoints && initialScopePoints != null ? initialScopePoints : initialScope;
  const idealPointsPerDay =
    durationDays > 0 ? totalScope / durationDays : null;

  const durationDaysDisplay =
    durationDays > 0 ? String(durationDays) : '—';
  const remainingDisplay =
    remaining != null ? String(remaining) : '—';
  const idealLabel =
    idealPointsPerDay != null
      ? `Ideal Pace: ${idealPointsPerDay.toFixed(1)}/day`
      : 'Ideal Pace: —';

  // Remove existing footer to prevent duplicates on remount
  const chartRoot = container.closest('.burndown-chart');
  const existingFooter = chartRoot?.querySelector('.burndown-chart__footer');
  if (existingFooter) {
    existingFooter.remove();
  }

  const footerEl = document.createElement('div');
  footerEl.className = 'burndown-chart__footer';
  footerEl.innerHTML = `
    <span>Days: ${durationDaysDisplay}</span>
    <span>Remaining: ${remainingDisplay}</span>
    <span>${idealLabel}</span>
  `;
  chartRoot?.appendChild(footerEl);

  if (x.length === 0) {
    return; // footer already rendered with placeholders
  }

  const series: any[] = [
    {}, // x
    {
      label: 'Remaining',
      stroke: accent,
      width: 2,
      points: {
        show: true,
        size: 6,
        width: 2,
        stroke: accent,
        fill: '#ffffff',
      },
      spanGaps: true,
    },
    {
      label: 'Ideal',
      stroke: muted,
      width: 1.5,
      dash: [4, 4],
      points: { show: false },
      spanGaps: true,
    },
  ];

  const alignedData: any[] = [x, primarySeries, idealSeries];

  const lengths = alignedData.map((arr) => arr.length);
  const sameLength = lengths.every((len) => len === lengths[0]);
  if (!sameLength) {
    console.warn('Burndown alignedData length mismatch', lengths);
    container.innerHTML = "<div class='muted'>Chart data is misaligned.</div>";
    return;
  }

  const xMin = x[0];
  const xMax = x.length > 1 ? x[x.length - 1] : xMin + 1;
  const yExtents = computeYExtents(
    alignedData.slice(1).map((s) => s as Array<number | null>)
  );
  const chartWidth = Math.max(container.clientWidth || 0, 320);
  console.debug('[burndown:uplot] mount', {
    chartRows: chartData.length,
    xCount: x.length,
    visibleCount,
    usePoints,
    lastValueIdx,
    xMin,
    xMax,
    yMin: yExtents.min,
    yMax: yExtents.max,
    initialScope,
    initialScopePoints,
  });

  const opts: any = {
    width: chartWidth,
    height: 240,
    series,
    scales: {
      x: { time: true, auto: false, min: xMin, max: xMax },
      y: { auto: false, min: yExtents.min, max: yExtents.max },
    },
    axes: [
      {
        stroke: text,
        grid: { show: true, stroke: border, width: 0.5 },
        ticks: { show: true },
        font: '12px system-ui, sans-serif',
        values: (_u: any, vals: number[]) =>
          vals.map((v) => {
            const d = new Date(v * 1000); // scale values are in seconds
            return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
          }),
      },
      {
        stroke: text,
        grid: { show: true, stroke: border, width: 0.5 },
        ticks: { show: true },
        font: '12px system-ui, sans-serif',
        values: (_u: any, vals: number[]) => vals.map((v) => String(Math.round(v))),
      },
    ],
    legend: { show: false },
    cursor: { show: false },
    select: { show: false },
  };

  try {
    container.innerHTML = ''; // Clear any no-data message before mounting
    uplotInstance = new uPlot(opts, alignedData, container);

    requestAnimationFrame(() => {
      if (!uplotInstance || typeof uplotInstance.setSize !== 'function') return;
      const nextWidth = Math.max(container.clientWidth || 0, 320);
      if (nextWidth !== chartWidth) {
        uplotInstance.setSize({ width: nextWidth, height: 240 });
      }
    });
  } catch (e) {
    console.error('uPlot init failed:', e);
    container.innerHTML = "<div class='muted'>Chart failed to load.</div>";
  }
}
