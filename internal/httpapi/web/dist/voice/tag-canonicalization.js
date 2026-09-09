/**
 * Mirrors store.CanonicalizeTag for client-side contract validation only.
 * This is deliberately distinct from spoken-reference identity: callers still
 * send the authoritative board label and let the server own persistence.
 */
export function canonicalizeTagName(input) {
    const canonical = String(input ?? "")
        .trim()
        .toLowerCase()
        .replace(/\s+/g, "-")
        .replace(/-+/g, "-")
        .replace(/^-+|-+$/g, "");
    return /^[a-z0-9][a-z0-9-]{0,31}$/.test(canonical) ? canonical : null;
}
