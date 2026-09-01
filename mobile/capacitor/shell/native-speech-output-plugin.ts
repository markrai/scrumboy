import { registerPlugin } from '@capacitor/core';

export type NativeSpeechOutputStatus =
  | { state: 'unsupported'; reason: 'os' | 'provider' | 'policy' | 'no-local-voice' }
  | { state: 'not-ready'; reason: 'initializing' }
  | { state: 'ready' }
  | { state: 'temporarily-unavailable'; reason: 'busy' | 'foreground' | 'provider' };

export interface NativeSpeechOutputPlugin {
  status(options: { operationId: string }): Promise<NativeSpeechOutputStatus>;
  speak(options: {
    operationId: string;
    text: string;
    language?: string;
  }): Promise<{ operationId: string }>;
  stop(options: { operationId?: string }): Promise<void>;
  invalidate(): Promise<void>;
}

export const ScrumboySpeechOutput =
  registerPlugin<NativeSpeechOutputPlugin>('ScrumboySpeechOutput');
