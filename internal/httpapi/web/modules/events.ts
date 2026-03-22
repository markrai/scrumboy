/**
 * Lightweight event bus for cross-module UI notifications.
 * No framework, no dependencies. Safe when events have no listeners.
 */
const listeners = new Map<string, Set<(payload?: unknown) => void>>();

export function on(event: string, handler: (payload?: unknown) => void): void {
  if (!listeners.has(event)) {
    listeners.set(event, new Set());
  }
  listeners.get(event)!.add(handler);
}

export function off(event: string, handler: (payload?: unknown) => void): void {
  listeners.get(event)?.delete(handler);
}

export function emit(event: string, payload?: unknown): void {
  listeners.get(event)?.forEach((h) => h(payload));
}
