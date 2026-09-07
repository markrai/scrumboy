import { registerPlugin, type PluginListenerHandle } from '@capacitor/core';

export const NATIVE_SPEECH_LISTENING_EVENT = 'listening' as const;
export const NATIVE_SPEECH_CAPABILITY_EVENT = 'asrCapability' as const;

export type NativeSpeechInputStatus =
  | { state: 'unsupported'; reason: 'os' | 'device' | 'provider' | 'policy' }
  | { state: 'ready' }
  | { state: 'temporarily-unavailable'; reason: 'busy' | 'foreground' | 'provider' };

export type NativeSpeechProviderId = 'mlkit_genai_advanced' | 'android_on_device';

export type NativeSpeechCapabilityEvent = Readonly<{
  cache: 'hit' | 'miss' | 'expired';
  advancedSupport: 'ready' | 'preparing' | 'unavailable' | 'unknown';
  statusSource: 'cached' | 'probed';
  locale: string;
  provider: NativeSpeechProviderId;
}>;

export type NativeSpeechListeningEvent = Readonly<{
  operationId: string;
  provider?: NativeSpeechProviderId;
}>;

export interface NativeSpeechInputPlugin {
  status(options: { operationId: string; language?: string }): Promise<NativeSpeechInputStatus>;
  listen(options: {
    operationId: string;
    maxDurationMs: number;
    language?: string;
  }): Promise<{ transcript: string }>;
  cancel(options: { operationId: string }): Promise<void>;
  invalidate(): Promise<void>;
  clearAdvancedCapabilityCache?(): Promise<void>;
  addListener(
    eventName: typeof NATIVE_SPEECH_LISTENING_EVENT,
    listener: (event: NativeSpeechListeningEvent) => void,
  ): Promise<PluginListenerHandle>;
  addListener(
    eventName: typeof NATIVE_SPEECH_CAPABILITY_EVENT,
    listener: (event: NativeSpeechCapabilityEvent) => void,
  ): Promise<PluginListenerHandle>;
}

export const ScrumboySpeechInput =
  registerPlugin<NativeSpeechInputPlugin>('ScrumboySpeechInput');
