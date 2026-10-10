/**
 * Client for the isolated read-only public board API (/api/public/board/{slug}).
 *
 * Wire DTOs mirror internal/httpapi/public_board_json.go exactly. Responses are
 * validated and normalized into public UI types; they are never converted into
 * the authenticated Board/Project/Todo models, and no numeric project, todo,
 * sprint, tag, or user identity is fabricated. This module calls only public
 * endpoints through the platform server transport.
 */

import { getAppRuntime } from './platform/runtime.js';
import { sanitizeHexColor } from './utils.js';

// ---- Wire DTOs (server contract) ----

type PublicTagWire = { name: string; color: string; activeCount?: number };
type PublicTodoWire = {
  localId: number;
  title: string;
  body: string;
  columnKey: string;
  estimationPoints?: number;
  priorityKey?: string;
  sprintNumber?: number;
  tags: PublicTagWire[];
};
type PublicLaneMetaWire = { hasMore: boolean; nextCursor: string | null; totalCount: number };

// ---- Normalized public UI types ----

export type PublicTag = { readonly name: string; readonly color: string | null };
export type PublicTagSummary = PublicTag & { readonly activeCount: number };

export type PublicTodo = {
  readonly localId: number;
  readonly title: string;
  readonly body: string;
  readonly columnKey: string;
  readonly estimationPoints: number | null;
  readonly priorityKey: string | null;
  readonly sprintNumber: number | null;
  readonly tags: readonly PublicTag[];
};

export type PublicWorkflowColumn = {
  readonly key: string;
  readonly name: string;
  readonly color: string | null;
  readonly isDone: boolean;
  readonly position: number;
};

export type PublicPriority = {
  readonly key: string;
  readonly name: string;
  readonly color: string | null;
  readonly position: number;
};

export type PublicLaneMeta = { readonly hasMore: boolean; readonly nextCursor: string | null; readonly totalCount: number };

export type PublicProject = {
  readonly slug: string;
  readonly name: string;
  readonly dominantColor: string | null;
  readonly estimationMode: string;
  readonly sprintsEnabled: boolean;
};

export type PublicBoardSnapshot = {
  readonly project: PublicProject;
  readonly workflow: readonly PublicWorkflowColumn[];
  readonly priorities: readonly PublicPriority[];
  readonly tags: readonly PublicTagSummary[];
  readonly columns: Readonly<Record<string, readonly PublicTodo[]>>;
  readonly columnsMeta: Readonly<Record<string, PublicLaneMeta>>;
};

export type PublicLanePage = PublicLaneMeta & { readonly items: readonly PublicTodo[] };

export type PublicTodoLink = { readonly direction: 'outbound' | 'inbound'; readonly localId: number; readonly title: string };

export type PublicSprint = { readonly number: number; readonly name: string; readonly state: string };

/** Filters the public API supports. Sorting is manual-only and never sent. */
export type PublicBoardQuery = {
  readonly search: string;
  readonly tags: readonly string[];
  readonly sprintNumber: number | null;
  readonly priority: string | null;
};

export const EMPTY_PUBLIC_QUERY: PublicBoardQuery = Object.freeze({ search: '', tags: [], sprintNumber: null, priority: null });

/** Server defaults and ceilings (Phase 2 contract). */
export const PUBLIC_DEFAULT_PAGE_SIZE = 20;
export const PUBLIC_MAX_PAGE_SIZE = 50;
export const PUBLIC_MAX_TAG_FILTERS = 10;
export const PUBLIC_MAX_SEARCH_CODE_POINTS = 200;

// ---- Errors ----

export type PublicBoardErrorKind =
  | 'not_found'
  | 'rate_limited'
  | 'invalid_request'
  | 'unavailable'
  | 'malformed'
  | 'network'
  | 'aborted';

export class PublicBoardApiError extends Error {
  readonly kind: PublicBoardErrorKind;
  readonly status: number | null;

  constructor(kind: PublicBoardErrorKind, status: number | null) {
    super(`public board request failed: ${kind}${status != null ? ` (${status})` : ''}`);
    this.name = 'PublicBoardApiError';
    this.kind = kind;
    this.status = status;
  }
}

