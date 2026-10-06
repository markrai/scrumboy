import type {
  AcquiredServerResource,
  ServerEventStream,
  ServerRequestOptions,
  ServerResourceOptions,
  ServerResponse,
  ServerTransport,
} from './server-transport.js';

/** Mechanical encapsulation of Scrumboy's existing same-origin browser behavior. */
export class BrowserServerTransport implements ServerTransport {
  request(path: string, options: ServerRequestOptions = {}): Promise<ServerResponse> {
    return fetch(path, options as RequestInit);
  }

  openEventStream(path: string): ServerEventStream {
    const url = new URL(path, window.location.origin).toString();
    return new EventSource(url);
  }

  async acquireResource(path: string, options: ServerResourceOptions = {}): Promise<AcquiredServerResource> {
    if (options.signal?.aborted) {
      throw options.signal.reason ?? new DOMException('Aborted', 'AbortError');
    }
    return {
      url: path,
      release() {},
    };
  }

  async logout(): Promise<void> {
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = '/api/auth/logout';
    document.body.appendChild(form);
    form.submit();
  }
}
