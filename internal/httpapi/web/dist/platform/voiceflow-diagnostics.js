const VOICEFLOW_DIAGNOSTICS_KEY = 'scrumboy_debug_voiceflow';
function diagnosticsEnabled() {
    try {
        return typeof globalThis.localStorage !== 'undefined'
            && globalThis.localStorage.getItem(VOICEFLOW_DIAGNOSTICS_KEY) === '1';
    }
    catch {
        return false;
    }
}
export function voiceFlowDiagnostic(event, details = {}) {
    if (!diagnosticsEnabled())
        return;
    // A serialized trace survives WebView console forwarding without [object Object].
    // The event also crosses the separately bundled native shell boundary.
    try {
        console.debug(`VoiceFlow ${event}`, details);
        if (event === 'trace' && typeof globalThis.dispatchEvent === 'function') {
            globalThis.dispatchEvent(new CustomEvent('scrumboy:voiceflow-trace', {
                detail: `VF ${JSON.stringify(details)}`,
            }));
        }
    }
    catch {
        // Diagnostics must never interrupt a command, even if a sink is unavailable.
    }
}
