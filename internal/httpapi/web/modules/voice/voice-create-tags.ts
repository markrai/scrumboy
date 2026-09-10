import { apiFetch } from '../api.js';
import { canonicalizeTagName } from './tag-canonicalization.js';
import { VoiceCreatePlanError } from './voice-create-plan.js';

/** The only tag data Create v2 needs: an authoritative, addressable label. */
export type VoiceCreateTag = Readonly<{ name: string }>;

export type VoiceCreateTagsReader = (
  projectSlug: string,
  signal: AbortSignal,
) => Promise<readonly VoiceCreateTag[]>;

type VoiceCreateTagsFetch = <T = unknown>(path: string, options?: RequestInit) => Promise<T>;

function isTagWire(value: unknown): value is Readonly<{ name: string }> {
  return !!value
    && typeof value === 'object'
    && !Array.isArray(value)
    && typeof (value as { name?: unknown }).name === 'string'
    && (value as { name: string }).name.trim().length > 0;
}

function tagGroupKey(name: string): string {
  return canonicalizeTagName(name) ?? name;
}

/**
 * Mirrors the store's TagGroupKey union semantics. Project labels win only as
 * the representation for a duplicate logical label; identity remains its name.
 */
export function combineVoiceCreateTags(
  projectTags: readonly unknown[],
  personalTags: readonly unknown[],
): readonly VoiceCreateTag[] {
  if (!projectTags.every(isTagWire) || !personalTags.every(isTagWire)) {
    throw new VoiceCreatePlanError('network');
  }
  const byKey = new Map<string, VoiceCreateTag>();
  for (const tag of [...projectTags, ...personalTags]) {
    const entry = Object.freeze({ name: tag.name });
    if (!byKey.has(tagGroupKey(entry.name))) byKey.set(tagGroupKey(entry.name), entry);
  }
  return Object.freeze([...byKey.values()].sort((a, b) => a.name === b.name ? 0 : a.name < b.name ? -1 : 1));
}

/** Shared production/device adapter for project labels plus the caller's personal library. */
export async function readVoiceCreateTags(
  projectSlug: string,
  signal: AbortSignal,
  fetcher: VoiceCreateTagsFetch = apiFetch,
): Promise<readonly VoiceCreateTag[]> {
  const [projectTags, personalTags] = await Promise.all([
    fetcher<unknown>(`/api/board/${encodeURIComponent(projectSlug)}/tags`, { signal }),
    fetcher<unknown>('/api/tags/mine', { signal }),
  ]);
  if (!Array.isArray(projectTags) || !Array.isArray(personalTags)) {
    throw new VoiceCreatePlanError('network');
  }
  return combineVoiceCreateTags(projectTags, personalTags);
}