export function isPublicNotFound(err: unknown): boolean {
  return err instanceof PublicBoardApiError && err.kind === 'not_found';
}

export function isPublicAbort(err: unknown): boolean {
  return err instanceof PublicBoardApiError && err.kind === 'aborted';
}

function kindForStatus(status: number): PublicBoardErrorKind {
  if (status === 404) return 'not_found';
  if (status === 429) return 'rate_limited';
  if (status === 400) return 'invalid_request';
  return 'unavailable';
}

// ---- Validation helpers ----

const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$/;

export function isPublicSlug(slug: string): boolean {
  return SLUG_RE.test(slug) && !slug.includes('--');
}

function malformed(): never {
  throw new PublicBoardApiError('malformed', null);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function str(value: unknown): string {
  if (typeof value !== 'string') malformed();
  return value as string;
}

function int(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value)) malformed();
  return value as number;
}

function positiveInt(value: unknown): number {
  const n = int(value);
  if (n < 1) malformed();
  return n;
}

function optionalInt(value: unknown): number | null {
  return value === undefined || value === null ? null : int(value);
}

function optionalString(value: unknown): string | null {
  return value === undefined || value === null ? null : str(value);
}

function bool(value: unknown): boolean {
  if (typeof value !== 'boolean') malformed();
  return value as boolean;
}

function array(value: unknown): unknown[] {
  if (!Array.isArray(value)) malformed();
  return value as unknown[];
}

function color(value: unknown): string | null {
  return sanitizeHexColor(typeof value === 'string' ? value : undefined);
}

function normalizeTag(value: unknown): PublicTag {
  if (!isRecord(value)) malformed();
  const wire = value as PublicTagWire;
  return { name: str(wire.name), color: color(wire.color) };
}

function normalizeTodo(value: unknown): PublicTodo {
  if (!isRecord(value)) malformed();
  const wire = value as PublicTodoWire;
  return {
    localId: positiveInt(wire.localId),
    title: str(wire.title),
    body: str(wire.body),
    columnKey: str(wire.columnKey),
    estimationPoints: optionalInt(wire.estimationPoints),
    priorityKey: optionalString(wire.priorityKey),
    sprintNumber: optionalInt(wire.sprintNumber),
    tags: array(wire.tags).map(normalizeTag),
  };
}

function normalizeLaneMeta(value: unknown): PublicLaneMeta {
  if (!isRecord(value)) malformed();
  const wire = value as PublicLaneMetaWire;
  const nextCursor = wire.nextCursor === undefined || wire.nextCursor === null ? null : str(wire.nextCursor);
  const hasMore = bool(wire.hasMore);
  const totalCount = int(wire.totalCount);
  if (totalCount < 0) malformed();
  return { hasMore: hasMore && nextCursor !== null, nextCursor: hasMore ? nextCursor : null, totalCount };
}

function byPosition<T extends { position: number }>(items: T[]): T[] {
  return items.slice().sort((a, b) => a.position - b.position);
}

