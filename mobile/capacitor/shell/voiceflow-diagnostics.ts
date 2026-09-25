import { registerPlugin } from '@capacitor/core';

const diagnostics = registerPlugin<{ emit(options: { line: string }): Promise<void> }>('ScrumboyVoiceFlow');

// The application and shell are separate bundles; a DOM event keeps the existing
// diagnostic gate authoritative without adding a second logging configuration.
globalThis.addEventListener('scrumboy:voiceflow-trace', (event) => {
  const line = (event as CustomEvent<unknown>).detail;
  if (typeof line === 'string' && line.startsWith('VF ')) {
    void diagnostics.emit({ line }).catch(() => undefined);
  }
});
