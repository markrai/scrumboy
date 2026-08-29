import { describe, expect, expectTypeOf, it } from 'vitest';
import { createClientCapabilityRegistry } from './client-capabilities.js';
import type { AppCapabilityMap } from './client-capabilities.js';
import type { LocalTextGenerationCapability } from './local-text-generation.js';

interface EchoCapability {
  echo(value: string): string;
}

interface ClockCapability {
  now(): number;
}

interface TestCapabilityMap {
  'test.echo': EchoCapability;
  'test.clock': ClockCapability;
}

describe('client capability registry', () => {
  it('returns null when a typed capability is absent', () => {
    const registry = createClientCapabilityRegistry<TestCapabilityMap>();

    expect(registry.get('test.echo')).toBeNull();
    expectTypeOf(registry.get('test.echo')).toEqualTypeOf<EchoCapability | null>();
  });

  it('returns the stable adapter associated with each typed key', () => {
    const echo: EchoCapability = { echo: (value) => value };
    const clock: ClockCapability = { now: () => 42 };
    const registry = createClientCapabilityRegistry<TestCapabilityMap>({
      'test.echo': echo,
      'test.clock': clock,
    });

    expect(registry.get('test.echo')).toBe(echo);
    expect(registry.get('test.echo')).toBe(echo);
    expect(registry.get('test.clock')).toBe(clock);
  });

  it('snapshots entries instead of observing later source mutations', () => {
    const original: EchoCapability = { echo: (value) => value };
    const replacement: EchoCapability = { echo: (value) => value.toUpperCase() };
    const source: Partial<TestCapabilityMap> = { 'test.echo': original };
    const registry = createClientCapabilityRegistry<TestCapabilityMap>(source);

    source['test.echo'] = replacement;
    source['test.clock'] = { now: () => 99 };

    expect(registry.get('test.echo')).toBe(original);
    expect(registry.get('test.clock')).toBeNull();
  });

  it('normalizes nullish entries to absence and exposes no mutation API', () => {
    const source = { 'test.echo': null } as unknown as Partial<TestCapabilityMap>;
    const registry = createClientCapabilityRegistry<TestCapabilityMap>(source);

    expect(registry.get('test.echo')).toBeNull();
    expect(Object.isFrozen(registry)).toBe(true);
    expect(Object.keys(registry)).toEqual(['get']);
  });

  it('registers the one production capability without changing test-local maps', () => {
    const localTextGeneration = {} as LocalTextGenerationCapability;
    const registry = createClientCapabilityRegistry<AppCapabilityMap>({
      'local-text-generation': localTextGeneration,
    });

    expect(registry.get('local-text-generation')).toBe(localTextGeneration);
    expectTypeOf(registry.get('local-text-generation'))
      .toEqualTypeOf<LocalTextGenerationCapability | null>();
  });
});
