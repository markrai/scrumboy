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
    console.debug(`VoiceFlow ${event}`, details);
}
