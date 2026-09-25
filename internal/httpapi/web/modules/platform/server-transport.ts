/**
 * Protocol mechanics for communication with the authoritative Scrumboy server.
 * Packaged/static asset loads deliberately do not use this contract.
 */

export type ServerRequestOptions = {
  method?: string;
  headers?: HeadersInit;
  body?: BodyInit | null;
  signal?: AbortSignal | null;
  credentials?: RequestCredentials;
  cache?: RequestCache;
};

export interface ServerResponse {
  readonly ok: boolean;
  readonly status: number;
  json(): Promise<any>;
  blob(): Promise<Blob>;
}

export interface ServerEventStream {
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent) => void) | null;
  onerror: ((event: Event) => void) | null;
  close(): void;
}

export interface AcquiredServerResource {
  readonly url: string;

  /**
   * Releases any resources owned by this acquisition.
   *
   * Must be safe to call after the resource is no longer displayed
   * and must be idempotent.
   */
  release(): void;
}

export interface ServerResourceOptions {
  signal?: AbortSignal;
}

export interface ServerTransport {
  request(path: string, options?: ServerRequestOptions): Promise<ServerResponse>;
  openEventStream(path: string): ServerEventStream;
  acquireResource(path: string, options?: ServerResourceOptions): Promise<AcquiredServerResource>;
  logout(): Promise<void>;
}