/** Validates a snapshot and binds it to the slug that was requested. */
export function normalizePublicBoardSnapshot(value: unknown, requestedSlug: string): PublicBoardSnapshot {
  if (!isRecord(value)) malformed();
  const access = value.access;
  if (!isRecord(access) || access.kind !== 'public' || access.readOnly !== true) malformed();
  const projectWire = value.project;
  if (!isRecord(projectWire)) malformed();
  const project: PublicProject = {
    slug: str(projectWire.slug),
    name: str(projectWire.name),
    dominantColor: color(projectWire.dominantColor),
    estimationMode: str(projectWire.estimationMode),
    sprintsEnabled: bool(projectWire.sprintsEnabled),
  };
  if (project.slug !== requestedSlug) malformed();

  const workflow = byPosition(array(value.workflow).map((raw) => {
    if (!isRecord(raw)) malformed();
    return { key: str(raw.key), name: str(raw.name), color: color(raw.color), isDone: bool(raw.isDone), position: int(raw.position) };
  }));
  const priorities = byPosition(array(value.priorities).map((raw) => {
    if (!isRecord(raw)) malformed();
    return { key: str(raw.key), name: str(raw.name), color: color(raw.color), position: int(raw.position) };
  }));
  const tags = array(value.tags).map((raw) => {
    if (!isRecord(raw)) malformed();
    const activeCount = raw.activeCount === undefined ? 0 : int(raw.activeCount);
    return { name: str(raw.name), color: color(raw.color), activeCount };
  });

  const columnsWire = value.columns;
  const metaWire = value.columnsMeta;
  if (!isRecord(columnsWire) || !isRecord(metaWire)) malformed();
  const columns: Record<string, PublicTodo[]> = {};
  const columnsMeta: Record<string, PublicLaneMeta> = {};
  // Only lanes that exist in the public workflow are rendered.
  for (const lane of workflow) {
    columns[lane.key] = columnsWire[lane.key] === undefined ? [] : array(columnsWire[lane.key]).map(normalizeTodo);
    columnsMeta[lane.key] = metaWire[lane.key] === undefined
      ? { hasMore: false, nextCursor: null, totalCount: columns[lane.key].length }
      : normalizeLaneMeta(metaWire[lane.key]);
  }
  return { project, workflow, priorities, tags, columns, columnsMeta };
}

export function normalizePublicLanePage(value: unknown): PublicLanePage {
  if (!isRecord(value)) malformed();
  const meta = normalizeLaneMeta(value);
  return { ...meta, items: array(value.items).map(normalizeTodo) };
}

export function normalizePublicTodoDetail(value: unknown, requestedLocalId: number): PublicTodo {
  if (!isRecord(value)) malformed();
  const todo = normalizeTodo(value.todo);
  if (todo.localId !== requestedLocalId) malformed();
  return todo;
}

export function normalizePublicTodoLinks(value: unknown): PublicTodoLink[] {
  if (!isRecord(value)) malformed();
  return array(value.links).map((raw) => {
    if (!isRecord(raw)) malformed();
    const direction = raw.direction;
    if (direction !== 'outbound' && direction !== 'inbound') malformed();
    return { direction: direction as 'outbound' | 'inbound', localId: positiveInt(raw.localId), title: str(raw.title) };
  });
}

export function normalizePublicSprints(value: unknown): PublicSprint[] {
  if (!isRecord(value)) malformed();
  return array(value.sprints).map((raw) => {
    if (!isRecord(raw)) malformed();
    return { number: positiveInt(raw.number), name: str(raw.name), state: str(raw.state) };
  });
}

// ---- Query construction ----

function clampPageSize(value: number | undefined): number {
  const n = Number.isFinite(value) ? Math.trunc(value as number) : PUBLIC_DEFAULT_PAGE_SIZE;
  return Math.min(PUBLIC_MAX_PAGE_SIZE, Math.max(1, n));
}

function truncateCodePoints(value: string, max: number): string {
  const points = Array.from(value);
  return points.length > max ? points.slice(0, max).join('') : value;
}

/** Normalizes user/URL input into a query the public API accepts. */
export function normalizePublicQuery(input: Partial<PublicBoardQuery>): PublicBoardQuery {
  const search = truncateCodePoints((input.search ?? '').trim(), PUBLIC_MAX_SEARCH_CODE_POINTS);
  const seen = new Set<string>();
  const tags: string[] = [];
  for (const raw of input.tags ?? []) {
    const tag = raw.trim();
    const key = tag.toLocaleLowerCase();
    if (!tag || seen.has(key)) continue;
    seen.add(key);
    tags.push(tag);
    if (tags.length >= PUBLIC_MAX_TAG_FILTERS) break;
  }
  const sprintNumber = input.sprintNumber != null && Number.isSafeInteger(input.sprintNumber) && input.sprintNumber > 0
    ? input.sprintNumber
    : null;
  const priority = input.priority && input.priority.trim() ? input.priority.trim() : null;
  return { search, tags, sprintNumber, priority };
}

