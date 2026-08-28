import { registerPlugin, type PluginListenerHandle } from '@capacitor/core';

export const TRANSPORT_EVENT = 'scrumboyTransportEvent';

export type ProbeErrorCode =
  | 'invalid_url'
  | 'https_required'
  | 'dns_failure'
  | 'connect_failure'
  | 'timeout'
  | 'tls_failure'
  | 'cross_origin_redirect'
  | 'server_error'
  | 'incompatible_server'
  | 'cancelled';

export type NativeTextBody = { kind: 'text'; data: string };
export type NativeMultipartField =
  | { kind: 'text'; name: string; value: string }
  | { kind: 'file'; name: string; filename: string; contentType: string; dataBase64: string };
export type NativeMultipartBody = { kind: 'multipart'; fields: NativeMultipartField[] };

export interface NativeRequestOptions {
  requestId: string;
  path: string;
  method?: string;
  headers?: Record<string, string>;
  body?: NativeTextBody | NativeMultipartBody;
}

export interface NativeResponse {
  status: number;
  bodyBase64: string;
}

export interface ProbeServerResult {
  normalizedOrigin: string;
  version: string;
  authStatus: Record<string, unknown>;
}

export interface NativeResourceResult {
  handle: string;
  fileUri: string;
}

export type NativeTransportEvent =
  | { streamId: string; kind: 'open' }
  | { streamId: string; kind: 'message'; data: string }
  | { streamId: string; kind: 'error'; code: string };

export interface ScrumboyTransportPlugin {
  probeServer(options: { origin: string }): Promise<ProbeServerResult>;
  configure(options: { origin: string; resetSession?: boolean }): Promise<void>;
  request(options: NativeRequestOptions): Promise<NativeResponse>;
  cancelRequest(options: { requestId: string }): Promise<void>;
  openEventStream(options: { streamId: string; path: string }): Promise<void>;
  closeEventStream(options: { streamId: string }): Promise<void>;
  acquireResource(options: { requestId: string; path: string }): Promise<NativeResourceResult>;
  releaseResource(options: { handle: string }): Promise<void>;
  logout(): Promise<void>;
  resetForServerChange(): Promise<void>;
  addListener(
    eventName: typeof TRANSPORT_EVENT,
    listener: (event: NativeTransportEvent) => void,
  ): Promise<PluginListenerHandle>;
}

export const ScrumboyTransport = registerPlugin<ScrumboyTransportPlugin>('ScrumboyTransport');
