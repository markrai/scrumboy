export function createClientCapabilityRegistry(entries) {
    const snapshot = new Map();
    for (const [name, capability] of Object.entries(entries ?? {})) {
        if (capability != null)
            snapshot.set(name, capability);
    }
    return Object.freeze({
        get(name) {
            return snapshot.get(name) ?? null;
        },
    });
}
export const emptyClientCapabilityRegistry = createClientCapabilityRegistry();
