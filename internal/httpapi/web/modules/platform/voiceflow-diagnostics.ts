const VOICEFLOW_DIAGNOSTICS_KEY = 'scrumboy_debug_voiceflow';

function diagnosticsEnabled(): boolean {
  try {
    return typeof globalThis.localStorage !== 'undefined'
      && globalThis.localStorage.getItem(VOICEFLOW_DIAGNOSTICS_KEY) === '1';
  } catch {
    return false;
  }
}

export function voiceFlowDiagnostic(
  event: string,
  details: Readonly<Record<string, unknown>> = {},
): void {
  if (!diagnosticsEnabled()) return;
  console.debug(`VoiceFlow ${event}`, details);
}
