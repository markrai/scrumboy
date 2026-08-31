import { getLocale } from '../i18n/index.js';
import { LOCAL_TEXT_GENERATION_CAPABILITY, } from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import { runInterpretationLabBatch, } from './interpretation-diagnostics.js';
import { INTERPRETATION_LAB_CANDIDATE_PROFILE, INTERPRETATION_LAB_CURRENT_PROFILE, INTERPRETATION_LAB_EXPERIMENTAL_PROFILE, } from './interpretation-lab-prompts.js';
export const INTERPRETATION_LAB_VERSION = 1;
export const INTERPRETATION_LAB_SEED_CORPUS = [
    'Create a to-do about cleaning the garage',
    'Create a to-do about cleaning the garage today',
    'Create a to-do about fixing the bathroom by 6:00 p.m. Tonight',
    'Create a to-do about fixing the bathroom by 6:00 p.m.',
    'Create a to-do called fix the flux capacitor in the bathroom',
    'Create a to-do called: fix the flux capacitor in the bathroom',
    'Create a to-do called fix flux capacitor in the bathroom',
    'Add a task to call the dentist',
    'Add something reminding me to call the plumber',
    'I need to buy milk',
    'Could you move Bogus to Done?',
    'Can you stick Bogus in Done',
    'Assign Bogus to Ada',
    'Open Bogus',
    'Delete Bogus',
    'Open and delete Bogus',
    'Move Bogus to Done and assign it to Ada',
    'Assign Bogus to Ada and open it',
    'Create a todo to call the dentist and delete Bogus',
    'Create a todo to buy milk and eggs',
    'Create a todo to call Alice and Bob',
].join('\n');
export function createInterpretationLabCopyPayload(results, timestamp) {
    return {
        labVersion: INTERPRETATION_LAB_VERSION,
        timestamp,
        results: results.map((result) => {
            const command = result.envelope.state === 'candidate' || result.envelope.state === 'refused'
                ? result.envelope.command
                : null;
            const unrepresented = result.envelope.state === 'candidate' || result.envelope.state === 'refused'
                ? [...result.envelope.unrepresented]
                : [];
            return {
                input: result.input,
                profile: result.profile,
                rawOutput: result.rawOutput ?? null,
                provider: result.provider,
                classification: result.finalClassification,
                command,
                unrepresented,
                outputValidation: result.envelope,
                canonicalParse: result.canonicalParse,
                resolution: result.resolution,
            };
        }),
    };
}
function readinessMessage(status) {
    switch (status.state) {
        case 'unsupported':
            return 'Local AI is unsupported. No lab calls were started.';
        case 'action-required':
            return `Local AI requires ${status.action}. Prepare it through normal VoiceFlow first.`;
        case 'preparing':
            return 'Local AI is preparing. No lab calls were started.';
        case 'temporarily-unavailable':
            return `Local AI is temporarily unavailable (${status.reason}). No lab calls were started.`;
        case 'ready':
            return 'Local AI is ready.';
    }
}
function profileLabel(profile) {
    switch (profile) {
        case INTERPRETATION_LAB_CURRENT_PROFILE:
            return 'Legacy v2';
        case INTERPRETATION_LAB_EXPERIMENTAL_PROFILE:
            return 'Experimental v3';
        case INTERPRETATION_LAB_CANDIDATE_PROFILE:
            return 'Candidate v3';
    }
}
function stageLabel(stage) {
    return stage.state;
}
function appendCell(row, text) {
    const cell = document.createElement('td');
    cell.textContent = text;
    row.appendChild(cell);
}
function renderLabResults(container, results) {
    container.replaceChildren();
    if (results.length === 0) {
        const empty = document.createElement('p');
        empty.className = 'voice-command__lab-empty';
        empty.textContent = 'No results yet.';
        container.appendChild(empty);
        return;
    }
    const table = document.createElement('table');
    table.className = 'voice-command__lab-table';
    const head = document.createElement('thead');
    const headingRow = document.createElement('tr');
    for (const heading of ['Profile', 'Input', 'Classification', 'Canonical command', 'Unrepresented', 'Parser', 'Resolution']) {
        const cell = document.createElement('th');
        cell.scope = 'col';
        cell.textContent = heading;
        headingRow.appendChild(cell);
    }
    head.appendChild(headingRow);
    table.appendChild(head);
    const body = document.createElement('tbody');
    for (const result of results) {
        const command = result.envelope.state === 'candidate' || result.envelope.state === 'refused'
            ? result.envelope.command ?? ''
            : '';
        const unrepresented = result.envelope.state === 'candidate' || result.envelope.state === 'refused'
            ? result.envelope.unrepresented.join(' | ')
            : '';
        const row = document.createElement('tr');
        appendCell(row, result.profile);
        appendCell(row, result.input);
        appendCell(row, result.finalClassification);
        appendCell(row, command);
        appendCell(row, unrepresented);
        appendCell(row, stageLabel(result.canonicalParse));
        appendCell(row, stageLabel(result.resolution));
        body.appendChild(row);
        const detailRow = document.createElement('tr');
        detailRow.className = 'voice-command__lab-detail-row';
        const detailCell = document.createElement('td');
        detailCell.colSpan = 7;
        const details = document.createElement('details');
        const summary = document.createElement('summary');
        summary.textContent = 'Raw output and validation detail';
        const raw = document.createElement('pre');
        raw.textContent = result.rawOutput ?? '(no provider output)';
        const validation = document.createElement('pre');
        validation.textContent = JSON.stringify({
            provider: result.provider,
            envelope: result.envelope,
            canonicalParse: result.canonicalParse,
            resolution: result.resolution,
        }, null, 2);
        details.append(summary, raw, validation);
        detailCell.appendChild(details);
        detailRow.appendChild(detailCell);
        body.appendChild(detailRow);
    }
    table.appendChild(body);
    container.appendChild(table);
}
async function defaultWriteClipboard(text) {
    if (!navigator.clipboard?.writeText)
        throw new Error('clipboard-unavailable');
    await navigator.clipboard.writeText(text);
}
export function mountInterpretationLab(dialog, options) {
    const anchor = dialog.querySelector('.voice-command__review');
    const launcher = document.createElement('div');
    launcher.className = 'voice-command__lab-launcher';
    launcher.innerHTML = '<button type="button" class="btn btn--ghost" id="voiceInterpretationLabToggle" aria-expanded="false">Interpretation Lab</button>';
    const panel = document.createElement('section');
    panel.className = 'voice-command__lab-panel';
    panel.id = 'voiceInterpretationLabPanel';
    panel.hidden = true;
    panel.innerHTML = `
    <div class="voice-command__lab-heading">
      <strong>Interpretation Lab</strong>
      <span>Experimental / temporary</span>
    </div>
    <p class="voice-command__lab-warning">Characterization only. Commands are parsed and optionally resolved against the current in-memory board, but never confirmed or executed.</p>
    <label class="field">
      <span>Corpus — one nonempty utterance per line</span>
      <textarea class="input voice-command__lab-corpus" id="voiceInterpretationLabCorpus" rows="10"></textarea>
    </label>
    <div class="voice-command__lab-actions">
      <button type="button" class="btn" id="voiceInterpretationLabRunCurrent">Run legacy</button>
      <button type="button" class="btn" id="voiceInterpretationLabRunExperimental">Run experimental</button>
      <button type="button" class="btn" id="voiceInterpretationLabRunCandidate">Run candidate</button>
      <button type="button" class="btn" id="voiceInterpretationLabRunAll">Run all</button>
      <button type="button" class="btn btn--ghost" id="voiceInterpretationLabStop" disabled>Stop</button>
      <button type="button" class="btn btn--ghost" id="voiceInterpretationLabCopy">Copy results</button>
      <button type="button" class="btn btn--ghost" id="voiceInterpretationLabClear">Clear results</button>
    </div>
    <div class="voice-command__status" id="voiceInterpretationLabStatus" aria-live="polite"></div>
    <div class="voice-command__lab-results" id="voiceInterpretationLabResults"></div>
  `;
    if (anchor) {
        anchor.insertAdjacentElement('afterend', launcher);
        launcher.insertAdjacentElement('afterend', panel);
    }
    else {
        dialog.querySelector('form')?.append(launcher, panel);
    }
    const toggle = launcher.querySelector('#voiceInterpretationLabToggle');
    const corpus = panel.querySelector('#voiceInterpretationLabCorpus');
    const runCurrent = panel.querySelector('#voiceInterpretationLabRunCurrent');
    const runExperimental = panel.querySelector('#voiceInterpretationLabRunExperimental');
    const runCandidate = panel.querySelector('#voiceInterpretationLabRunCandidate');
    const runAll = panel.querySelector('#voiceInterpretationLabRunAll');
    const stop = panel.querySelector('#voiceInterpretationLabStop');
    const copy = panel.querySelector('#voiceInterpretationLabCopy');
    const clear = panel.querySelector('#voiceInterpretationLabClear');
    const status = panel.querySelector('#voiceInterpretationLabStatus');
    const resultContainer = panel.querySelector('#voiceInterpretationLabResults');
    if (corpus)
        corpus.value = INTERPRETATION_LAB_SEED_CORPUS;
    const runtime = options.runtime ?? getAppRuntime();
    const locale = options.getLocale ?? getLocale;
    const runBatch = options.runBatch ?? runInterpretationLabBatch;
    const writeClipboard = options.writeClipboard ?? defaultWriteClipboard;
    const now = options.now ?? (() => new Date());
    const results = [];
    let runningController = null;
    let disposed = false;
    const setStatus = (text) => {
        if (!disposed && status)
            status.textContent = text;
    };
    const setRunning = (running) => {
        for (const button of [runCurrent, runExperimental, runCandidate, runAll, clear]) {
            if (button)
                button.disabled = running;
        }
        if (stop)
            stop.disabled = !running;
        if (copy)
            copy.disabled = running;
        if (corpus)
            corpus.readOnly = running;
    };
    const renderResults = () => {
        if (!disposed && resultContainer)
            renderLabResults(resultContainer, results);
    };
    const runProfiles = async (profiles) => {
        if (disposed || runningController)
            return;
        const inputs = (corpus?.value ?? '').split(/\r?\n/).map((input) => input.trim()).filter(Boolean);
        if (inputs.length === 0) {
            setStatus('Enter at least one nonempty utterance.');
            return;
        }
        if (locale() !== 'en') {
            setStatus('The current production interpreter is English-only. No lab calls were started.');
            return;
        }
        const capability = runtime.capability(LOCAL_TEXT_GENERATION_CAPABILITY);
        if (!capability) {
            setStatus('Local AI is absent. No lab calls were started.');
            return;
        }
        const controller = new AbortController();
        runningController = controller;
        setRunning(true);
        setStatus('Checking local AI readiness...');
        try {
            const capabilityStatus = await capability.status({ signal: controller.signal });
            if (disposed || runningController !== controller)
                return;
            if (controller.signal.aborted) {
                setStatus('Stopped.');
                return;
            }
            if (capabilityStatus.state !== 'ready') {
                setStatus(readinessMessage(capabilityStatus));
                return;
            }
            const batch = await runBatch({
                inputs,
                profiles,
                capability,
                locale: 'en',
                signal: controller.signal,
                getResolveContext: options.getResolveContext,
                onProgress(progress) {
                    if (disposed || runningController !== controller)
                        return;
                    setStatus(`${progress.completed} / ${progress.total} — ${profileLabel(progress.profile)}`);
                },
            });
            if (disposed || runningController !== controller)
                return;
            results.push(...batch);
            renderResults();
            setStatus(controller.signal.aborted ? 'Stopped.' : `Complete — ${batch.length} result${batch.length === 1 ? '' : 's'} added.`);
        }
        catch (error) {
            if (!disposed && runningController === controller) {
                setStatus(controller.signal.aborted || (error instanceof Error && error.name === 'AbortError')
                    ? 'Stopped.'
                    : 'The lab run could not start. Local AI remains unchanged.');
            }
        }
        finally {
            if (runningController === controller)
                runningController = null;
            if (!disposed)
                setRunning(false);
        }
    };
    toggle?.addEventListener('click', () => {
        panel.hidden = !panel.hidden;
        toggle.setAttribute('aria-expanded', String(!panel.hidden));
        if (!panel.hidden)
            corpus?.focus();
    });
    runCurrent?.addEventListener('click', () => {
        void runProfiles([INTERPRETATION_LAB_CURRENT_PROFILE]);
    });
    runExperimental?.addEventListener('click', () => {
        void runProfiles([INTERPRETATION_LAB_EXPERIMENTAL_PROFILE]);
    });
    runCandidate?.addEventListener('click', () => {
        void runProfiles([INTERPRETATION_LAB_CANDIDATE_PROFILE]);
    });
    runAll?.addEventListener('click', () => {
        void runProfiles([
            INTERPRETATION_LAB_CURRENT_PROFILE,
            INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
            INTERPRETATION_LAB_CANDIDATE_PROFILE,
        ]);
    });
    stop?.addEventListener('click', () => {
        runningController?.abort();
        setStatus('Stopping...');
    });
    clear?.addEventListener('click', () => {
        results.splice(0, results.length);
        renderResults();
        setStatus('Results cleared.');
    });
    copy?.addEventListener('click', () => {
        if (results.length === 0) {
            setStatus('No results to copy.');
            return;
        }
        const payload = createInterpretationLabCopyPayload(results, now().toISOString());
        void writeClipboard(JSON.stringify(payload, null, 2)).then(() => setStatus('Results copied.'), () => setStatus('Clipboard unavailable. Results remain in memory.'));
    });
    renderResults();
    return {
        dispose() {
            if (disposed)
                return;
            disposed = true;
            runningController?.abort();
            runningController = null;
            results.splice(0, results.length);
            if (corpus)
                corpus.value = '';
            launcher.remove();
            panel.remove();
        },
    };
}
