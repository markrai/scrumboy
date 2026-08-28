import { Capacitor } from '@capacitor/core';
import type {
  AcquiredServerResource,
  ServerEventStream,
  ServerRequestOptions,
  ServerResourceOptions,
  ServerResponse,
  ServerTransport,
} from '../../../internal/httpapi/web/modules/platform/server-transport.js';
import {
  ScrumboyTransport,
  TRANSPORT_EVENT,
  type NativeMultipartField,
  type NativeRequestOptions,
  type NativeResponse,
  type NativeTransportEvent,
  type ScrumboyTransportPlugin,
} from './native-plugin.js';
import { clearScrumboyWebState } from './native-runtime.js';

const FORBIDDEN_HEADERS = new Set(['cookie', 'host', 'set-cookie']);

function requestId(): string {
  return globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function abortError(signal?: AbortSignal | null): unknown {
  return signal?.reason ?? new DOMException('Aborted', 'AbortError');
}

export function validateRootRelativePath(path: string): void {
  if (!path.startsWith('/') || path.startsWith('//') || path.startsWith('\\') || path.includes('\\')) {
    throw new TypeError('Scrumboy server paths must be root-relative');
  }
  if (path.includes('#')) throw new TypeError('Scrumboy server paths must not contain fragments');
}

function encodeHeaders(input?: HeadersInit): Record<string, string> {
  const output: Record<string, string> = {};
  const headers = new Headers(input);
  headers.forEach((value, name) => {
    if (FORBIDDEN_HEADERS.has(name.toLowerCase())) {
      throw new TypeError(`Forbidden native request header: ${name}`);
    }
    output[name] = value;
  });
  return output;
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  const chunk = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunk) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk));
  }
  return btoa(binary);
}

function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

async function encodeBody(body: BodyInit | null | undefined, signal?: AbortSignal | null): Promise<NativeRequestOptions['body']> {
  if (body == null) return undefined;
  if (signal?.aborted) throw abortError(signal);
  if (typeof body === 'string') return { kind: 'text', data: body };
  if (body instanceof FormData) {
    const fields: NativeMultipartField[] = [];
    for (const [name, value] of body.entries()) {
      if (signal?.aborted) throw abortError(signal);
      if (typeof value === 'string') {
        fields.push({ kind: 'text', name, value });
      } else {
        const bytes = new Uint8Array(await value.arrayBuffer());
        if (signal?.aborted) throw abortError(signal);
        fields.push({
          kind: 'file',
          name,
          filename: value.name || 'upload',
          contentType: value.type || 'application/octet-stream',
          dataBase64: bytesToBase64(bytes),
        });
      }
    }
    return { kind: 'multipart', fields };
  }
  throw new TypeError('Unsupported Scrumboy native request body');
}

class NativeServerResponse implements ServerResponse {
  readonly ok: boolean;
  readonly status: number;
  readonly #bytes: Uint8Array;

  constructor(response: NativeResponse) {
    this.status = response.status;
    this.ok = response.status >= 200 && response.status < 300;
    this.#bytes = base64ToBytes(response.bodyBase64 || '');
  }

