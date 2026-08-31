import { registerPlugin, type PluginListenerHandle } from '@capacitor/core';

export const NATIVE_SPEECH_LISTENING_EVENT = 'listening' as const;

export type NativeSpeechInputStatus =
  | { state: 'unsupported'; reason: 'os' | 'device' | 'provider' | 'policy' }
  | { state: 'ready' }
  | { state: 'temporarily-unavailable'; reason: 'busy' | 'foreground' | 'provider' };

export interface NativeSpeechInputPlugin {
  status(options: { operationId: string }): Promise<NativeSpeechInputStatus>;
  listen(options: {
    operationId: string;
    maxDurationMs: number;
    language?: string;
  }): Promise<{ transcript: string }>;
  cancel(options: { operationId: string }): Promise<void>;
  invalidate(): Promise<void>;
  addListener(
    eventName: typeof NATIVE_SPEECH_LISTENING_EVENT,
    listener: (event: { operationId: string }) => void,
  ): Promise<PluginListenerHandle>;
}

export const ScrumboySpeechInput =
  registerPlugin<NativeSpeechInputPlugin>('ScrumboySpeechInput');
