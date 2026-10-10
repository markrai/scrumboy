/** Mechanical encapsulation of Scrumboy's existing same-origin browser behavior. */
export class BrowserServerTransport {
    request(path, options = {}) {
        return fetch(path, options);
    }
    openEventStream(path) {
        const url = new URL(path, window.location.origin).toString();
        return new EventSource(url);
    }
    async acquireResource(path, options = {}) {
        if (options.signal?.aborted) {
            throw options.signal.reason ?? new DOMException('Aborted', 'AbortError');
        }
        return {
            url: path,
            release() { },
        };
    }
    async logout() {
        const form = document.createElement('form');
        form.method = 'POST';
        form.action = '/api/auth/logout';
        // Return to the same page after logout (a still-published board resolves
        // to public again); the server sanitizes this to a same-origin path.
        const returnTo = document.createElement('input');
        returnTo.type = 'hidden';
        returnTo.name = 'return_to';
        returnTo.value = window.location.pathname + window.location.search;
        form.appendChild(returnTo);
        document.body.appendChild(form);
        form.submit();
    }
}
