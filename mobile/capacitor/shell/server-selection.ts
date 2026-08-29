export type SelectorMode =
  | { kind: 'entry'; initialOrigin?: string; message?: string }
  | { kind: 'saved-unreachable'; origin: string; message: string };

export interface SelectorActions {
  connect(origin: string): Promise<void>;
  retry?(): Promise<void>;
  change?(): void;
}

function element<K extends keyof HTMLElementTagNameMap>(tag: K, text?: string): HTMLElementTagNameMap[K] {
  const value = document.createElement(tag);
  if (text !== undefined) value.textContent = text;
  return value;
}

function errorText(error: unknown): string {
  const code = typeof error === 'object' && error && 'code' in error
    ? String((error as { code: unknown }).code)
    : '';
  const messages: Record<string, string> = {
    invalid_url: 'Enter a valid server origin without a path, query, or fragment.',
    https_required: 'HTTPS is required in release builds.',
    dns_failure: 'The server name could not be resolved.',
    connect_failure: 'Could not connect to the server.',
    timeout: 'The server did not respond in time.',
    tls_failure: 'The server certificate could not be verified.',
    cross_origin_redirect: 'The server redirected to a different origin.',
    server_error: 'The server returned an error during compatibility checks.',
    incompatible_server: 'This does not appear to be a compatible Scrumboy server.',
    cancelled: 'Connection was cancelled.',
  };
  return messages[code] || 'Could not connect to this Scrumboy server.';
}

function productMount(): HTMLElement {
  const host = document.getElementById('app');
  if (!host) throw new Error('Packaged Scrumboy application root is missing');
  return host;
}

export function renderServerSelector(mode: SelectorMode, actions: SelectorActions): void {
  const shell = element('main');
  shell.id = 'scrumboy-mobile-bootstrap';
  shell.setAttribute('aria-labelledby', 'scrumboy-mobile-bootstrap-title');
  const title = element('h1', 'Scrumboy');
  title.id = 'scrumboy-mobile-bootstrap-title';
  shell.append(title);

  const status = element('p');
  status.id = 'scrumboy-mobile-server-status';
  status.setAttribute('role', 'status');

  if (mode.kind === 'saved-unreachable') {
    shell.append(element('p', mode.origin), element('p', mode.message));
    const retry = element('button', 'Retry');
    retry.type = 'button';
    const change = element('button', 'Change server');
    change.type = 'button';
    retry.addEventListener('click', () => void actions.retry?.());
    change.addEventListener('click', () => actions.change?.());
    shell.append(retry, change, status);
    productMount().replaceChildren(shell);
    return;
  }

  const form = element('form');
  const label = element('label', 'Server');
  label.htmlFor = 'scrumboy-mobile-server-origin';
  const input = element('input');
  input.id = 'scrumboy-mobile-server-origin';
  input.name = 'origin';
  input.type = 'url';
  input.inputMode = 'url';
  input.setAttribute('autocomplete', 'url');
  input.required = true;
  input.placeholder = 'https://scrumboy.example';
  input.value = mode.initialOrigin || '';
  const insecure = element('p');
  insecure.id = 'scrumboy-mobile-insecure-http';
  const updateInsecure = () => {
    insecure.textContent = input.value.trim().toLowerCase().startsWith('http://')
      ? 'HTTP is insecure and is available only in internal debug builds.'
      : '';
  };
  input.addEventListener('input', updateInsecure);
  updateInsecure();
  const connect = element('button', 'Connect');
  connect.type = 'submit';
  form.append(label, input, insecure, connect, status);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    connect.disabled = true;
    status.textContent = 'Checking server…';
    try {
      await actions.connect(input.value);
    } catch (error) {
      status.textContent = errorText(error);
      connect.disabled = false;
    }
  });
  if (mode.message) status.textContent = mode.message;
  shell.append(form);
  productMount().replaceChildren(shell);
  input.focus();
}
