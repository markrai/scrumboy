import { getVoiceFlowHandsFreeConfirmationPreference, getVoiceFlowModePreference, setVoiceFlowHandsFreeConfirmationPreference, setVoiceFlowModePreference, VOICE_FLOW_CONFIRM_DELETES, VOICE_FLOW_CONFIRM_MUTATIONS, } from '../core/voiceflow-preferences.js';
import { isAnonymousBoard, isTemporaryBoard, showConfirmDialog, showToast } from '../utils.js';
import { canRunVoiceMutationCommands, canShowVoiceCommands } from '../views/board-command-capabilities.js';
import { executeCommandIR } from './execute.js';
import { callMcpTool } from './mcp-client.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';
import { startOneShotRecognition } from './speech.js';
import { speak } from './speech-output.js';
import { transitionVoiceInteractionState } from './state-machine.js';
import { commandFailure, isCommandFailure, } from './schema.js';
import { normalizeConfirmationResponse } from './vocabulary.js';
const CONFIRM_DELETES_LABEL = "Confirm only deletes";
const CONFIRM_MUTATIONS_LABEL = "Confirm every action before execution";
function setText(el, text) {
    if (el)
        el.textContent = text;
}
function commandHash(command) {
    return JSON.stringify(command.ir);
}
function draftHash(draft) {
    return JSON.stringify(draft);
}
function dedupeAlternatives(alternatives) {
    const out = [];
    for (const alternative of alternatives) {
        const transcript = String(alternative ?? "").trim();
        if (transcript && !out.includes(transcript))
            out.push(transcript);
        if (out.length >= 3)
            break;
    }
    return out;
}
function isMutationCommand(command) {
    switch (command.ir.intent) {
        case "todos.create":
        case "todos.move":
        case "todos.delete":
        case "todos.assign":
            return true;
        case "open_todo":
            return false;
        default: {
            const exhaustive = command.ir;
            return exhaustive;
        }
    }
}
function canRunResolvedCommand(context, command) {
    if (!isMutationCommand(command))
        return true;
    return canRunVoiceMutationCommands({
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        role: context.role,
        isTemporary: isTemporaryBoard(context.board),
        isAnonymous: isAnonymousBoard(context.board),
    });
}
function getActiveContext(options) {
    const context = options.getContext();
    if (!context || context.projectId !== options.initialProjectId || context.projectSlug !== options.initialProjectSlug) {
        return commandFailure("stale_context", "The board changed before the command could run.");
    }
    const allowed = canShowVoiceCommands({
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        role: context.role,
        isTemporary: isTemporaryBoard(context.board),
        isAnonymous: isAnonymousBoard(context.board),
    });
    if (!allowed) {
        return commandFailure("stale_context", "Commands are unavailable for this board.");
    }
    return { ok: true, value: context };
}
async function resolveParsedDraft(draft, context, signal) {
    return resolveCommandDraft(draft, {
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        board: context.board,
        members: context.members,
        callTool: (tool, input) => callMcpTool(tool, input, { signal }),
    });
}
export async function parseAndResolveCommand(transcript, options, signal) {
    const context = getActiveContext(options);
    if (isCommandFailure(context))
        return context;
    const parsed = parseCommand(transcript);
    if (isCommandFailure(parsed))
        return parsed;
    const resolved = await resolveParsedDraft(parsed.value, context.value, signal);
    if (isCommandFailure(resolved))
        return resolved;
    if (!canRunResolvedCommand(context.value, resolved.value)) {
        return commandFailure("unauthorized", "Only maintainers can run mutating commands.");
    }
    return resolved;
}
export async function parseAlternatives(alternatives, options, signal) {
    const successes = [];
    let firstFailure = null;
    for (const transcript of dedupeAlternatives(alternatives)) {
        const parsed = parseCommand(transcript);
        if (!isCommandFailure(parsed)) {
            successes.push({ transcript, draft: parsed.value });
        }
        else if (!firstFailure) {
            firstFailure = parsed;
        }
    }
    if (successes.length === 0) {
        return firstFailure ?? commandFailure("unsupported", "Unsupported command.");
    }
    const first = successes[0];
    if (successes.some((candidate) => candidate.draft.intent !== first.draft.intent)) {
        return commandFailure("unsupported", "Speech matched more than one command. Review the text and try again.");
    }
    const context = getActiveContext(options);
    if (isCommandFailure(context))
        return context;
    if (first.draft.intent === "todos.create") {
        const resolved = await resolveParsedDraft(first.draft, context.value, signal);
        if (isCommandFailure(resolved))
            return resolved;
        if (!canRunResolvedCommand(context.value, resolved.value)) {
            return commandFailure("unauthorized", "Only maintainers can run mutating commands.");
        }
        return { ok: true, value: { transcript: first.draft.display, resolved: resolved.value } };
    }
    const resolvedByHash = new Map();
    const seenDrafts = new Set();
    let firstResolvedFailure = null;
    for (const candidate of successes) {
        const candidateHash = draftHash(candidate.draft);
        if (seenDrafts.has(candidateHash))
            continue;
        seenDrafts.add(candidateHash);
        const resolved = await resolveParsedDraft(candidate.draft, context.value, signal);
        if (isCommandFailure(resolved)) {
            if (!firstResolvedFailure)
                firstResolvedFailure = resolved;
            continue;
        }
        if (!canRunResolvedCommand(context.value, resolved.value)) {
            if (!firstResolvedFailure) {
                firstResolvedFailure = commandFailure("unauthorized", "Only maintainers can run mutating commands.");
            }
            continue;
        }
        const resolvedHash = commandHash(resolved.value);
        if (!resolvedByHash.has(resolvedHash)) {
            resolvedByHash.set(resolvedHash, { transcript: candidate.draft.display, resolved: resolved.value });
        }
    }
    if (resolvedByHash.size === 1) {
        return { ok: true, value: Array.from(resolvedByHash.values())[0] };
    }
    if (resolvedByHash.size > 1) {
        return commandFailure("unsupported", "Speech matched more than one command. Review the text and try again.");
    }
    return firstResolvedFailure ?? commandFailure("unsupported", "Unsupported command.");
}
export function parseConfirmationAlternatives(alternatives) {
    const confirmations = [];
    for (const transcript of dedupeAlternatives(alternatives)) {
        const confirmation = normalizeConfirmationResponse(transcript);
        if (confirmation && !confirmations.includes(confirmation)) {
            confirmations.push(confirmation);
        }
    }
    if (confirmations.length === 1) {
        return { ok: true, value: confirmations[0] };
    }
    if (confirmations.length > 1) {
        return commandFailure("unsupported", "Confirmation was ambiguous.");
    }
    return commandFailure("unsupported", "Please say yes or no.");
}
function createDialog() {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog voice-command-dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form voice-command" id="voiceCommandForm">
      <div class="dialog__header">
        <div class="dialog__title">VoiceFlow</div>
        <button class="btn btn--ghost" type="button" id="voiceCommandClose" aria-label="Close">x</button>
      </div>

      <div class="voice-command__tabs" role="tablist" aria-label="Command input mode">
        <button type="button" class="voice-command__tab voice-command__tab--active" id="voiceModeSafe">Safe-Mode</button>
        <button type="button" class="voice-command__tab" id="voiceModeHandsFree">Hands-Free</button>
      </div>
      <div class="voice-command__state" id="voiceFlowState" aria-live="polite"></div>

      <div class="voice-command__speech" id="voiceSpeechPanel">
        <button type="button" class="btn" id="voiceListenBtn">Listen</button>
        <button type="button" class="btn btn--ghost" id="voiceStopBtn" disabled>Stop</button>
        <span class="voice-command__status" id="voiceListenStatus" aria-live="polite"></span>
      </div>

      <label class="field">
        <div class="field__label">Command</div>
        <textarea id="voiceTranscript" class="input voice-command__transcript" rows="3" maxlength="260" placeholder="create story Fix login"></textarea>
      </label>
      <div class="voice-command__confirmation-policy" id="voiceHandsFreeConfirmPolicy" hidden>
        <label class="voice-command__switch">
          <input type="checkbox" id="voiceHandsFreeConfirmToggle" role="switch" aria-describedby="voiceHandsFreeConfirmLabel" />
          <span class="voice-command__switch-track" aria-hidden="true">
            <span class="voice-command__switch-thumb"></span>
          </span>
          <span class="voice-command__confirmation-label" id="voiceHandsFreeConfirmLabel">${CONFIRM_DELETES_LABEL}</span>
        </label>
      </div>

      <div class="voice-command__review">
        <button type="button" class="btn btn--ghost" id="voiceReviewBtn">Review</button>
        <span class="voice-command__status" id="voiceReviewStatus" aria-live="polite"></span>
      </div>

      <div class="voice-command__summary" id="voiceSummary" hidden></div>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="voiceCancelBtn">Cancel</button>
        <button type="submit" class="btn" id="voiceExecuteBtn" disabled>Execute</button>
      </div>
    </form>
  `;
    return dialog;
}
export function openVoiceCommandDialog(options) {
    const existing = document.getElementById("voiceCommandDialog");
    if (existing?.parentNode) {
        existing.dispatchEvent(new Event("voice-command:close"));
        if (existing.parentNode)
            existing.parentNode.removeChild(existing);
    }
    const dialog = createDialog();
    dialog.id = "voiceCommandDialog";
    document.body.appendChild(dialog);
    const form = dialog.querySelector("#voiceCommandForm");
    const closeBtn = dialog.querySelector("#voiceCommandClose");
    const cancelBtn = dialog.querySelector("#voiceCancelBtn");
    const listenBtn = dialog.querySelector("#voiceListenBtn");
    const stopBtn = dialog.querySelector("#voiceStopBtn");
    const safeTab = dialog.querySelector("#voiceModeSafe");
    const handsFreeTab = dialog.querySelector("#voiceModeHandsFree");
    const speechPanel = dialog.querySelector("#voiceSpeechPanel");
    const transcript = dialog.querySelector("#voiceTranscript");
    const handsFreeConfirmPolicy = dialog.querySelector("#voiceHandsFreeConfirmPolicy");
    const handsFreeConfirmToggle = dialog.querySelector("#voiceHandsFreeConfirmToggle");
    const handsFreeConfirmLabel = dialog.querySelector("#voiceHandsFreeConfirmLabel");
    const reviewBtn = dialog.querySelector("#voiceReviewBtn");
    const executeBtn = dialog.querySelector("#voiceExecuteBtn");
    const summary = dialog.querySelector("#voiceSummary");
    const listenStatus = dialog.querySelector("#voiceListenStatus");
    const reviewStatus = dialog.querySelector("#voiceReviewStatus");
    const stateEl = dialog.querySelector("#voiceFlowState");
    const notify = options.showMessage ?? showToast;
    let mode = getVoiceFlowModePreference();
    let handsFreeConfirmation = getVoiceFlowHandsFreeConfirmationPreference();
    let flowState = "idle";
    let currentCommand = null;
    let executing = false;
    let closed = false;
    let listenStoppedByUser = false;
    let lastExecutedHash = null;
    let listenController = null;
    let reviewController = null;
    let executeController = null;
    const safeSetText = (el, text) => {
        if (!closed)
            setText(el, text);
    };
    const setFlowState = (event) => {
        flowState = transitionVoiceInteractionState(flowState, event);
        safeSetText(stateEl, flowState.replace(/_/g, " "));
    };
    const applyHandsFreeConfirmationPreference = () => {
        const confirmMutations = handsFreeConfirmation === VOICE_FLOW_CONFIRM_MUTATIONS;
        if (handsFreeConfirmToggle) {
            handsFreeConfirmToggle.checked = confirmMutations;
            handsFreeConfirmToggle.setAttribute("aria-checked", String(confirmMutations));
        }
        safeSetText(handsFreeConfirmLabel, confirmMutations ? CONFIRM_MUTATIONS_LABEL : CONFIRM_DELETES_LABEL);
    };
    const clearResolved = () => {
        currentCommand = null;
        if (summary) {
            summary.hidden = true;
            summary.textContent = "";
        }
        if (executeBtn) {
            executeBtn.disabled = true;
            executeBtn.classList.remove("btn--danger");
            executeBtn.textContent = "Execute";
        }
    };
    const close = () => {
        if (closed)
            return;
        closed = true;
        listenController?.abort();
        reviewController?.abort();
        executeController?.abort();
        listenController = null;
        reviewController = null;
        executeController = null;
        if (dialog.open)
            dialog.close();
        dialog.remove();
    };
    const stopListening = () => {
        listenStoppedByUser = true;
        listenController?.abort();
        listenController = null;
        if (listenBtn)
            listenBtn.disabled = false;
        if (stopBtn)
            stopBtn.disabled = true;
    };
    const setMode = (nextMode, persist = true) => {
        if (mode !== nextMode) {
            stopListening();
            reviewController?.abort();
            executeController?.abort();
            clearResolved();
        }
        mode = nextMode;
        if (persist)
            setVoiceFlowModePreference(nextMode);
        safeTab?.classList.toggle("voice-command__tab--active", mode === "safe");
        handsFreeTab?.classList.toggle("voice-command__tab--active", mode === "hands-free");
        if (speechPanel)
            speechPanel.hidden = false;
        if (handsFreeConfirmPolicy)
            handsFreeConfirmPolicy.hidden = mode !== "hands-free";
        if (reviewBtn)
            reviewBtn.hidden = mode === "hands-free";
        if (executeBtn)
            executeBtn.hidden = mode === "hands-free";
        if (transcript)
            transcript.readOnly = mode === "hands-free";
        safeSetText(listenStatus, "");
        safeSetText(reviewStatus, "");
        setFlowState("reset");
    };
    const shouldConfirmHandsFreeCommand = (resolved) => {
        if (handsFreeConfirmation === VOICE_FLOW_CONFIRM_MUTATIONS) {
            return isMutationCommand(resolved);
        }
        return resolved.danger;
    };
    const applyResolved = (resolved) => {
        if (closed)
            return;
        currentCommand = resolved;
        safeSetText(summary, resolved.summary);
        if (summary)
            summary.hidden = false;
        if (executeBtn) {
            executeBtn.disabled = mode === "hands-free";
            executeBtn.textContent = resolved.confirmLabel;
            executeBtn.classList.toggle("btn--danger", resolved.danger);
        }
        safeSetText(reviewStatus, "");
    };
    const reviewTranscript = async () => {
        reviewController?.abort();
        const controller = new AbortController();
        reviewController = controller;
        clearResolved();
        const value = transcript?.value.trim() ?? "";
        safeSetText(reviewStatus, "Reviewing...");
        try {
            const resolved = await parseAndResolveCommand(value, options, controller.signal);
            if (closed || controller.signal.aborted || reviewController !== controller)
                return;
            if (isCommandFailure(resolved)) {
                safeSetText(reviewStatus, resolved.message);
                return;
            }
            applyResolved(resolved.value);
        }
        finally {
            if (reviewController === controller)
                reviewController = null;
        }
    };
    const executeReviewedCommand = async (reviewedCommand, controller) => {
        const reviewedHash = commandHash(reviewedCommand);
        if (reviewedHash === lastExecutedHash) {
            safeSetText(reviewStatus, "This command already ran.");
            return false;
        }
        const value = transcript?.value.trim() ?? "";
        const resolved = await parseAndResolveCommand(value, options, controller.signal);
        if (closed || controller.signal.aborted || executeController !== controller)
            return false;
        if (isCommandFailure(resolved)) {
            safeSetText(reviewStatus, resolved.message);
            return false;
        }
        const nextHash = commandHash(resolved.value);
        if (nextHash !== reviewedHash) {
            clearResolved();
            safeSetText(reviewStatus, "Command changed. Review again before running.");
            return false;
        }
        await executeCommandIR(resolved.value.ir, {
            refreshBoard: options.refreshBoard,
            openTodo: options.openTodo,
            recordMutation: options.recordMutation,
            signal: controller.signal,
        });
        if (closed || controller.signal.aborted || executeController !== controller)
            return false;
        lastExecutedHash = nextHash;
        return true;
    };
    const runHandsFreeConfirmation = async (resolved, controller) => {
        for (let attempt = 0; attempt < 2; attempt += 1) {
            setFlowState("speak_confirmation");
            safeSetText(reviewStatus, `${resolved.summary}. Confirm?`);
            await speak(`${resolved.summary}. Confirm?`, { signal: controller.signal });
            if (closed || controller.signal.aborted || executeController !== controller)
                return false;
            setFlowState("listen_confirmation");
            safeSetText(listenStatus, "Say yes or no");
            const speech = await startOneShotRecognition({ signal: controller.signal, timeoutMs: 8000 });
            if (closed || controller.signal.aborted || executeController !== controller)
                return false;
            const confirmation = parseConfirmationAlternatives(speech.alternatives);
            if (isCommandFailure(confirmation)) {
                safeSetText(listenStatus, attempt === 0 ? "Please say yes or no." : "Confirmation not understood.");
                continue;
            }
            if (confirmation.value === "no" || confirmation.value === "cancel") {
                setFlowState("cancel");
                safeSetText(reviewStatus, "Cancelled");
                return false;
            }
            return true;
        }
        setFlowState("error");
        return false;
    };
    const runHandsFreeCommand = async () => {
        listenController?.abort();
        reviewController?.abort();
        executeController?.abort();
        clearResolved();
        listenStoppedByUser = false;
        const controller = new AbortController();
        listenController = controller;
        executeController = controller;
        setFlowState("start_command");
        safeSetText(listenStatus, "Listening...");
        if (listenBtn)
            listenBtn.disabled = true;
        if (stopBtn)
            stopBtn.disabled = false;
        try {
            const speech = await startOneShotRecognition({ signal: controller.signal });
            if (closed || controller.signal.aborted || listenController !== controller)
                return;
            const parsed = await parseAlternatives(speech.alternatives, options, controller.signal);
            if (closed || controller.signal.aborted || listenController !== controller)
                return;
            if (isCommandFailure(parsed)) {
                if (transcript && speech.alternatives[0])
                    transcript.value = speech.alternatives[0];
                safeSetText(listenStatus, parsed.message);
                setFlowState("error");
                return;
            }
            if (transcript)
                transcript.value = parsed.value.transcript;
            setFlowState("parsed");
            applyResolved(parsed.value.resolved);
            setFlowState("show_feedback");
            const shouldConfirm = shouldConfirmHandsFreeCommand(parsed.value.resolved);
            if (shouldConfirm) {
                const confirmed = await runHandsFreeConfirmation(parsed.value.resolved, controller);
                if (!confirmed)
                    return;
            }
            setFlowState("execute");
            safeSetText(reviewStatus, "Running...");
            const executed = await executeReviewedCommand(parsed.value.resolved, controller);
            if (!executed)
                return;
            setFlowState("success");
            notify("Command complete");
            close();
        }
        catch (err) {
            if (!closed && !controller.signal.aborted) {
                safeSetText(listenStatus, err?.message || "Speech recognition failed.");
                setFlowState("error");
            }
            else if (!closed && listenStoppedByUser) {
                safeSetText(listenStatus, "Stopped");
                setFlowState("cancel");
            }
        }
        finally {
            if (listenController === controller)
                listenController = null;
            if (executeController === controller)
                executeController = null;
            if (!closed) {
                if (listenBtn)
                    listenBtn.disabled = false;
                if (stopBtn)
                    stopBtn.disabled = true;
            }
        }
    };
    safeTab?.addEventListener("click", () => setMode("safe"));
    handsFreeTab?.addEventListener("click", () => {
        setMode("hands-free");
        void runHandsFreeCommand();
    });
    handsFreeConfirmToggle?.addEventListener("change", () => {
        handsFreeConfirmation = handsFreeConfirmToggle.checked ? VOICE_FLOW_CONFIRM_MUTATIONS : VOICE_FLOW_CONFIRM_DELETES;
        setVoiceFlowHandsFreeConfirmationPreference(handsFreeConfirmation);
        applyHandsFreeConfirmationPreference();
    });
    closeBtn?.addEventListener("click", close);
    cancelBtn?.addEventListener("click", close);
    dialog.addEventListener("voice-command:close", close);
    dialog.addEventListener("click", (event) => {
        if (event.target === dialog)
            close();
    });
    dialog.addEventListener("cancel", (event) => {
        event.preventDefault();
        close();
    });
    transcript?.addEventListener("input", clearResolved);
    reviewBtn?.addEventListener("click", () => {
        void reviewTranscript();
    });
    listenBtn?.addEventListener("click", async () => {
        if (mode === "hands-free") {
            void runHandsFreeCommand();
            return;
        }
        listenController?.abort();
        reviewController?.abort();
        clearResolved();
        listenStoppedByUser = false;
        const controller = new AbortController();
        listenController = controller;
        setFlowState("start_command");
        safeSetText(listenStatus, "Listening...");
        if (listenBtn)
            listenBtn.disabled = true;
        if (stopBtn)
            stopBtn.disabled = false;
        try {
            const speech = await startOneShotRecognition({ signal: controller.signal });
            if (closed || controller.signal.aborted || listenController !== controller)
                return;
            const parsed = await parseAlternatives(speech.alternatives, options, controller.signal);
            if (closed || controller.signal.aborted || listenController !== controller)
                return;
            if (isCommandFailure(parsed)) {
                if (transcript && speech.alternatives[0])
                    transcript.value = speech.alternatives[0];
                safeSetText(listenStatus, parsed.message);
                return;
            }
            if (transcript)
                transcript.value = parsed.value.transcript;
            setFlowState("parsed");
            applyResolved(parsed.value.resolved);
            setFlowState("show_feedback");
            safeSetText(listenStatus, "Ready");
        }
        catch (err) {
            if (!closed && !controller.signal.aborted) {
                safeSetText(listenStatus, err?.message || "Speech recognition failed.");
                setFlowState("error");
            }
            else if (!closed && listenStoppedByUser) {
                safeSetText(listenStatus, "Stopped");
                setFlowState("cancel");
            }
        }
        finally {
            if (listenController === controller)
                listenController = null;
            if (!closed) {
                if (listenBtn)
                    listenBtn.disabled = false;
                if (stopBtn)
                    stopBtn.disabled = true;
            }
        }
    });
    stopBtn?.addEventListener("click", () => {
        stopListening();
        safeSetText(listenStatus, "Stopped");
        setFlowState("cancel");
    });
    form?.addEventListener("submit", async (event) => {
        event.preventDefault();
        if (mode === "hands-free" || executing || !currentCommand || !executeBtn)
            return;
        const reviewedCommand = currentCommand;
        executeController?.abort();
        const controller = new AbortController();
        executeController = controller;
        executing = true;
        executeBtn.disabled = true;
        setFlowState("execute");
        safeSetText(reviewStatus, "Running...");
        try {
            if (reviewedCommand.danger) {
                const confirmed = await showConfirmDialog(reviewedCommand.summary, "Confirm command", reviewedCommand.confirmLabel);
                if (!confirmed) {
                    executeBtn.disabled = false;
                    safeSetText(reviewStatus, "Cancelled");
                    setFlowState("cancel");
                    return;
                }
            }
            const executed = await executeReviewedCommand(reviewedCommand, controller);
            if (!executed) {
                if (currentCommand)
                    executeBtn.disabled = false;
                return;
            }
            setFlowState("success");
            notify("Command complete");
            close();
        }
        catch (err) {
            if (!closed && !controller.signal.aborted) {
                safeSetText(reviewStatus, err?.message || "Command failed.");
                executeBtn.disabled = false;
                setFlowState("error");
            }
        }
        finally {
            if (executeController === controller)
                executeController = null;
            executing = false;
        }
    });
    setMode(mode, false);
    applyHandsFreeConfirmationPreference();
    dialog.showModal();
    transcript?.focus();
    if (mode === "hands-free") {
        void runHandsFreeCommand();
    }
}
