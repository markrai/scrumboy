import { getVoiceFlowHandsFreeConfirmationPreference, getVoiceFlowContinueConversationPreference, getVoiceFlowModePreference, setVoiceFlowContinueConversationPreference, setVoiceFlowHandsFreeConfirmationPreference, setVoiceFlowModePreference, VOICE_FLOW_CONFIRM_DELETES, VOICE_FLOW_CONFIRM_MUTATIONS, } from '../core/voiceflow-preferences.js';
import { NATIVE_FOREGROUND_EVENT } from '../core/realtime.js';
import { showConfirmDialog, showToast } from '../utils.js';
import { FIELD_TOOLTIPS, fieldLabelHTML, titleAttr } from '../field-tooltips.js';
import { I18N_LOCALE_CHANGED } from '../i18n/index.js';
import { deterministicVoiceCommandInterpreter } from './deterministic-interpreter.js';
import { createVoiceConversationSession } from './conversation-session.js';
import { activeTodoTransitionAfterSuccessfulIR, } from './conversation-resolve.js';
import { executeCommandIR } from './execute.js';
import { normalizeVoiceInterpreterFailure, prepareVoiceCommandInterpreterForTurn, selectVoiceCommandInterpreterForTurn, } from './interpreter-selection.js';
import { parseCommand } from './parser.js';
import { formatResolvedCommand } from './resolve.js';
import { startOneShotRecognition } from './speech.js';
import { speak } from './speech-output.js';
import { transitionVoiceInteractionState } from './state-machine.js';
import { cloneCommandFailure, isCommandFailure, localizedCommandFailure, localizeCommandFailure, } from './schema.js';
import { normalizeConfirmationResponse, normalizeDisambiguationChoice } from './vocabulary.js';
import { renderVoiceMessage, voiceMessage, voiceText } from './i18n.js';
import { canRunResolvedVoiceCommand, getActiveVoiceCommandContext, isVoiceMutationCommand, parseAndResolveVoiceCommand, resolveParsedVoiceDraft, voiceCommandHash, } from './command-resolution.js';
import { resolveVoiceConversationCommand } from './conversation-command.js';
const VOICE_STATE_LABELS = {
    idle: voiceMessage("voice.state.idle", "idle"),
    listening_command: voiceMessage("voice.state.listeningCommand", "listening command"),
    resolving_target: voiceMessage("voice.state.resolvingTarget", "resolving target"),
    parsed: voiceMessage("voice.state.parsed", "parsed"),
    disambiguation_prompt: voiceMessage("voice.state.disambiguationPrompt", "disambiguation prompt"),
    listening_disambiguation: voiceMessage("voice.state.listeningDisambiguation", "listening disambiguation"),
    resolved_target: voiceMessage("voice.state.resolvedTarget", "resolved target"),
    showing_feedback_or_confirmation: voiceMessage("voice.state.showingFeedbackOrConfirmation", "showing feedback or confirmation"),
    speaking_confirmation: voiceMessage("voice.state.speakingConfirmation", "speaking confirmation"),
    listening_confirmation: voiceMessage("voice.state.listeningConfirmation", "listening confirmation"),
    executing: voiceMessage("voice.state.executing", "executing"),
    success: voiceMessage("voice.state.success", "success"),
    cancelled: voiceMessage("voice.state.cancelled", "cancelled"),
    error: voiceMessage("voice.state.error", "error"),
};
const HYDRATE_ATTRS = [
    ["data-i18n-text", "textContent", "data-i18n-fallback"],
    ["data-i18n-aria-label", "aria-label", "data-i18n-fallback-aria-label"],
    ["data-i18n-placeholder", "placeholder", "data-i18n-fallback-placeholder"],
    ["data-i18n-title", "title", "data-i18n-fallback-title"],
];
function setText(el, text) {
    if (el)
        el.textContent = text;
}
function elementsForAttribute(root, attributeName) {
    const elements = [];
    if (typeof Element !== "undefined" && root instanceof Element && root.hasAttribute(attributeName)) {
        elements.push(root);
    }
    root.querySelectorAll?.(`[${attributeName}]`).forEach((element) => elements.push(element));
    return elements;
}
function hydrateVoiceI18n(root) {
    for (const [sourceAttribute, targetAttribute, fallbackAttribute] of HYDRATE_ATTRS) {
        for (const element of elementsForAttribute(root, sourceAttribute)) {
            const key = element.getAttribute(sourceAttribute);
            if (!key)
                continue;
            const fallback = element.getAttribute(fallbackAttribute)
                ?? (targetAttribute === "textContent" ? element.textContent ?? "" : element.getAttribute(targetAttribute) ?? "");
            const message = voiceText(key, fallback);
            if (targetAttribute === "textContent") {
                element.textContent = message;
            }
            else {
                element.setAttribute(targetAttribute, message);
            }
        }
    }
}
function isFailureMessage(message) {
    return message.ok === false;
}
function renderDialogMessage(message) {
    if (!message)
        return "";
    if ("kind" in message) {
        if (message.kind === "literal")
            return message.text;
        const display = formatResolvedCommand(message.command);
        return voiceText("voice.prompt.confirm", "{summary}. Confirm?", { summary: display.summary });
    }
    if (isFailureMessage(message))
        return localizeCommandFailure(message);
    return renderVoiceMessage(message);
}
const commandHash = voiceCommandHash;
function draftHash(draft) {
    return JSON.stringify(draft);
}
function isTargetAmbiguity(result) {
    return result.code === "ambiguous_story" && Array.isArray(result.candidates) && result.candidates.length > 0 && !!result.draft;
}
function choiceIndex(choice) {
    switch (choice) {
        case "option_1":
            return 0;
        case "option_2":
            return 1;
        case "option_3":
            return 2;
    }
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
export const parseAndResolveCommand = parseAndResolveVoiceCommand;
const getActiveContext = getActiveVoiceCommandContext;
const resolveParsedDraft = resolveParsedVoiceDraft;
const canRunResolvedCommand = canRunResolvedVoiceCommand;
const isMutationCommand = isVoiceMutationCommand;
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
        return firstFailure ?? localizedCommandFailure("unsupported", "voice.errors.unsupportedCommand", "Unsupported command.");
    }
    const first = successes[0];
    if (successes.some((candidate) => candidate.draft.intent !== first.draft.intent)) {
        return localizedCommandFailure("unsupported", "voice.errors.speechAmbiguous", "Speech matched more than one command. Review the text and try again.");
    }
    const context = getActiveContext(options);
    if (isCommandFailure(context))
        return context;
    if (first.draft.intent === "todos.create") {
        const resolved = await resolveParsedDraft(first.draft, context.value, signal);
        if (isCommandFailure(resolved))
            return cloneCommandFailure(resolved, { transcript: first.draft.display });
        if (!canRunResolvedCommand(context.value, resolved.value)) {
            return localizedCommandFailure("unauthorized", "voice.errors.unauthorizedMutation", "Only maintainers can run mutating commands.");
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
                firstResolvedFailure = cloneCommandFailure(resolved, { transcript: candidate.draft.display });
            continue;
        }
        if (!canRunResolvedCommand(context.value, resolved.value)) {
            if (!firstResolvedFailure) {
                firstResolvedFailure = localizedCommandFailure("unauthorized", "voice.errors.unauthorizedMutation", "Only maintainers can run mutating commands.");
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
        return localizedCommandFailure("unsupported", "voice.errors.speechAmbiguous", "Speech matched more than one command. Review the text and try again.");
    }
    return firstResolvedFailure ?? localizedCommandFailure("unsupported", "voice.errors.unsupportedCommand", "Unsupported command.");
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
        return localizedCommandFailure("unsupported", "voice.errors.confirmationAmbiguous", "Confirmation was ambiguous.");
    }
    return localizedCommandFailure("unsupported", "voice.errors.confirmationRequired", "Please say yes or no.");
}
export function parseDisambiguationAlternatives(alternatives, candidateCount) {
    const choices = [];
    for (const transcript of dedupeAlternatives(alternatives)) {
        const choice = normalizeDisambiguationChoice(transcript);
        if (!choice)
            continue;
        const index = choiceIndex(choice);
        if (index < candidateCount && !choices.includes(index)) {
            choices.push(index);
        }
    }
    if (choices.length === 1) {
        return { ok: true, value: choices[0] };
    }
    if (choices.length > 1) {
        return localizedCommandFailure("unsupported", "voice.errors.choiceAmbiguous", "Choice was ambiguous.");
    }
    return localizedCommandFailure("unsupported", "voice.errors.choiceRequired", "Please say one, two, or three.");
}
function createDialog() {
    const dialog = document.createElement("dialog");
    dialog.className = "dialog voice-command-dialog";
    dialog.innerHTML = `
    <form method="dialog" class="dialog__form voice-command" id="voiceCommandForm">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="voice.title" data-i18n-fallback="VoiceFlow">VoiceFlow</div>
        <button class="btn btn--ghost" type="button" id="voiceCommandClose" aria-label="Close" data-i18n-aria-label="common.close" data-i18n-fallback-aria-label="Close">x</button>
      </div>

      <div class="voice-command__tabs" role="tablist" aria-label="Command input mode" data-i18n-aria-label="voice.inputMode" data-i18n-fallback-aria-label="Command input mode">
        <button type="button" class="voice-command__tab voice-command__tab--active" id="voiceModeSafe" data-i18n-text="voice.mode.safe" data-i18n-fallback="Safe-Mode">Safe-Mode</button>
        <button type="button" class="voice-command__tab" id="voiceModeHandsFree" data-i18n-text="voice.mode.handsFree" data-i18n-fallback="Hands-Free">Hands-Free</button>
      </div>
      <div class="voice-command__state" id="voiceFlowState" aria-live="polite"></div>

      <div class="voice-command__speech" id="voiceSpeechPanel">
        <button type="button" class="btn" id="voiceListenBtn" data-i18n-text="voice.action.listen" data-i18n-fallback="Listen">Listen</button>
        <button type="button" class="btn btn--ghost" id="voiceStopBtn" disabled data-i18n-text="voice.action.stop" data-i18n-fallback="Stop">Stop</button>
        <span class="voice-command__status" id="voiceListenStatus" aria-live="polite"></span>
      </div>

      <label class="field">
        ${fieldLabelHTML('Command', FIELD_TOOLTIPS.voiceCommand, 'voice.input.label')}
        <textarea id="voiceTranscript" class="input voice-command__transcript" rows="3" maxlength="260" placeholder="create story Fix login" data-i18n-placeholder="voice.input.placeholder" data-i18n-fallback-placeholder="create story Fix login" data-i18n-title="tooltips.voiceCommand"${titleAttr(FIELD_TOOLTIPS.voiceCommand)}></textarea>
      </label>
      <div class="voice-command__confirmation-policy" id="voiceHandsFreeConfirmPolicy" hidden>
        <label class="voice-command__switch">
          <input type="checkbox" id="voiceHandsFreeConfirmToggle" role="switch" aria-describedby="voiceHandsFreeConfirmLabel" />
          <span class="voice-command__switch-track" aria-hidden="true">
            <span class="voice-command__switch-thumb"></span>
          </span>
          <span class="voice-command__confirmation-label" id="voiceHandsFreeConfirmLabel">Confirm only deletes</span>
        </label>
      </div>
      <div class="voice-command__confirmation-policy">
        <label class="voice-command__switch">
          <input type="checkbox" id="voiceContinueConversationToggle" role="switch" aria-describedby="voiceContinueConversationLabel" />
          <span class="voice-command__switch-track" aria-hidden="true">
            <span class="voice-command__switch-thumb"></span>
          </span>
          <span class="voice-command__confirmation-label" id="voiceContinueConversationLabel" data-i18n-text="voice.continueConversation" data-i18n-fallback="Continue conversation">Continue conversation</span>
        </label>
      </div>

      <div class="voice-command__semantic-interaction" id="voiceSemanticInteraction" role="status" aria-live="polite" aria-atomic="true" hidden></div>

      <div class="voice-command__review">
        <button type="button" class="btn btn--ghost" id="voiceReviewBtn" data-i18n-text="voice.action.review" data-i18n-fallback="Review">Review</button>
        <span class="voice-command__status" id="voiceReviewStatus" aria-live="polite"></span>
      </div>

      <section class="voice-command__interpretation" id="voiceInterpretationPanel" hidden>
        <p class="voice-command__interpretation-disclosure" id="voiceInterpretationDisclosure" data-i18n-text="voice.ai.disclosure" data-i18n-fallback="On-device interpretation can rewrite this text as a supported VoiceFlow command. Your text stays on this device.">On-device interpretation can rewrite this text as a supported VoiceFlow command. Your text stays on this device.</p>
        <div class="voice-command__status" id="voiceInterpretationStatus" aria-live="polite"></div>
        <div class="voice-command__interpretation-actions">
          <button type="button" class="btn" id="voiceInterpretBtn" hidden data-i18n-text="voice.ai.interpret" data-i18n-fallback="Interpret on device">Interpret on device</button>
          <button type="button" class="btn" id="voicePrepareBtn" hidden data-i18n-text="voice.ai.setup" data-i18n-fallback="Set up on-device interpretation">Set up on-device interpretation</button>
          <button type="button" class="btn btn--ghost" id="voiceInterpretRetryBtn" hidden data-i18n-text="voice.ai.retry" data-i18n-fallback="Retry">Retry</button>
          <button type="button" class="btn btn--ghost" id="voiceUseBasicBtn" hidden data-i18n-text="voice.ai.useBasic" data-i18n-fallback="Use basic commands">Use basic commands</button>
          <button type="button" class="btn btn--ghost" id="voiceInterpretCancelBtn" hidden data-i18n-text="common.cancel" data-i18n-fallback="Cancel">Cancel</button>
        </div>
      </section>

      <section class="voice-command__interpretation-proposal" id="voiceInterpretationProposal" hidden>
        <dl>
          <div><dt data-i18n-text="voice.ai.original" data-i18n-fallback="Original">Original</dt><dd id="voiceInterpretationOriginal"></dd></div>
          <div><dt data-i18n-text="voice.ai.interpreted" data-i18n-fallback="Interpreted command">Interpreted command</dt><dd id="voiceInterpretationCandidate"></dd></div>
          <div><dt data-i18n-text="voice.ai.willDo" data-i18n-fallback="Will do">Will do</dt><dd id="voiceInterpretationAction"></dd></div>
        </dl>
      </section>

      <div class="voice-command__summary" id="voiceSummary" hidden></div>
      <div class="voice-command__disambiguation" id="voiceDisambiguation" hidden></div>

      <div class="dialog__footer">
        <div class="spacer"></div>
        <button type="button" class="btn btn--ghost" id="voiceCancelBtn" data-i18n-text="common.cancel" data-i18n-fallback="Cancel">Cancel</button>
        <button type="submit" class="btn" id="voiceExecuteBtn" disabled data-i18n-text="voice.action.execute" data-i18n-fallback="Execute">Execute</button>
      </div>
    </form>
  `;
    const commandLabel = dialog.querySelector('[data-i18n-text="voice.input.label"]');
    commandLabel?.setAttribute("data-i18n-fallback", "Command");
    commandLabel?.setAttribute("data-i18n-title", "tooltips.voiceCommand");
    hydrateVoiceI18n(dialog);
    return dialog;
}
export function openVoiceCommandDialog(options) {
    const existing = document.getElementById("voiceCommandDialog");
    if (existing?.parentNode) {
        existing.dispatchEvent(new Event("voice-command:close"));
        if (existing.parentNode)
            existing.parentNode.removeChild(existing);
    }
    const conversationSession = createVoiceConversationSession();
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
    const continueConversationToggle = dialog.querySelector("#voiceContinueConversationToggle");
    const semanticInteraction = dialog.querySelector("#voiceSemanticInteraction");
    const reviewBtn = dialog.querySelector("#voiceReviewBtn");
    const executeBtn = dialog.querySelector("#voiceExecuteBtn");
    const summary = dialog.querySelector("#voiceSummary");
    const disambiguation = dialog.querySelector("#voiceDisambiguation");
    const interpretationPanel = dialog.querySelector("#voiceInterpretationPanel");
    const interpretationDisclosure = dialog.querySelector("#voiceInterpretationDisclosure");
    const interpretationStatus = dialog.querySelector("#voiceInterpretationStatus");
    const interpretBtn = dialog.querySelector("#voiceInterpretBtn");
    const prepareBtn = dialog.querySelector("#voicePrepareBtn");
    const interpretationRetryBtn = dialog.querySelector("#voiceInterpretRetryBtn");
    const useBasicBtn = dialog.querySelector("#voiceUseBasicBtn");
    const interpretationCancelBtn = dialog.querySelector("#voiceInterpretCancelBtn");
    const interpretationProposal = dialog.querySelector("#voiceInterpretationProposal");
    const interpretationOriginal = dialog.querySelector("#voiceInterpretationOriginal");
    const interpretationCandidate = dialog.querySelector("#voiceInterpretationCandidate");
    const interpretationAction = dialog.querySelector("#voiceInterpretationAction");
    const listenStatus = dialog.querySelector("#voiceListenStatus");
    const reviewStatus = dialog.querySelector("#voiceReviewStatus");
    const stateEl = dialog.querySelector("#voiceFlowState");
    const notify = options.showMessage ?? showToast;
    let mode = getVoiceFlowModePreference();
    let handsFreeConfirmation = getVoiceFlowHandsFreeConfirmationPreference();
    let continueConversation = getVoiceFlowContinueConversationPreference();
    let flowState = "idle";
    let currentCommand = null;
    let currentCommandSource = 'deterministic';
    let currentOriginalTranscript = '';
    let currentCanonicalTranscript = '';
    let currentSemanticIntent = null;
    let currentSemanticTarget = null;
    let pendingDisambiguation = null;
    let currentTargetSelection = null;
    let executing = false;
    let closed = false;
    let listenStoppedByUser = false;
    let lastExecutedHash = null;
    let listenController = null;
    let reviewController = null;
    let executeController = null;
    let interpretationController = null;
    let interpretationOwner = 0;
    let interpretationRoute = null;
    let interpretationAvailability = null;
    let interpretationPhase = 'idle';
    let interpretationFailure = null;
    let lastForegroundStatusRefreshAt = 0;
    let listenStatusMessage = null;
    let reviewStatusMessage = null;
    const isActiveHandsFreeRun = (controller) => !closed && !controller.signal.aborted && listenController === controller;
    const safeSetText = (el, text) => {
        if (!closed)
            setText(el, text);
    };
    const renderFlowState = () => {
        safeSetText(stateEl, renderVoiceMessage(VOICE_STATE_LABELS[flowState]));
    };
    const setListenStatus = (message) => {
        listenStatusMessage = message;
        safeSetText(listenStatus, renderDialogMessage(message));
    };
    const setReviewStatus = (message) => {
        reviewStatusMessage = message;
        safeSetText(reviewStatus, renderDialogMessage(message));
    };
    const renderStatuses = () => {
        safeSetText(listenStatus, renderDialogMessage(listenStatusMessage));
        safeSetText(reviewStatus, renderDialogMessage(reviewStatusMessage));
    };
    const renderSemanticInteraction = () => {
        if (!semanticInteraction)
            return;
        const interaction = conversationSession.getState().lastInteraction;
        semanticInteraction.hidden = interaction === null;
        semanticInteraction.textContent = interaction
            ? renderVoiceMessage(interaction.message)
            : "";
    };
    const setSemanticInteraction = (interaction) => {
        conversationSession.setLastInteraction(interaction);
        renderSemanticInteraction();
    };
    const interpreterOptions = (signal) => {
        if (!conversationSession.getState().pending)
            return { signal };
        return {
            signal,
            conversation: {
                pending: { action: 'todo.update_title', slot: 'title' },
            },
        };
    };
    const interpretationErrorMessage = (failure) => {
        switch (failure?.code ?? null) {
            case 'cancelled':
                return '';
            case 'busy':
                return voiceText("voice.ai.busy", "On-device interpretation is busy. Try again shortly.");
            case 'temporarily-unavailable':
                return voiceText("voice.ai.quota", "On-device interpretation is temporarily unavailable. Try again later.");
            case 'foreground-required':
                return voiceText("voice.ai.foreground", "Keep Scrumboy in the foreground, then try again.");
            case 'storage-required':
                return voiceText("voice.ai.storage", "More device storage is required for on-device interpretation.");
            case 'invalid-output':
                return voiceText("voice.ai.invalidOutput", "That interpretation could not be safely understood. Edit the command and try again.");
            case 'input-too-large':
                return voiceText("voice.ai.inputTooLarge", "Shorten the command before interpreting it on device.");
            case 'unavailable':
            case null:
                return voiceText("voice.ai.failed", "On-device interpretation is unavailable. You can edit the command and review it manually.");
        }
    };
    const availabilityMessage = (availability) => {
        if (!availability)
            return '';
        switch (availability.state) {
            case 'absent':
                return '';
            case 'locale-unsupported':
                return voiceText("voice.ai.englishOnly", "On-device interpretation is currently available for English commands only.");
            case 'unsupported':
                return voiceText("voice.ai.unsupported", "On-device interpretation is not supported on this device.");
            case 'action-required':
                if (availability.action === 'download') {
                    return voiceText("voice.ai.downloadRequired", "Set up the on-device model before interpreting commands.");
                }
                if (availability.action === 'enable') {
                    return voiceText("voice.ai.enableRequired", "Enable on-device intelligence in system settings, then retry status.");
                }
                return voiceText("voice.ai.updateRequired", "A system update is required for on-device interpretation.");
            case 'preparing': {
                if (availability.downloadedBytes != null && availability.totalBytes != null && availability.totalBytes > 0) {
                    const percent = Math.max(0, Math.min(100, Math.round((availability.downloadedBytes / availability.totalBytes) * 100)));
                    return voiceText("voice.ai.preparingProgress", "Preparing on-device interpretation: {percent}%", { percent });
                }
                return voiceText("voice.ai.preparing", "Preparing on-device interpretation...");
            }
            case 'ready':
                return voiceText("voice.ai.ready", "This command can be interpreted on this device.");
            case 'temporarily-unavailable':
                switch (availability.reason) {
                    case 'busy':
                        return voiceText("voice.ai.busy", "On-device interpretation is busy. Try again shortly.");
                    case 'quota':
                        return voiceText("voice.ai.quota", "On-device interpretation is temporarily unavailable. Try again later.");
                    case 'foreground':
                        return voiceText("voice.ai.foreground", "Keep Scrumboy in the foreground, then try again.");
                    case 'storage':
                        return voiceText("voice.ai.storage", "More device storage is required for on-device interpretation.");
                    case 'initializing':
                    case 'provider':
                        return voiceText("voice.ai.unavailable", "On-device interpretation is temporarily unavailable.");
                }
        }
    };
    const renderInterpretation = () => {
        if (!interpretationPanel)
            return;
        const visible = interpretationRoute !== null
            && mode === 'safe'
            && interpretationAvailability?.state !== 'absent';
        interpretationPanel.hidden = !visible;
        if (!visible)
            return;
        const busy = interpretationPhase === 'checking'
            || interpretationPhase === 'preparing'
            || interpretationPhase === 'interpreting';
        interpretationPanel.setAttribute('aria-busy', String(busy));
        if (interpretationDisclosure) {
            interpretationDisclosure.hidden = interpretationAvailability?.state === 'locale-unsupported'
                || interpretationAvailability?.state === 'unsupported';
        }
        let message = availabilityMessage(interpretationAvailability);
        if (interpretationPhase === 'checking') {
            message = voiceText("voice.ai.checking", "Checking on-device interpretation...");
        }
        else if (interpretationPhase === 'preparing') {
            message = voiceText("voice.ai.preparing", "Preparing on-device interpretation...");
        }
        else if (interpretationPhase === 'interpreting') {
            message = voiceText("voice.ai.interpreting", "Interpreting on device...");
        }
        else if (interpretationPhase === 'refused') {
            message = voiceText("voice.ai.refused", "That request could not be converted into one supported command. Edit it and try again.");
        }
        else if (interpretationPhase === 'error') {
            message = interpretationErrorMessage(interpretationFailure);
        }
        safeSetText(interpretationStatus, message);
        const availability = interpretationAvailability;
        if (interpretBtn) {
            interpretBtn.hidden = interpretationRoute !== 'legacy-explicit'
                || busy
                || availability?.state !== 'ready';
            interpretBtn.disabled = busy;
        }
        if (prepareBtn) {
            prepareBtn.hidden = busy
                || availability?.state !== 'action-required'
                || availability.action !== 'download';
            prepareBtn.disabled = busy;
        }
        if (interpretationRetryBtn) {
            const selectedProviderRetry = interpretationRoute === 'selected-provider'
                && (interpretationPhase === 'refused'
                    || (interpretationPhase === 'error' && interpretationFailure?.recoverable === true)
                    || availability?.state === 'action-required'
                    || availability?.state === 'preparing'
                    || availability?.state === 'temporarily-unavailable');
            const legacyRetry = interpretationRoute === 'legacy-explicit'
                && !!availability
                && ['action-required', 'preparing', 'temporarily-unavailable'].includes(availability.state);
            interpretationRetryBtn.hidden = busy || (!selectedProviderRetry && !legacyRetry);
            interpretationRetryBtn.disabled = busy;
        }
        if (useBasicBtn) {
            useBasicBtn.hidden = busy
                || interpretationRoute !== 'selected-provider'
                || !(interpretationPhase === 'refused'
                    || interpretationPhase === 'error'
                    || availability?.state === 'action-required'
                    || availability?.state === 'preparing'
                    || availability?.state === 'temporarily-unavailable');
            useBasicBtn.disabled = busy;
        }
        if (interpretationCancelBtn) {
            interpretationCancelBtn.hidden = !busy || interpretationPhase === 'checking';
            interpretationCancelBtn.disabled = !busy;
        }
    };
    const renderInterpretationProposal = () => {
        const isAi = !!currentCommand && currentCommandSource === 'ai';
        if (interpretationProposal)
            interpretationProposal.hidden = !isAi;
        if (!isAi || !currentCommand)
            return;
        safeSetText(interpretationOriginal, currentOriginalTranscript);
        safeSetText(interpretationCandidate, currentCanonicalTranscript);
        safeSetText(interpretationAction, formatResolvedCommand(currentCommand).summary);
    };
    const abortInterpretation = () => {
        interpretationOwner += 1;
        interpretationController?.abort();
        interpretationController = null;
    };
    const clearInterpretation = () => {
        abortInterpretation();
        interpretationRoute = null;
        interpretationAvailability = null;
        interpretationPhase = 'idle';
        interpretationFailure = null;
        if (interpretationPanel)
            interpretationPanel.hidden = true;
        if (interpretationProposal)
            interpretationProposal.hidden = true;
    };
    const renderCurrentCommand = () => {
        if (!currentCommand)
            return;
        const display = formatResolvedCommand(currentCommand);
        safeSetText(summary, display.summary);
        if (executeBtn) {
            executeBtn.textContent = display.confirmLabel;
        }
    };
    const setFlowState = (event) => {
        flowState = transitionVoiceInteractionState(flowState, event);
        renderFlowState();
    };
    const applyHandsFreeConfirmationPreference = () => {
        const confirmMutations = handsFreeConfirmation === VOICE_FLOW_CONFIRM_MUTATIONS;
        if (handsFreeConfirmToggle) {
            handsFreeConfirmToggle.checked = confirmMutations;
            handsFreeConfirmToggle.setAttribute("aria-checked", String(confirmMutations));
        }
        safeSetText(handsFreeConfirmLabel, confirmMutations
            ? voiceText("voice.confirmPolicy.mutations", "Confirm every action before execution")
            : voiceText("voice.confirmPolicy.deletes", "Confirm only deletes"));
    };
    const applyContinueConversationPreference = () => {
        if (continueConversationToggle) {
            continueConversationToggle.checked = continueConversation;
            continueConversationToggle.setAttribute("aria-checked", String(continueConversation));
        }
    };
    const clearResolved = () => {
        currentCommand = null;
        currentCommandSource = 'deterministic';
        currentOriginalTranscript = '';
        currentCanonicalTranscript = '';
        currentSemanticIntent = null;
        currentSemanticTarget = null;
        pendingDisambiguation = null;
        currentTargetSelection = null;
        if (summary) {
            summary.hidden = true;
            summary.textContent = "";
        }
        if (disambiguation) {
            disambiguation.hidden = true;
            disambiguation.replaceChildren();
        }
        if (executeBtn) {
            executeBtn.disabled = true;
            executeBtn.classList.remove("btn--danger");
            executeBtn.textContent = voiceText("voice.action.execute", "Execute");
        }
        if (interpretationProposal)
            interpretationProposal.hidden = true;
    };
    const renderDisambiguation = (pending) => {
        if (!disambiguation)
            return;
        disambiguation.replaceChildren();
        const title = document.createElement("div");
        title.className = "voice-command__disambiguation-title";
        title.textContent = voiceText("voice.prompt.whichOne", "Which one?");
        disambiguation.appendChild(title);
        const list = document.createElement("div");
        list.className = "voice-command__candidate-list";
        pending.candidates.slice(0, 3).forEach((candidate, index) => {
            const button = document.createElement("button");
            button.type = "button";
            button.className = "voice-command__candidate";
            button.dataset.index = String(index);
            button.textContent = `${index + 1}. #${candidate.localId} ${candidate.title}`;
            list.appendChild(button);
        });
        disambiguation.appendChild(list);
        disambiguation.hidden = false;
    };
    const showTargetAmbiguity = (failure, transcriptValue, source = 'deterministic', originalTranscript = transcriptValue) => {
        if (!isTargetAmbiguity(failure))
            return false;
        pendingDisambiguation = {
            transcript: failure.transcript || transcriptValue,
            draft: failure.draft,
            candidates: failure.candidates.slice(0, 3),
            source,
            originalTranscript,
        };
        currentCommand = null;
        currentTargetSelection = null;
        if (summary) {
            summary.hidden = true;
            summary.textContent = "";
        }
        if (executeBtn) {
            executeBtn.disabled = true;
            executeBtn.classList.remove("btn--danger");
            executeBtn.textContent = voiceText("voice.action.execute", "Execute");
        }
        renderDisambiguation(pendingDisambiguation);
        setReviewStatus(failure);
        setFlowState("prompt_disambiguation");
        return true;
    };
    const relocalizeDialog = () => {
        if (closed)
            return;
        hydrateVoiceI18n(dialog);
        renderFlowState();
        applyHandsFreeConfirmationPreference();
        applyContinueConversationPreference();
        renderSemanticInteraction();
        renderCurrentCommand();
        if (pendingDisambiguation)
            renderDisambiguation(pendingDisambiguation);
        renderStatuses();
        renderInterpretation();
        renderInterpretationProposal();
    };
    const onLocaleChange = () => {
        reviewController?.abort();
        reviewController = null;
        abortInterpretation();
        if (currentCommandSource === 'ai')
            clearResolved();
        if (interpretationRoute === 'legacy-explicit') {
            interpretationAvailability = null;
            interpretationPhase = 'idle';
            interpretationFailure = null;
            void refreshLegacyInterpretationAvailability();
        }
        else {
            clearInterpretation();
        }
        relocalizeDialog();
    };
    const close = () => {
        if (closed)
            return;
        closed = true;
        conversationSession.dispose();
        document.removeEventListener(I18N_LOCALE_CHANGED, onLocaleChange);
        document.removeEventListener('visibilitychange', onVisibilityChange);
        window.removeEventListener(NATIVE_FOREGROUND_EVENT, onNativeForeground);
        listenController?.abort();
        reviewController?.abort();
        executeController?.abort();
        abortInterpretation();
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
            clearInterpretation();
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
        setListenStatus(null);
        setReviewStatus(null);
        setFlowState("reset");
    };
    const resetTurn = () => {
        reviewController?.abort();
        reviewController = null;
        clearInterpretation();
        clearResolved();
        if (transcript)
            transcript.value = '';
        lastExecutedHash = null;
        setListenStatus(null);
        setReviewStatus(null);
        setFlowState('reset');
        if (listenBtn)
            listenBtn.disabled = false;
        if (stopBtn)
            stopBtn.disabled = true;
        transcript?.focus();
    };
    const setContinueConversation = (enabled, persist = true) => {
        const next = !!enabled;
        if (continueConversation && !next) {
            conversationSession.reset();
            resetTurn();
            if (semanticInteraction) {
                semanticInteraction.hidden = true;
                semanticInteraction.textContent = '';
            }
        }
        continueConversation = next;
        conversationSession.setContinuationEnabled(next);
        if (persist)
            setVoiceFlowContinueConversationPreference(next);
        applyContinueConversationPreference();
    };
    const completeSuccessfulTurn = () => {
        if (conversationSession.getState().continuationEnabled) {
            resetTurn();
            return;
        }
        close();
    };
    const cancelReviewedCommand = (command) => {
        if (command.ir.intent !== 'todos.update_title')
            return;
        conversationSession.clearPendingInteraction();
        setSemanticInteraction({
            kind: 'information',
            message: { key: 'voice.status.cancelled', fallback: 'Cancelled' },
        });
        clearResolved();
        if (conversationSession.getState().continuationEnabled)
            resetTurn();
    };
    const shouldConfirmHandsFreeCommand = (resolved) => {
        if (handsFreeConfirmation === VOICE_FLOW_CONFIRM_MUTATIONS) {
            return isMutationCommand(resolved);
        }
        return resolved.danger;
    };
    const confirmationTone = (resolved) => {
        if (resolved.ir.intent === "todos.delete")
            return "danger";
        if (resolved.ir.intent === "todos.create")
            return "success";
        return "default";
    };
    const applyResolved = (resolved, metadata = {}) => {
        if (closed)
            return;
        currentCommand = resolved;
        currentCommandSource = metadata.source ?? 'deterministic';
        currentOriginalTranscript = metadata.originalTranscript ?? (transcript?.value.trim() ?? '');
        currentCanonicalTranscript = metadata.canonicalTranscript ?? currentOriginalTranscript;
        currentSemanticIntent = metadata.semanticIntent ?? null;
        currentSemanticTarget = metadata.semanticTarget ?? null;
        pendingDisambiguation = null;
        if (disambiguation) {
            disambiguation.hidden = true;
            disambiguation.replaceChildren();
        }
        safeSetText(summary, formatResolvedCommand(resolved).summary);
        if (summary)
            summary.hidden = false;
        if (executeBtn) {
            executeBtn.disabled = mode === "hands-free";
            executeBtn.textContent = formatResolvedCommand(resolved).confirmLabel;
            executeBtn.classList.toggle("btn--danger", resolved.danger);
        }
        setReviewStatus(null);
        renderInterpretationProposal();
    };
    const awaitPendingQuestion = () => {
        clearResolved();
        if (transcript)
            transcript.value = '';
        setListenStatus(null);
        setReviewStatus(null);
        setFlowState('reset');
        transcript?.focus();
    };
    const resolveSemanticIntent = (intent, signal, targetOverride) => resolveVoiceConversationCommand(intent, conversationSession, options, signal, targetOverride);
    const beginInterpretationOperation = () => {
        abortInterpretation();
        const controller = new AbortController();
        interpretationController = controller;
        return { controller, owner: interpretationOwner };
    };
    const interpretationStillOwns = (controller, owner, originalTranscript) => {
        if (closed
            || controller.signal.aborted
            || interpretationController !== controller
            || interpretationOwner !== owner
            || mode !== 'safe')
            return false;
        if (originalTranscript != null && transcript?.value.trim() !== originalTranscript)
            return false;
        return !isCommandFailure(getActiveContext(options));
    };
    const refreshLegacyInterpretationAvailability = async () => {
        if (interpretationRoute !== 'legacy-explicit' || mode !== 'safe' || closed)
            return;
        if (interpretationController && interpretationPhase === 'checking')
            return;
        const originalTranscript = transcript?.value.trim() ?? '';
        const { controller, owner } = beginInterpretationOperation();
        interpretationPhase = 'checking';
        interpretationFailure = null;
        renderInterpretation();
        try {
            const selection = await selectVoiceCommandInterpreterForTurn({ signal: controller.signal });
            if (!interpretationStillOwns(controller, owner, originalTranscript))
                return;
            interpretationAvailability = selection.availability;
            interpretationPhase = 'idle';
            renderInterpretation();
        }
        catch (error) {
            if (!interpretationStillOwns(controller, owner, originalTranscript))
                return;
            const failure = normalizeVoiceInterpreterFailure(error);
            if (failure.code === 'cancelled')
                return;
            interpretationPhase = 'error';
            interpretationFailure = failure;
            renderInterpretation();
        }
        finally {
            if (interpretationController === controller)
                interpretationController = null;
        }
    };
    const runInterpretationPreparation = async () => {
        if (!interpretationRoute || mode !== 'safe' || closed)
            return;
        const route = interpretationRoute;
        const originalTranscript = transcript?.value.trim() ?? '';
        const { controller, owner } = beginInterpretationOperation();
        interpretationPhase = 'preparing';
        interpretationFailure = null;
        renderInterpretation();
        let restartSelectedTurn = false;
        try {
            await prepareVoiceCommandInterpreterForTurn({ signal: controller.signal });
            if (!interpretationStillOwns(controller, owner, originalTranscript))
                return;
            if (route === 'selected-provider') {
                restartSelectedTurn = true;
            }
            else {
                const selection = await selectVoiceCommandInterpreterForTurn({ signal: controller.signal });
                if (!interpretationStillOwns(controller, owner, originalTranscript))
                    return;
                interpretationAvailability = selection.availability;
                interpretationPhase = 'idle';
                renderInterpretation();
            }
        }
        catch (error) {
            if (!interpretationStillOwns(controller, owner, originalTranscript))
                return;
            const failure = normalizeVoiceInterpreterFailure(error);
            interpretationPhase = failure.code === 'cancelled' ? 'idle' : 'error';
            interpretationFailure = failure.code === 'cancelled' ? null : failure;
            renderInterpretation();
        }
        finally {
            if (interpretationController === controller)
                interpretationController = null;
        }
        if (restartSelectedTurn)
            await reviewTranscript();
    };
    const runInterpretation = async () => {
        if (interpretationRoute !== 'legacy-explicit'
            || interpretationAvailability?.state !== 'ready'
            || mode !== 'safe'
            || closed)
            return;
        const originalTranscript = transcript?.value.trim() ?? '';
        const initialContext = getActiveContext(options);
        if (isCommandFailure(initialContext)) {
            conversationSession.clearActiveTodo();
            setReviewStatus(initialContext);
            return;
        }
        const { controller, owner } = beginInterpretationOperation();
        interpretationPhase = 'interpreting';
        interpretationFailure = null;
        renderInterpretation();
        try {
            const selection = await selectVoiceCommandInterpreterForTurn({ signal: controller.signal });
            if (selection.kind !== 'interpreter' || selection.provider !== 'local-ai') {
                if (!interpretationStillOwns(controller, owner, originalTranscript))
                    return;
                interpretationAvailability = selection.availability;
                interpretationPhase = 'idle';
                renderInterpretation();
                return;
            }
            const interpreted = await selection.interpreter.interpret(originalTranscript, interpreterOptions(controller.signal));
            if (!interpretationStillOwns(controller, owner, originalTranscript))
                return;
            const latestContext = getActiveContext(options);
            if (isCommandFailure(latestContext)
                || latestContext.value.userId !== initialContext.value.userId
                || latestContext.value.projectId !== initialContext.value.projectId
                || latestContext.value.projectSlug !== initialContext.value.projectSlug
                || latestContext.value.board !== initialContext.value.board
                || latestContext.value.members !== initialContext.value.members
                || latestContext.value.role !== initialContext.value.role)
                return;
            if (interpreted.kind === 'unsupported') {
                interpretationPhase = 'refused';
                renderInterpretation();
                return;
            }
            let resolvedCommand;
            let semanticTarget = null;
            if (interpreted.kind === 'semantic') {
                const resolved = await resolveSemanticIntent(interpreted.intent, controller.signal);
                if (!interpretationStillOwns(controller, owner, originalTranscript))
                    return;
                if (isCommandFailure(resolved)) {
                    interpretationRoute = null;
                    interpretationPhase = 'idle';
                    setReviewStatus(resolved);
                    setFlowState('error');
                    renderInterpretation();
                    return;
                }
                if (resolved.value === null) {
                    interpretationRoute = null;
                    interpretationPhase = 'idle';
                    renderSemanticInteraction();
                    awaitPendingQuestion();
                    renderInterpretation();
                    return;
                }
                resolvedCommand = resolved.value.command;
                semanticTarget = resolved.value.target;
            }
            else {
                const resolved = await parseAndResolveCommand(interpreted.command, options, controller.signal);
                if (!interpretationStillOwns(controller, owner, originalTranscript))
                    return;
                if (isCommandFailure(resolved)) {
                    if (showTargetAmbiguity(resolved, interpreted.command, 'ai', originalTranscript)) {
                        interpretationRoute = null;
                        renderInterpretation();
                        return;
                    }
                    interpretationPhase = 'error';
                    interpretationFailure = { code: 'invalid-output', recoverable: false };
                    renderInterpretation();
                    return;
                }
                resolvedCommand = resolved.value;
            }
            interpretationRoute = null;
            interpretationPhase = 'idle';
            applyResolved(resolvedCommand, {
                source: 'ai',
                originalTranscript,
                canonicalTranscript: interpreted.kind === 'candidate'
                    ? interpreted.command
                    : originalTranscript,
                semanticIntent: interpreted.kind === 'semantic'
                    ? interpreted.intent
                    : null,
                semanticTarget,
            });
            setFlowState('target_resolved');
            renderInterpretation();
            executeBtn?.focus();
        }
        catch (error) {
            if (!interpretationStillOwns(controller, owner, originalTranscript))
                return;
            const failure = normalizeVoiceInterpreterFailure(error);
            interpretationPhase = failure.code === 'cancelled' ? 'idle' : 'error';
            interpretationFailure = failure.code === 'cancelled' ? null : failure;
            renderInterpretation();
        }
        finally {
            if (interpretationController === controller)
                interpretationController = null;
        }
    };
    const cancelInterpretation = () => {
        if (interpretationRoute === 'selected-provider') {
            reviewController?.abort();
            reviewController = null;
        }
        abortInterpretation();
        if (interpretationRoute === 'selected-provider') {
            interpretationPhase = 'error';
            interpretationFailure = { code: 'cancelled', recoverable: true };
            setReviewStatus(voiceMessage("voice.status.cancelled", "Cancelled"));
        }
        else {
            interpretationPhase = 'idle';
            interpretationFailure = null;
        }
        renderInterpretation();
        (interpretBtn?.hidden ? interpretationRetryBtn : interpretBtn)?.focus();
    };
    const onVisibilityChange = () => {
        if (document.visibilityState === 'hidden') {
            lastForegroundStatusRefreshAt = 0;
            const route = interpretationRoute;
            const wasEnhanced = route === 'selected-provider' || currentCommandSource === 'ai';
            reviewController?.abort();
            reviewController = null;
            abortInterpretation();
            if (currentCommandSource === 'ai')
                clearResolved();
            interpretationRoute = wasEnhanced ? 'selected-provider' : route;
            interpretationPhase = wasEnhanced ? 'error' : 'idle';
            interpretationFailure = wasEnhanced
                ? { code: 'foreground-required', recoverable: true }
                : null;
            renderInterpretation();
            return;
        }
        refreshInterpretationAfterForeground();
    };
    const onNativeForeground = () => {
        if (document.visibilityState !== 'hidden')
            refreshInterpretationAfterForeground();
    };
    const refreshInterpretationAfterForeground = () => {
        if (interpretationRoute !== 'legacy-explicit')
            return;
        const now = Date.now();
        if (now - lastForegroundStatusRefreshAt < 1000)
            return;
        lastForegroundStatusRefreshAt = now;
        void refreshLegacyInterpretationAvailability();
    };
    const offerInterpretationForFailure = (failure, value) => {
        const parserResult = parseCommand(value);
        if (value.length === 0
            || value.length > 260
            || failure.code !== 'unsupported'
            || !isCommandFailure(parserResult)
            || parserResult.code !== 'unsupported')
            return;
        interpretationRoute = 'legacy-explicit';
        void refreshLegacyInterpretationAvailability();
    };
    const sameTurnContext = (initial) => {
        const latest = getActiveContext(options);
        return !isCommandFailure(latest)
            && latest.value.userId === initial.userId
            && latest.value.projectId === initial.projectId
            && latest.value.projectSlug === initial.projectSlug
            && latest.value.board === initial.board
            && latest.value.members === initial.members
            && latest.value.role === initial.role;
    };
    const reviewTranscript = async (provider = 'select') => {
        reviewController?.abort();
        clearInterpretation();
        const controller = new AbortController();
        reviewController = controller;
        clearResolved();
        const value = transcript?.value.trim() ?? "";
        setReviewStatus(voiceMessage("voice.status.reviewing", "Reviewing..."));
        setFlowState("resolve_target");
        try {
            const selection = provider === 'deterministic'
                ? {
                    kind: 'interpreter',
                    provider: 'deterministic',
                    availability: { state: 'absent' },
                    interpreter: deterministicVoiceCommandInterpreter,
                }
                : await selectVoiceCommandInterpreterForTurn({ signal: controller.signal });
            if (closed || controller.signal.aborted || reviewController !== controller)
                return;
            if (selection.kind === 'enhanced-not-ready') {
                interpretationRoute = 'selected-provider';
                interpretationAvailability = selection.availability;
                interpretationPhase = 'idle';
                interpretationFailure = null;
                setReviewStatus(null);
                setFlowState('reset');
                renderInterpretation();
                return;
            }
            const source = selection.provider === 'local-ai' ? 'ai' : 'deterministic';
            let initialContext = null;
            if (source === 'ai') {
                const activeContext = getActiveContext(options);
                if (isCommandFailure(activeContext)) {
                    conversationSession.clearActiveTodo();
                    setReviewStatus(activeContext);
                    return;
                }
                initialContext = activeContext.value;
                interpretationRoute = 'selected-provider';
                interpretationAvailability = selection.availability;
                interpretationPhase = 'interpreting';
                interpretationFailure = null;
                renderInterpretation();
            }
            const interpretation = await selection.interpreter.interpret(value, interpreterOptions(controller.signal));
            if (closed || controller.signal.aborted || reviewController !== controller)
                return;
            if (initialContext && !sameTurnContext(initialContext))
                return;
            if (interpretation.kind === 'unsupported') {
                if (source === 'ai') {
                    interpretationPhase = 'refused';
                    setReviewStatus(null);
                    setFlowState('error');
                    renderInterpretation();
                }
                else {
                    if (showTargetAmbiguity(interpretation.failure, value))
                        return;
                    setReviewStatus(interpretation.failure);
                }
                return;
            }
            let resolvedCommand;
            let semanticTarget = null;
            if (interpretation.kind === 'semantic') {
                const resolved = await resolveSemanticIntent(interpretation.intent, controller.signal);
                if (closed || controller.signal.aborted || reviewController !== controller)
                    return;
                if (initialContext && !sameTurnContext(initialContext))
                    return;
                if (isCommandFailure(resolved)) {
                    interpretationRoute = null;
                    interpretationPhase = 'idle';
                    setReviewStatus(resolved);
                    setFlowState('error');
                    renderInterpretation();
                    return;
                }
                if (resolved.value === null) {
                    interpretationRoute = null;
                    interpretationPhase = 'idle';
                    renderSemanticInteraction();
                    awaitPendingQuestion();
                    renderInterpretation();
                    return;
                }
                resolvedCommand = resolved.value.command;
                semanticTarget = resolved.value.target;
            }
            else {
                const resolved = await parseAndResolveCommand(interpretation.command, options, controller.signal);
                if (closed || controller.signal.aborted || reviewController !== controller)
                    return;
                if (initialContext && !sameTurnContext(initialContext))
                    return;
                if (isCommandFailure(resolved)) {
                    if (showTargetAmbiguity(resolved, interpretation.command, source, value)) {
                        interpretationRoute = null;
                        renderInterpretation();
                        return;
                    }
                    if (source === 'ai') {
                        interpretationPhase = 'error';
                        interpretationFailure = { code: 'invalid-output', recoverable: false };
                        setFlowState('error');
                        renderInterpretation();
                    }
                    else {
                        setReviewStatus(resolved);
                    }
                    return;
                }
                resolvedCommand = resolved.value;
            }
            interpretationRoute = null;
            interpretationPhase = 'idle';
            applyResolved(resolvedCommand, {
                source,
                originalTranscript: value,
                canonicalTranscript: interpretation.kind === 'candidate'
                    ? interpretation.command
                    : value,
                semanticIntent: interpretation.kind === 'semantic'
                    ? interpretation.intent
                    : null,
                semanticTarget,
            });
            setFlowState("target_resolved");
            renderInterpretation();
        }
        catch (error) {
            if (closed || controller.signal.aborted || reviewController !== controller)
                return;
            const failure = normalizeVoiceInterpreterFailure(error);
            if (failure.code === 'cancelled')
                return;
            if (provider === 'deterministic') {
                setReviewStatus(voiceMessage("voice.errors.unsupportedCommand", "Unsupported command."));
                setFlowState('error');
                return;
            }
            interpretationRoute = 'selected-provider';
            interpretationAvailability ?? (interpretationAvailability = { state: 'temporarily-unavailable', reason: 'provider' });
            interpretationPhase = 'error';
            interpretationFailure = failure;
            setReviewStatus(null);
            setFlowState('error');
            renderInterpretation();
        }
        finally {
            if (reviewController === controller)
                reviewController = null;
        }
    };
    const executeReviewedCommand = async (reviewedCommand, controller, reviewedContext = {
        source: currentCommandSource,
        originalTranscript: currentOriginalTranscript,
        canonicalTranscript: currentCanonicalTranscript,
        semanticIntent: currentSemanticIntent,
        semanticTarget: currentSemanticTarget,
    }) => {
        const reviewedHash = commandHash(reviewedCommand);
        if (reviewedHash === lastExecutedHash) {
            setReviewStatus(voiceMessage("voice.status.alreadyRan", "This command already ran."));
            return false;
        }
        const originalValue = transcript?.value.trim() ?? "";
        if (reviewedContext.source === 'ai' && originalValue !== reviewedContext.originalTranscript) {
            clearResolved();
            setReviewStatus(voiceMessage("voice.status.commandChanged", "Command changed. Review again before running."));
            return false;
        }
        const value = reviewedContext.source === 'ai' ? reviewedContext.canonicalTranscript : originalValue;
        const selection = currentTargetSelection?.transcript === value
            ? {
                selectedLocalId: currentTargetSelection.localId,
                allowedLocalIds: currentTargetSelection.allowedLocalIds,
            }
            : {};
        let resolvedCommand;
        if (reviewedContext.semanticIntent) {
            const resolved = await resolveSemanticIntent(reviewedContext.semanticIntent, controller.signal, reviewedContext.semanticTarget);
            if (closed || controller.signal.aborted || executeController !== controller)
                return false;
            if (isCommandFailure(resolved)) {
                setReviewStatus(resolved);
                return false;
            }
            if (resolved.value === null)
                return false;
            resolvedCommand = resolved.value.command;
        }
        else {
            const resolved = await parseAndResolveCommand(value, options, controller.signal, selection);
            if (closed || controller.signal.aborted || executeController !== controller)
                return false;
            if (isCommandFailure(resolved)) {
                if (showTargetAmbiguity(resolved, value))
                    return false;
                setReviewStatus(resolved);
                return false;
            }
            resolvedCommand = resolved.value;
        }
        const nextHash = commandHash(resolvedCommand);
        if (nextHash !== reviewedHash) {
            clearResolved();
            setReviewStatus(voiceMessage("voice.status.commandChanged", "Command changed. Review again before running."));
            return false;
        }
        await executeCommandIR(resolvedCommand.ir, {
            refreshBoard: options.refreshBoard,
            openTodo: options.openTodo,
            recordMutation: options.recordMutation,
            signal: controller.signal,
        });
        if (closed || controller.signal.aborted || executeController !== controller)
            return false;
        const activeTodoTransition = activeTodoTransitionAfterSuccessfulIR(resolvedCommand.ir, conversationSession.getState().activeTodo);
        if (activeTodoTransition.kind === 'set') {
            conversationSession.setActiveTodo(activeTodoTransition.reference);
        }
        else if (activeTodoTransition.kind === 'clear') {
            conversationSession.clearActiveTodo();
        }
        if (resolvedCommand.ir.intent === 'todos.update_title') {
            conversationSession.clearPendingInteraction();
            setSemanticInteraction({
                kind: 'success',
                message: {
                    key: 'voice.success.titleUpdated',
                    fallback: 'Title updated successfully.',
                },
            });
        }
        lastExecutedHash = nextHash;
        return true;
    };
    const resolvePendingDisambiguation = async (index, controller) => {
        const pending = pendingDisambiguation;
        if (!pending || index < 0 || index >= pending.candidates.length)
            return null;
        const candidate = pending.candidates[index];
        const allowedLocalIds = pending.candidates.map((item) => item.localId);
        if (!allowedLocalIds.includes(candidate.localId))
            return null;
        const resolved = await parseAndResolveCommand(pending.transcript, options, controller.signal, {
            selectedLocalId: candidate.localId,
            allowedLocalIds,
        });
        if (closed || controller.signal.aborted || pendingDisambiguation !== pending)
            return null;
        if (isCommandFailure(resolved)) {
            setReviewStatus(resolved);
            return null;
        }
        if (transcript && pending.source === 'deterministic')
            transcript.value = pending.transcript;
        currentTargetSelection = { transcript: pending.transcript, localId: candidate.localId, allowedLocalIds };
        setFlowState("target_resolved");
        applyResolved(resolved.value, {
            source: pending.source,
            originalTranscript: pending.originalTranscript,
            canonicalTranscript: pending.transcript,
        });
        return resolved.value;
    };
    const runHandsFreeConfirmation = async (resolved, controller) => {
        for (let attempt = 0; attempt < 2; attempt += 1) {
            setFlowState("speak_confirmation");
            const prompt = voiceMessage("voice.prompt.confirm", "{summary}. Confirm?", { summary: formatResolvedCommand(resolved).summary });
            setReviewStatus({ kind: "confirmPrompt", command: resolved });
            await speak(renderVoiceMessage(prompt), { signal: controller.signal });
            if (closed || controller.signal.aborted || executeController !== controller)
                return false;
            setFlowState("listen_confirmation");
            setListenStatus(voiceMessage("voice.status.sayYesNo", "Say yes or no"));
            const speech = await startOneShotRecognition({ signal: controller.signal, timeoutMs: 8000 });
            if (closed || controller.signal.aborted || executeController !== controller)
                return false;
            const confirmation = parseConfirmationAlternatives(speech.alternatives);
            if (isCommandFailure(confirmation)) {
                setListenStatus(attempt === 0
                    ? voiceMessage("voice.errors.confirmationRequired", "Please say yes or no.")
                    : voiceMessage("voice.errors.confirmationNotUnderstood", "Confirmation not understood."));
                continue;
            }
            if (confirmation.value === "no" || confirmation.value === "cancel") {
                setFlowState("cancel");
                setReviewStatus(voiceMessage("voice.status.cancelled", "Cancelled"));
                return false;
            }
            return true;
        }
        setFlowState("error");
        return false;
    };
    const speakDisambiguationPrompt = async (pending, controller) => {
        const optionsText = pending.candidates
            .map((candidate, index) => voiceText("voice.disambiguation.option", "Option {index}: {title}", { index: index + 1, title: candidate.title }))
            .join(". ");
        setReviewStatus(voiceMessage("voice.prompt.whichOne", "Which one?"));
        await speak(voiceText("voice.prompt.disambiguation", "Which one? {options}.", { options: optionsText }), { signal: controller.signal });
    };
    const runHandsFreeDisambiguation = async (failure, fallbackTranscript, controller) => {
        if (!showTargetAmbiguity(failure, fallbackTranscript) || !pendingDisambiguation)
            return null;
        for (let attempt = 0; attempt < 2; attempt += 1) {
            setFlowState("listen_disambiguation");
            await speakDisambiguationPrompt(pendingDisambiguation, controller);
            if (!isActiveHandsFreeRun(controller))
                return null;
            setListenStatus(voiceMessage("voice.status.sayOneTwoThree", "Say one, two, or three"));
            const speech = await startOneShotRecognition({ signal: controller.signal, timeoutMs: 8000 });
            if (!isActiveHandsFreeRun(controller))
                return null;
            const choice = parseDisambiguationAlternatives(speech.alternatives, pendingDisambiguation.candidates.length);
            if (isCommandFailure(choice)) {
                setListenStatus(attempt === 0
                    ? voiceMessage("voice.errors.choiceRequired", "Please say one, two, or three.")
                    : voiceMessage("voice.errors.choiceNotUnderstood", "Choice not understood."));
                continue;
            }
            return resolvePendingDisambiguation(choice.value, controller);
        }
        setFlowState("error");
        setReviewStatus(voiceMessage("voice.errors.choiceNotUnderstood", "Choice not understood."));
        return null;
    };
    const runHandsFreeCommand = async () => {
        listenController?.abort();
        reviewController?.abort();
        executeController?.abort();
        clearInterpretation();
        clearResolved();
        listenStoppedByUser = false;
        const controller = new AbortController();
        listenController = controller;
        executeController = controller;
        setFlowState("start_command");
        setListenStatus(voiceMessage("voice.status.listening", "Listening..."));
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
            let resolvedCommand;
            if (isCommandFailure(parsed)) {
                if (transcript && speech.alternatives[0])
                    transcript.value = speech.alternatives[0];
                const disambiguated = await runHandsFreeDisambiguation(parsed, speech.alternatives[0] || "", controller);
                if (closed || controller.signal.aborted || listenController !== controller)
                    return;
                if (!disambiguated) {
                    if (!isTargetAmbiguity(parsed))
                        setListenStatus(parsed);
                    setFlowState("error");
                    return;
                }
                resolvedCommand = disambiguated;
            }
            else {
                if (transcript)
                    transcript.value = parsed.value.transcript;
                resolvedCommand = parsed.value.resolved;
                setFlowState("parsed");
                applyResolved(resolvedCommand);
                setFlowState("show_feedback");
            }
            if (flowState === "resolved_target")
                setFlowState("show_feedback");
            const shouldConfirm = shouldConfirmHandsFreeCommand(resolvedCommand);
            if (shouldConfirm) {
                const confirmed = await runHandsFreeConfirmation(resolvedCommand, controller);
                if (!confirmed)
                    return;
            }
            setFlowState("execute");
            setReviewStatus(voiceMessage("voice.status.running", "Running..."));
            const executed = await executeReviewedCommand(resolvedCommand, controller);
            if (!executed)
                return;
            setFlowState("success");
            notify(voiceText("voice.status.commandComplete", "Command complete"));
            completeSuccessfulTurn();
        }
        catch (err) {
            if (!closed && !controller.signal.aborted) {
                setListenStatus(err?.message ? { kind: "literal", text: err.message } : voiceMessage("voice.errors.speechRecognitionFailed", "Speech recognition failed."));
                setFlowState("error");
            }
            else if (!closed && listenStoppedByUser) {
                setListenStatus(voiceMessage("voice.status.stopped", "Stopped"));
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
    continueConversationToggle?.addEventListener("change", () => {
        setContinueConversation(continueConversationToggle.checked);
    });
    closeBtn?.addEventListener("click", close);
    cancelBtn?.addEventListener("click", close);
    dialog.addEventListener("voice-command:close", close);
    document.addEventListener(I18N_LOCALE_CHANGED, onLocaleChange);
    document.addEventListener('visibilitychange', onVisibilityChange);
    window.addEventListener(NATIVE_FOREGROUND_EVENT, onNativeForeground);
    dialog.addEventListener("click", (event) => {
        if (event.target === dialog)
            close();
    });
    dialog.addEventListener("cancel", (event) => {
        event.preventDefault();
        close();
    });
    transcript?.addEventListener("input", () => {
        reviewController?.abort();
        reviewController = null;
        clearInterpretation();
        clearResolved();
    });
    reviewBtn?.addEventListener("click", () => {
        void reviewTranscript();
    });
    interpretBtn?.addEventListener('click', () => {
        void runInterpretation();
    });
    prepareBtn?.addEventListener('click', () => {
        void runInterpretationPreparation();
    });
    interpretationRetryBtn?.addEventListener('click', () => {
        if (interpretationRoute === 'selected-provider') {
            void reviewTranscript();
        }
        else {
            void refreshLegacyInterpretationAvailability();
        }
    });
    useBasicBtn?.addEventListener('click', () => {
        void reviewTranscript('deterministic');
    });
    interpretationCancelBtn?.addEventListener('click', cancelInterpretation);
    disambiguation?.addEventListener("click", (event) => {
        const button = event.target.closest(".voice-command__candidate");
        if (!button)
            return;
        const index = Number(button.dataset.index);
        if (!Number.isInteger(index))
            return;
        reviewController?.abort();
        const controller = new AbortController();
        reviewController = controller;
        setReviewStatus(voiceMessage("voice.status.resolving", "Resolving..."));
        void resolvePendingDisambiguation(index, controller).finally(() => {
            if (reviewController === controller)
                reviewController = null;
        });
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
        setListenStatus(voiceMessage("voice.status.listening", "Listening..."));
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
                if (showTargetAmbiguity(parsed, speech.alternatives[0] || ""))
                    return;
                setListenStatus(parsed);
                offerInterpretationForFailure(parsed, speech.alternatives[0] || "");
                return;
            }
            if (transcript)
                transcript.value = parsed.value.transcript;
            setFlowState("parsed");
            applyResolved(parsed.value.resolved);
            setFlowState("show_feedback");
            setListenStatus(voiceMessage("voice.status.ready", "Ready"));
        }
        catch (err) {
            if (!closed && !controller.signal.aborted) {
                setListenStatus(err?.message ? { kind: "literal", text: err.message } : voiceMessage("voice.errors.speechRecognitionFailed", "Speech recognition failed."));
                setFlowState("error");
            }
            else if (!closed && listenStoppedByUser) {
                setListenStatus(voiceMessage("voice.status.stopped", "Stopped"));
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
        setListenStatus(voiceMessage("voice.status.stopped", "Stopped"));
        setFlowState("cancel");
    });
    form?.addEventListener("submit", async (event) => {
        event.preventDefault();
        if (mode === "hands-free" || executing || !currentCommand || !executeBtn)
            return;
        const reviewedCommand = currentCommand;
        const reviewedContext = {
            source: currentCommandSource,
            originalTranscript: currentOriginalTranscript,
            canonicalTranscript: currentCanonicalTranscript,
            semanticIntent: currentSemanticIntent,
            semanticTarget: currentSemanticTarget,
        };
        executeController?.abort();
        const controller = new AbortController();
        executeController = controller;
        executing = true;
        executeBtn.disabled = true;
        setFlowState("execute");
        setReviewStatus(voiceMessage("voice.status.running", "Running..."));
        try {
            if (reviewedContext.source === 'ai' || reviewedCommand.danger) {
                const display = formatResolvedCommand(reviewedCommand);
                const confirmed = await showConfirmDialog(display.summary, reviewedContext.source === 'ai'
                    ? voiceText("voice.ai.confirmTitle", "Confirm interpreted command")
                    : voiceText("voice.confirm.title", "Confirm command"), display.confirmLabel, confirmationTone(reviewedCommand));
                if (!confirmed) {
                    executeBtn.disabled = false;
                    setReviewStatus(voiceMessage("voice.status.cancelled", "Cancelled"));
                    setFlowState("cancel");
                    cancelReviewedCommand(reviewedCommand);
                    return;
                }
            }
            const executed = await executeReviewedCommand(reviewedCommand, controller, reviewedContext);
            if (!executed) {
                if (currentCommand)
                    executeBtn.disabled = false;
                return;
            }
            setFlowState("success");
            notify(voiceText("voice.status.commandComplete", "Command complete"));
            completeSuccessfulTurn();
        }
        catch (err) {
            if (!closed && !controller.signal.aborted) {
                setReviewStatus(err?.message ? { kind: "literal", text: err.message } : voiceMessage("voice.errors.commandFailed", "Command failed."));
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
    setContinueConversation(continueConversation, false);
    renderSemanticInteraction();
    dialog.showModal();
    transcript?.focus();
    if (mode === "hands-free") {
        void runHandsFreeCommand();
    }
}
