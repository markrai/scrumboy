(() => {
  const runtimeMarker = document.head.querySelector(
    'meta[name="scrumboy-runtime"]',
  );
  if (runtimeMarker?.content !== 'capacitor') {
    throw new Error('Refusing to start the mobile bootstrap outside the packaged Capacitor runtime');
  }

  const shell = document.createElement('main');
  shell.id = 'scrumboy-mobile-bootstrap';
  shell.setAttribute('aria-labelledby', 'scrumboy-mobile-bootstrap-title');

  const title = document.createElement('h1');
  title.id = 'scrumboy-mobile-bootstrap-title';
  title.textContent = 'Scrumboy';

  const status = document.createElement('p');
  status.textContent = 'Mobile shell ready.';

  const nextPhase = document.createElement('p');
  nextPhase.textContent = 'Server connection will be configured in the next phase.';

  shell.append(title, status, nextPhase);
  document.body.replaceChildren(shell);
})();
