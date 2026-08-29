import type { LocalTextGenerationCapability } from './local-text-generation.js';

/** Operation-oriented product capability vocabulary. */
export interface AppCapabilityMap {
  'local-text-generation': LocalTextGenerationCapability;
}

export type CapabilityId<M extends object> = Extract<keyof M, string>;

export interface ClientCapabilityRegistry<M extends object = AppCapabilityMap> {
  get<K extends CapabilityId<M>>(name: K): M[K] | null;
}

export type AppCapabilityRegistry = ClientCapabilityRegistry<AppCapabilityMap>;

export function createClientCapabilityRegistry<M extends object>(
  entries?: Readonly<Partial<M>>,
): ClientCapabilityRegistry<M> {
  type Id = CapabilityId<M>;
  type Capability = M[Id];
  const snapshot = new Map<Id, Capability>();

  for (const [name, capability] of Object.entries(entries ?? {}) as Array<[
    Id,
    Capability | null | undefined,
  ]>) {
    if (capability != null) snapshot.set(name, capability);
  }

  return Object.freeze({
    get<K extends Id>(name: K): M[K] | null {
      return (snapshot.get(name) as M[K] | undefined) ?? null;
    },
  });
}

export const emptyClientCapabilityRegistry: AppCapabilityRegistry =
  createClientCapabilityRegistry<AppCapabilityMap>();
