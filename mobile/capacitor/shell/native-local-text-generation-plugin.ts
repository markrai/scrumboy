import { registerPlugin } from '@capacitor/core';

export type NativeLocalTextGenerationStatus =
  | { state: 'unsupported'; reason: 'os' | 'device' | 'provider' | 'policy' }
  | { state: 'action-required'; action: 'download' | 'enable' | 'system-update' }
  | { state: 'preparing'; downloadedBytes?: number; totalBytes?: number }
  | {
      state: 'ready';
      maximumOutputTokens: number;
      contextTokenLimit?: number;
      providerModel?: string;
    }
  | {
      state: 'temporarily-unavailable';
      reason: 'initializing' | 'busy' | 'quota' | 'foreground' | 'storage' | 'provider';
      retryAfterMs?: number;
    };

export interface NativeLocalTextGenerationPlugin {
  status(options: { operationId: string }): Promise<NativeLocalTextGenerationStatus>;
  prepare(options: { operationId: string; userInitiated: boolean }): Promise<void>;
  generate(options: {
    operationId: string;
    requestId: string;
    input: string;
    instructions: string;
    maximumOutputTokens: number;
  }): Promise<{ requestId: string; text: string }>;
  cancel(options: { operationId: string }): Promise<void>;
  invalidate(): Promise<void>;
}

export const ScrumboyLocalTextGeneration =
  registerPlugin<NativeLocalTextGenerationPlugin>('ScrumboyLocalTextGeneration');