  async json(): Promise<unknown> {
    return JSON.parse(new TextDecoder().decode(this.#bytes));
  }

  async blob(): Promise<Blob> {
    return new Blob([this.#bytes.slice().buffer as ArrayBuffer]);
  }
}

type StreamCallbacks = {
  stream: ServerEventStream;
  closed: boolean;
};

export class NativeServerTransport implements ServerTransport {
  readonly #plugin: ScrumboyTransportPlugin;
  readonly #streams = new Map<string, StreamCallbacks>();
  #listenerReady: Promise<void> | null = null;
  readonly #onLogout: () => void;
  readonly #convertFileSrc: (uri: string) => string;

  constructor(options: {
    plugin?: ScrumboyTransportPlugin;
    onLogout?: () => void;
    convertFileSrc?: (uri: string) => string;
  } = {}) {
    this.#plugin = options.plugin || ScrumboyTransport;
    this.#convertFileSrc = options.convertFileSrc || ((uri) => Capacitor.convertFileSrc(uri));
    this.#onLogout = options.onLogout || (() => {
      clearScrumboyWebState();
      globalThis.location?.reload();
    });
  }

  async request(path: string, options: ServerRequestOptions = {}): Promise<ServerResponse> {
    validateRootRelativePath(path);
    if (options.signal?.aborted) throw abortError(options.signal);
    const id = requestId();
    const nativeOptions: NativeRequestOptions = {
      requestId: id,
      path,
      method: options.method,
      headers: encodeHeaders(options.headers),
      body: await encodeBody(options.body, options.signal),
    };
    if (options.signal?.aborted) throw abortError(options.signal);
    let aborted = false;
    const onAbort = () => {
      aborted = true;
      void this.#plugin.cancelRequest({ requestId: id }).catch(() => undefined);
    };
    options.signal?.addEventListener('abort', onAbort, { once: true });
    try {
      const response = await this.#plugin.request(nativeOptions);
      if (aborted || options.signal?.aborted) throw abortError(options.signal);
      return new NativeServerResponse(response);
    } catch (error) {
      if (aborted || options.signal?.aborted) throw abortError(options.signal);
      throw error;
    } finally {
      options.signal?.removeEventListener('abort', onAbort);
    }
  }

  openEventStream(path: string): ServerEventStream {
    validateRootRelativePath(path);
    const id = requestId();
    const stream: ServerEventStream = {
      onopen: null,
      onmessage: null,
      onerror: null,
      close: () => {
        const entry = this.#streams.get(id);
        if (!entry || entry.closed) return;
        entry.closed = true;
        this.#streams.delete(id);
        void this.#plugin.closeEventStream({ streamId: id }).catch(() => undefined);
      },
    };
    this.#streams.set(id, { stream, closed: false });
    void this.#ensureListener().then(async () => {
      if (!this.#streams.has(id)) return;
      try {
        await this.#plugin.openEventStream({ streamId: id, path });
      } catch (error) {
        const entry = this.#streams.get(id);
        if (entry && !entry.closed) entry.stream.onerror?.(Object.assign(new Event('error'), { error }));
      }
    });
    return stream;
  }

  async acquireResource(path: string, options: ServerResourceOptions = {}): Promise<AcquiredServerResource> {
    validateRootRelativePath(path);
    if (options.signal?.aborted) throw abortError(options.signal);
    const id = requestId();
    let aborted = false;
    const onAbort = () => {
      aborted = true;
      void this.#plugin.cancelRequest({ requestId: id }).catch(() => undefined);
    };
    options.signal?.addEventListener('abort', onAbort, { once: true });
    try {
      const result = await this.#plugin.acquireResource({ requestId: id, path });
      if (aborted || options.signal?.aborted) {
        await this.#plugin.releaseResource({ handle: result.handle }).catch(() => undefined);
        throw abortError(options.signal);
      }
      let released = false;
      return {
        url: this.#convertFileSrc(result.fileUri),
        release: () => {
          if (released) return;
          released = true;
          void this.#plugin.releaseResource({ handle: result.handle }).catch(() => undefined);
        },
      };
    } catch (error) {
      if (aborted || options.signal?.aborted) throw abortError(options.signal);
      throw error;
    } finally {
      options.signal?.removeEventListener('abort', onAbort);
    }
  }

  async logout(): Promise<void> {
    for (const entry of [...this.#streams.values()]) entry.stream.close();
    try {
      await this.#plugin.logout();
    } finally {
      this.#onLogout();
    }
  }

  async #ensureListener(): Promise<void> {
    if (!this.#listenerReady) {
      this.#listenerReady = this.#plugin.addListener(TRANSPORT_EVENT, (event) => this.#routeEvent(event)).then(() => undefined);
    }
    return this.#listenerReady;
  }

  #routeEvent(event: NativeTransportEvent): void {
    const entry = this.#streams.get(event.streamId);
    if (!entry || entry.closed) return;
    if (event.kind === 'open') entry.stream.onopen?.(new Event('open'));
    if (event.kind === 'message') entry.stream.onmessage?.(new MessageEvent('message', { data: event.data }));
    if (event.kind === 'error') entry.stream.onerror?.(Object.assign(new Event('error'), { code: event.code }));
  }
}
