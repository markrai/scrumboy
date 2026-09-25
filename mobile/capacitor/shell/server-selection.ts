export type SelectorMode =
  | { kind: 'entry'; initialOrigin?: string; message?: string }
  | { kind: 'saved-unreachable'; origin: string; failure?: unknown };

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

function setStatus(status: HTMLParagraphElement, message: string, error = false): void {
  status.textContent = message;
  status.classList.toggle('mobile-bootstrap__status--error', error);
}

function createShell(titleText: string, helperText: string): {
  shell: HTMLElement;
  panel: HTMLDivElement;
  status: HTMLParagraphElement;
} {
  const shell = element('main');
  shell.id = 'scrumboy-mobile-bootstrap';
  shell.className = 'page page--mobile-bootstrap';
  shell.setAttribute('aria-labelledby', 'scrumboy-mobile-bootstrap-title');

  const container = element('div');
  container.className = 'container mobile-bootstrap__container';

  const brand = element('div');
  brand.className = 'brand mobile-bootstrap__brand';
  const wordmark = element('img');
  wordmark.className = 'brand-text';
  wordmark.src = '/scrumboytext.png';
  wordmark.alt = 'Scrumboy';
  brand.append(wordmark);

  const panel = element('div');
  panel.className = 'panel mobile-bootstrap__panel';
  const header = element('div');
  header.className = 'panel__header';
  const title = element('h1', titleText);
  title.id = 'scrumboy-mobile-bootstrap-title';
  title.className = 'panel__title';
  header.append(title);
  const helper = element('p', helperText);
  helper.className = 'muted mobile-bootstrap__helper';

  const status = element('p');
  status.id = 'scrumboy-mobile-server-status';
  status.className = 'muted mobile-bootstrap__status';
  status.setAttribute('role', 'status');

  panel.append(header, helper);
  container.append(brand, panel);
  shell.append(container);
  return { shell, panel, status };
}

function productMount(): HTMLElement {
  const host = document.getElementById('app');
  if (!host) throw new Error('Packaged Scrumboy application root is missing');
  return host;
}

export function renderServerSelector(mode: SelectorMode, actions: SelectorActions): void {
  if (mode.kind === 'saved-unreachable') {
    const { shell, panel, status } = createShell(
      'Can’t reach your server',
      'Check the saved address or try connecting again.',
    );
    const origin = element('p', mode.origin);
    origin.className = 'mobile-bootstrap__origin';
    const actionsContainer = element('div');
    actionsContainer.className = 'stack mobile-bootstrap__actions';
    const retry = element('button', 'Try again');
    retry.type = 'button';
    retry.className = 'btn mobile-bootstrap__action';
    const change = element('button', 'Change server');
    change.type = 'button';
    change.className = 'btn btn--ghost mobile-bootstrap__action';
    retry.addEventListener('click', async () => {
      retry.disabled = true;
      setStatus(status, 'Checking this server…');
      try {
        await actions.retry?.();
      } catch (error) {
        setStatus(status, errorText(error), true);
        retry.disabled = false;
      }
    });
    change.addEventListener('click', () => actions.change?.());
    setStatus(status, errorText(mode.failure), true);
    actionsContainer.append(retry, change);
    panel.append(origin, status, actionsContainer);
    productMount().replaceChildren(shell);
    return;
  }

  const { shell, panel, status } = createShell(
    'Connect to your server',
    'Enter the address of your Scrumboy instance. You’ll sign in next.',
  );
  const form = element('form');
  form.className = 'stack mobile-bootstrap__form';
  const field = element('div');
  field.className = 'field mobile-bootstrap__field';
  const label = element('label', 'Server address');
  label.className = 'field__label';
  label.htmlFor = 'scrumboy-mobile-server-origin';
  const input = element('input');
  input.className = 'input';
  input.id = 'scrumboy-mobile-server-origin';
  input.name = 'origin';
  input.type = 'url';
  input.inputMode = 'url';
  input.setAttribute('autocomplete', 'url');
  input.setAttribute('autocapitalize', 'none');
  input.setAttribute('autocorrect', 'off');
  input.setAttribute('spellcheck', 'false');
  input.setAttribute('enterkeyhint', 'go');
  input.required = true;
  input.placeholder = 'https://scrumboy.example.com';
  input.value = mode.initialOrigin || '';
  const insecure = element('p');
  insecure.id = 'scrumboy-mobile-insecure-http';
  insecure.className = 'mobile-bootstrap__http-warning';
  const updateInsecure = () => {
    const visible = input.value.trim().toLowerCase().startsWith('http://');
    insecure.textContent = visible
      ? 'HTTP is insecure and is available only in internal debug builds.'
      : '';
    insecure.hidden = !visible;
  };
  input.addEventListener('input', updateInsecure);
  updateInsecure();
  const connect = element('button', 'Connect');
  connect.type = 'submit';
  connect.className = 'btn mobile-bootstrap__action';
  const privacy = element('p', 'This address is stored only on this device.');
  privacy.className = 'muted mobile-bootstrap__privacy';
  field.append(label, input, insecure);
  form.append(field, status, connect, privacy);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    connect.disabled = true;
    setStatus(status, 'Checking this server…');
    try {
      await actions.connect(input.value);
    } catch (error) {
      setStatus(status, errorText(error), true);
      connect.disabled = false;
    }
  });
  if (mode.message) setStatus(status, mode.message);
  panel.append(form);
  productMount().replaceChildren(shell);
  input.focus();
}
