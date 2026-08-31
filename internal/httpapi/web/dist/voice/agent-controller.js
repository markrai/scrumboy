import { getActiveVoiceCommandContext, isVoiceMutationCommand, parseAndResolveVoiceCommand, voiceCommandHash, } from './command-resolution.js';
import { resolveVoiceConversationCommand } from './conversation-command.js';
import { activeTodoTransitionAfterSuccessfulIR, } from './conversation-resolve.js';
import { createVoiceConversationSession, } from './conversation-session.js';
import { executeCommandIR } from './execute.js';
import { formatResolvedCommand } from './resolve.js';
import { isCommandFailure, localizeCommandFailure, } from './schema.js';
import { SPEECH_INPUT_MAX_DURATION_MS, SpeechInputError, } from '../platform/speech-input.js';
const message = (key, fallback) => ({ key, fallback });
const literal = (text) => ({ kind: 'literal', text });
function speechFailureMessage(error) {
    switch (error.code) {
        case 'permission_denied':
            return message('voice.agent.permissionDenied', 'Microphone permission was denied.');
        case 'permission_denied_permanently':
            return message('voice.agent.permissionBlocked', 'Microphone permission is blocked. Enable it in system settings.');
        case 'no_speech':
            return message('voice.agent.noSpeech', 'No speech was recognized.');
        case 'timeout':
            return message('voice.agent.timeout', 'Listening stopped after 10 seconds.');
        case 'busy':
            return message('voice.agent.busy', 'VoiceFlow is already listening.');
        case 'foreground_required':
            return message('voice.agent.foreground', 'Keep Scrumboy in the foreground and try again.');
        case 'unsupported':
            return message('voice.agent.speechUnsupported', 'On-device speech input is not supported.');
        case 'cancelled':
            return message('voice.agent.ready', 'Ready');
        default:
            return message('voice.agent.speechFailed', 'On-device speech recognition failed.');
    }
}
function pendingInterpreterContext(session) {
    if (!session.getState().pending)
        return undefined;
    return {
        pending: { action: 'todo.update_title', slot: 'title' },
    };
}
export function createVoiceAgentController(options) {
    const session = options.session ?? createVoiceConversationSession();
    session.setContinuationEnabled(options.continuationEnabled);
    let view = Object.freeze({
        phase: 'ready',
        status: message('voice.agent.ready', 'Ready'),
        confirmation: null,
    });
    let reviewed = null;
    let operationController = null;
    let operationOwner = 0;
    let closed = false;
    const emit = (phase, status, confirmation = null) => {
        view = Object.freeze({ phase, status, confirmation });
        options.onView(view);
    };
    const owns = (owner, controller) => !closed
        && operationOwner === owner
        && operationController === controller
        && !controller.signal.aborted;
    const contextIsCurrent = () => !isCommandFailure(getActiveVoiceCommandContext(options));
    const cancelOwnedOperation = () => {
        operationOwner += 1;
        operationController?.abort();
        operationController = null;
    };
    const monitorContext = (owner, operation) => globalThis.setInterval(() => {
        if (!owns(owner, operation) || contextIsCurrent())
            return;
        cancelOwnedOperation();
        session.clearActiveTodo();
        fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
    }, 100);
    const fail = (status) => {
        reviewed = null;
        emit('error', status);
    };
    const completeResolved = async (command, owner, operation) => {
        await executeCommandIR(command.ir, {
            refreshBoard: options.refreshBoard,
            openTodo: options.openTodo,
            recordMutation: options.recordMutation,
            signal: operation.signal,
        });
        if (!owns(owner, operation))
            return;
        const activeTransition = activeTodoTransitionAfterSuccessfulIR(command.ir, session.getState().activeTodo);
        if (activeTransition.kind === 'set')
            session.setActiveTodo(activeTransition.reference);
        if (activeTransition.kind === 'clear')
            session.clearActiveTodo();
        const success = command.ir.intent === 'todos.update_title'
            ? { key: 'voice.success.titleUpdated', fallback: 'Title updated successfully.' }
            : { key: 'voice.status.commandComplete', fallback: 'Command complete' };
        if (command.ir.intent === 'todos.update_title')
            session.clearPendingInteraction();
        session.setLastInteraction({ kind: 'success', message: success });
        reviewed = null;
        if (!session.getState().continuationEnabled)
            session.reset();
        emit('success', success);
    };
    const stageResolved = async (command, canonicalTranscript, conversationIntent, conversationTarget, owner, operation) => {
        if (!isVoiceMutationCommand(command)) {
            await completeResolved(command, owner, operation);
            return;
        }
        reviewed = Object.freeze({
            command,
            canonicalTranscript,
            conversationIntent,
            conversationTarget,
        });
        const display = formatResolvedCommand(command);
        session.setLastInteraction({
            kind: 'confirmation',
            message: {
                key: 'voice.prompt.confirm',
                fallback: '{summary}. Confirm?',
                values: { summary: display.summary },
            },
            confirmLabel: {
                key: 'voice.agent.confirm',
                fallback: display.confirmLabel,
            },
            danger: command.danger,
        });
        emit('confirmation', message('voice.agent.confirmation', 'Confirmation required'), Object.freeze({
            summary: display.summary,
            confirmLabel: display.confirmLabel,
            danger: command.danger,
        }));
    };
    const interpretTranscript = async (transcript, owner, controller) => {
        const normalized = transcript.trim();
        if (!normalized || !owns(owner, controller))
            return;
        if (!contextIsCurrent()) {
            session.clearActiveTodo();
            fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
            return;
        }
        emit('processing', message('voice.agent.processing', 'Processing…'));
        const interpretation = await options.interpreter.interpret(normalized, {
            signal: controller.signal,
            conversation: pendingInterpreterContext(session),
        });
        if (!owns(owner, controller))
            return;
        if (interpretation.kind === 'unsupported') {
            fail(literal(localizeCommandFailure(interpretation.failure)));
            return;
        }
        if (interpretation.kind === 'candidate') {
            const resolved = await parseAndResolveVoiceCommand(interpretation.command, options, controller.signal);
            if (!owns(owner, controller))
                return;
            if (isCommandFailure(resolved)) {
                fail(literal(localizeCommandFailure(resolved)));
                return;
            }
            await stageResolved(resolved.value, interpretation.command, null, null, owner, controller);
            return;
        }
        const resolved = await resolveVoiceConversationCommand(interpretation.intent, session, options, controller.signal);
        if (!owns(owner, controller))
            return;
        if (isCommandFailure(resolved)) {
            fail(literal(localizeCommandFailure(resolved)));
            return;
        }
        if (resolved.value === null) {
            const interaction = session.getState().lastInteraction;
            emit('question', interaction ? interaction.message : message('voice.question.updateTitle', 'What would you like to change the title to?'));
            return;
        }
        await stageResolved(resolved.value.command, normalized, interpretation.intent, resolved.value.target, owner, controller);
    };
    const beginTranscriptOperation = async (transcript) => {
        if (closed || operationController || reviewed)
            return;
        const controller = new AbortController();
        const owner = ++operationOwner;
        operationController = controller;
        const contextMonitor = monitorContext(owner, controller);
        try {
            await interpretTranscript(transcript, owner, controller);
        }
        catch (error) {
            if (!owns(owner, controller))
                return;
            fail(message('voice.agent.aiFailed', 'On-device interpretation failed.'));
        }
        finally {
            globalThis.clearInterval(contextMonitor);
            if (operationOwner === owner && operationController === controller) {
                operationController = null;
            }
        }
    };
    const controller = {
        getView: () => view,
        getConversationState: () => session.getState(),
        matchesContext(context) {
            return options.initialUserId === context.initialUserId
                && options.initialProjectId === context.initialProjectId
                && options.initialProjectSlug === context.initialProjectSlug;
        },
        async startListening() {
            if (closed || operationController || reviewed)
                return;
            if (!contextIsCurrent()) {
                session.clearActiveTodo();
                fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
                return;
            }
            const operation = new AbortController();
            const owner = ++operationOwner;
            operationController = operation;
            const contextMonitor = monitorContext(owner, operation);
            emit('processing', message('voice.agent.startingMicrophone', 'Starting microphone…'));
            try {
                const result = await options.speechInput.listen({
                    maxDurationMs: SPEECH_INPUT_MAX_DURATION_MS,
                    language: globalThis.navigator?.language || 'en-US',
                    signal: operation.signal,
                    onListening: () => {
                        if (owns(owner, operation)) {
                            emit('listening', message('voice.agent.listening', 'Listening…'));
                        }
                    },
                });
                if (!owns(owner, operation))
                    return;
                await interpretTranscript(result.transcript, owner, operation);
            }
            catch (error) {
                if (!owns(owner, operation))
                    return;
                if (error instanceof SpeechInputError) {
                    if (error.code === 'cancelled') {
                        emit('ready', speechFailureMessage(error));
                    }
                    else {
                        fail(speechFailureMessage(error));
                    }
                }
                else {
                    fail(message('voice.agent.speechFailed', 'On-device speech recognition failed.'));
                }
            }
            finally {
                globalThis.clearInterval(contextMonitor);
                if (operationOwner === owner && operationController === operation) {
                    operationController = null;
                }
            }
        },
        submitTranscript: beginTranscriptOperation,
        stopListening() {
            if (closed || !operationController)
                return;
            cancelOwnedOperation();
            emit('ready', message('voice.agent.stopped', 'Listening stopped.'));
        },
        async confirm() {
            if (closed || operationController || !reviewed)
                return;
            const pendingReview = reviewed;
            const operation = new AbortController();
            const owner = ++operationOwner;
            operationController = operation;
            const contextMonitor = monitorContext(owner, operation);
            emit('processing', message('voice.agent.processing', 'Processing…'));
            try {
                let freshlyResolved;
                if (pendingReview.conversationIntent) {
                    const resolved = await resolveVoiceConversationCommand(pendingReview.conversationIntent, session, options, operation.signal, pendingReview.conversationTarget);
                    if (!owns(owner, operation))
                        return;
                    if (isCommandFailure(resolved)) {
                        fail(literal(localizeCommandFailure(resolved)));
                        return;
                    }
                    if (resolved.value === null)
                        return;
                    freshlyResolved = resolved.value.command;
                }
                else {
                    const resolved = await parseAndResolveVoiceCommand(pendingReview.canonicalTranscript, options, operation.signal);
                    if (!owns(owner, operation))
                        return;
                    if (isCommandFailure(resolved)) {
                        fail(literal(localizeCommandFailure(resolved)));
                        return;
                    }
                    freshlyResolved = resolved.value;
                }
                if (voiceCommandHash(freshlyResolved) !== voiceCommandHash(pendingReview.command)) {
                    fail(message('voice.status.commandChanged', 'Command changed. Review again before running.'));
                    return;
                }
                await completeResolved(freshlyResolved, owner, operation);
            }
            catch (error) {
                if (!owns(owner, operation))
                    return;
                fail(literal(error instanceof Error && error.message
                    ? error.message
                    : 'Command failed.'));
            }
            finally {
                globalThis.clearInterval(contextMonitor);
                if (operationOwner === owner && operationController === operation) {
                    operationController = null;
                }
            }
        },
        cancelConfirmation() {
            if (closed || !reviewed)
                return;
            if (reviewed.command.ir.intent === 'todos.update_title') {
                session.clearPendingInteraction();
            }
            reviewed = null;
            session.setLastInteraction({
                kind: 'information',
                message: { key: 'voice.status.cancelled', fallback: 'Cancelled' },
            });
            emit('ready', message('voice.status.cancelled', 'Cancelled'));
        },
        setContinuationEnabled(enabled) {
            if (closed)
                return;
            const next = !!enabled;
            if (session.getState().continuationEnabled && !next) {
                cancelOwnedOperation();
                reviewed = null;
                session.reset();
            }
            session.setContinuationEnabled(next);
            emit('ready', message('voice.agent.ready', 'Ready'));
        },
        invalidate(invalidationOptions = {}) {
            if (closed)
                return;
            cancelOwnedOperation();
            reviewed = null;
            if (invalidationOptions.clearConversation)
                session.reset();
            emit('ready', message('voice.agent.ready', 'Ready'));
        },
        close() {
            if (closed)
                return;
            closed = true;
            cancelOwnedOperation();
            reviewed = null;
            session.dispose();
            emit('closed', message('voice.agent.closed', 'VoiceFlow closed.'));
        },
    };
    options.onView(view);
    return Object.freeze(controller);
}