export function samePublicQuery(a: PublicBoardQuery, b: PublicBoardQuery): boolean {
  return a.search === b.search
    && a.sprintNumber === b.sprintNumber
    && a.priority === b.priority
    && a.tags.length === b.tags.length
    && a.tags.every((tag, index) => tag === b.tags[index]);
}

function appendQuery(params: URLSearchParams, query: PublicBoardQuery): void {
  if (query.search) params.set('search', query.search);
  for (const tag of query.tags) params.append('tag', tag);
  if (query.sprintNumber != null) params.set('sprintNumber', String(query.sprintNumber));
  if (query.priority) params.set('priority', query.priority);
}

function boardPath(slug: string): string {
  if (!isPublicSlug(slug)) throw new PublicBoardApiError('not_found', null);
  return `/api/public/board/${encodeURIComponent(slug)}`;
}

export function publicBoardEventsPath(slug: string): string {
  return `${boardPath(slug)}/events`;
}

// ---- Requests ----

type RequestOpts = { signal?: AbortSignal };

async function getPublicJSON(path: string, opts: RequestOpts): Promise<unknown> {
  let response;
  try {
    response = await getAppRuntime().transport().request(path, {
      method: 'GET',
      headers: { Accept: 'application/json' },
      cache: 'no-store',
      signal: opts.signal ?? null,
    });
  } catch (err) {
    if (opts.signal?.aborted || (err as { name?: string } | null)?.name === 'AbortError') {
      throw new PublicBoardApiError('aborted', null);
    }
    throw new PublicBoardApiError('network', null);
  }
  if (!response.ok) {
    throw new PublicBoardApiError(kindForStatus(response.status), response.status);
  }
  try {
    return await response.json();
  } catch (err) {
    if (opts.signal?.aborted) throw new PublicBoardApiError('aborted', null);
    throw new PublicBoardApiError('malformed', response.status);
  }
}

export async function fetchPublicBoardSnapshot(
  slug: string,
  query: PublicBoardQuery,
  opts: RequestOpts & { limitPerLane?: number } = {},
): Promise<PublicBoardSnapshot> {
  const params = new URLSearchParams();
  params.set('limitPerLane', String(clampPageSize(opts.limitPerLane)));
  appendQuery(params, normalizePublicQuery(query));
  const data = await getPublicJSON(`${boardPath(slug)}?${params.toString()}`, opts);
  return normalizePublicBoardSnapshot(data, slug);
}

export async function fetchPublicLanePage(
  slug: string,
  columnKey: string,
  query: PublicBoardQuery,
  afterCursor: string,
  opts: RequestOpts & { limit?: number } = {},
): Promise<PublicLanePage> {
  if (!columnKey || !afterCursor) throw new PublicBoardApiError('invalid_request', null);
  const params = new URLSearchParams();
  params.set('limit', String(clampPageSize(opts.limit)));
  // The cursor is opaque: it is sent exactly as the server returned it.
  params.set('afterCursor', afterCursor);
  appendQuery(params, normalizePublicQuery(query));
  const data = await getPublicJSON(`${boardPath(slug)}/lanes/${encodeURIComponent(columnKey)}?${params.toString()}`, opts);
  return normalizePublicLanePage(data);
}

function todoPath(slug: string, localId: number): string {
  if (!Number.isSafeInteger(localId) || localId < 1) throw new PublicBoardApiError('not_found', null);
  return `${boardPath(slug)}/todos/${localId}`;
}

export async function fetchPublicTodo(slug: string, localId: number, opts: RequestOpts = {}): Promise<PublicTodo> {
  const data = await getPublicJSON(todoPath(slug, localId), opts);
  return normalizePublicTodoDetail(data, localId);
}

export async function fetchPublicTodoLinks(slug: string, localId: number, opts: RequestOpts = {}): Promise<PublicTodoLink[]> {
  const data = await getPublicJSON(`${todoPath(slug, localId)}/links`, opts);
  return normalizePublicTodoLinks(data);
}

export async function fetchPublicSprints(slug: string, opts: RequestOpts = {}): Promise<PublicSprint[]> {
  const data = await getPublicJSON(`${boardPath(slug)}/sprints`, opts);
  return normalizePublicSprints(data);
}
