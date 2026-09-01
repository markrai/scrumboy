import {
  getActiveVoiceCommandContext,
  isVoiceMutationCommand,
  parseAndResolveVoiceCommand,
  voiceCommandHash,
  type VoiceCommandOptions,
} from './command-resolution.js';
import {
  activeTodoTransitionAfterSuccessfulIR,
} from './conversation-resolve.js';
import {
  createVoiceConversationSession,
  type VoiceConversationSession,
} from './conversation-session.js';
import type {
  VoiceConversationState,
} from './conversation-state.js';
import { executeCommandIR } from './execute.js';
import type { VoiceCommandInterpreter } from './interpreter.js';
import { resolveVoiceSemanticCommand } from './semantic-command.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import type {
  VoiceSemanticResolution,
  VoiceSemanticSelection,
} from './semantic-resolver.js';
import { formatResolvedCommand } from './resolve.js';
import {
  isCommandFailure,
  localizeCommandFailure,
  type ResolvedCommand,
} from './schema.js';
import type { VoiceMessageDescriptor } from './i18n.js';
import {
  SPEECH_INPUT_MAX_DURATION_MS,
  SpeechInputError,
  type SpeechInputCapability,
} from '../platform/speech-input.js';

export type VoiceAgentPhase =
  | 'ready'
  | 'listening'
  | 'processing'
  | 'question'
  | 'confirmation'
  | 'success'
  | 'error'
  | 'closed';

export type VoiceAgentMessage = VoiceMessageDescriptor | Readonly<{
  kind: 'literal';
  text: string;
}>;

export type VoiceAgentView = Readonly<{
  phase: VoiceAgentPhase;
  status: VoiceAgentMessage;
  confirmation: null | Readonly<{
    summary: string;
    confirmLabel: string;
    danger: boolean;
  }>;
  clarification: null | Readonly<{
    options: readonly Readonly<{
      id: string;
      label: string;
    }>[];
  }>;
}>;

type ReviewedAgentCommand = Readonly<{
  command: ResolvedCommand;
  canonicalTranscript: string;
  semanticIntent: VoiceSemanticIntent | null;
  semanticSelection: VoiceSemanticSelection;
}>;

export type VoiceAgentControllerOptions = VoiceCommandOptions & Readonly<{
  speechInput: SpeechInputCapability;
  interpreter: VoiceCommandInterpreter;
  continuationEnabled: boolean;
  session?: VoiceConversationSession;
  onView(view: VoiceAgentView): void;
}>;

export interface VoiceAgentController {
  getView(): VoiceAgentView;
  getConversationState(): VoiceConversationState;
  matchesContext(context: Pick<VoiceCommandOptions, 'initialUserId' | 'initialProjectId' | 'initialProjectSlug'>): boolean;
  startListening(): Promise<void>;
  submitTranscript(transcript: string): Promise<void>;
  stopListening(): void;
  confirm(): Promise<void>;
  cancelConfirmation(): void;
  chooseClarification(index: number): Promise<void>;
  cancelClarification(): void;
  setContinuationEnabled(enabled: boolean): void;
  invalidate(options?: { clearConversation?: boolean }): void;
  close(): void;
}

const message = (key: string, fallback: string): VoiceMessageDescriptor => ({ key, fallback });
const literal = (text: string): VoiceAgentMessage => ({ kind: 'literal', text });

function speechFailureMessage(error: SpeechInputError): VoiceMessageDescriptor {
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

function pendingInterpreterContext(session: VoiceConversationSession) {
  if (session.getState().pending?.kind !== 'missing-slot') return undefined;
  return {
    pending: { action: 'todo.update_title' as const, slot: 'title' as const },
  };
}

export function createVoiceAgentController(
  options: VoiceAgentControllerOptions,
): VoiceAgentController {
  const session = options.session ?? createVoiceConversationSession();
  session.setContinuationEnabled(options.continuationEnabled);
  let view: VoiceAgentView = Object.freeze({
    phase: 'ready',
    status: message('voice.agent.ready', 'Ready'),
    confirmation: null,
    clarification: null,
  });
  let reviewed: ReviewedAgentCommand | null = null;
  let operationController: AbortController | null = null;
  let operationOwner = 0;
  let closed = false;

  const emit = (
    phase: VoiceAgentPhase,
    status: VoiceAgentMessage,
    confirmation: VoiceAgentView['confirmation'] = null,
    clarification: VoiceAgentView['clarification'] = null,
  ) => {
    view = Object.freeze({ phase, status, confirmation, clarification });
    options.onView(view);
  };

  const owns = (owner: number, controller: AbortController): boolean =>
    !closed
    && operationOwner === owner
    && operationController === controller
    && !controller.signal.aborted;

  const contextIsCurrent = (): boolean => !isCommandFailure(getActiveVoiceCommandContext(options));

  const cancelOwnedOperation = () => {
    operationOwner += 1;
    operationController?.abort();
    operationController = null;
  };

  const monitorContext = (
    owner: number,
    operation: AbortController,
  ): ReturnType<typeof globalThis.setInterval> =>
    globalThis.setInterval(() => {
      if (!owns(owner, operation) || contextIsCurrent()) return;
      cancelOwnedOperation();
      session.clearActiveTodo();
      fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
    }, 100);

  const fail = (status: VoiceAgentMessage) => {
    reviewed = null;
    emit('error', status);
  };

  const completeResolved = async (
    command: ResolvedCommand,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    await executeCommandIR(command.ir, {
      refreshBoard: options.refreshBoard,
      openTodo: options.openTodo,
      recordMutation: options.recordMutation,
      signal: operation.signal,
    });
    if (!owns(owner, operation)) return;
    const activeTransition = activeTodoTransitionAfterSuccessfulIR(
      command.ir,
      session.getState().activeTodo,
    );
    if (activeTransition.kind === 'set') session.setActiveTodo(activeTransition.reference);
    if (activeTransition.kind === 'clear') session.clearActiveTodo();

    const success = command.ir.intent === 'todos.update_title'
      ? { key: 'voice.success.titleUpdated', fallback: 'Title updated successfully.' }
      : { key: 'voice.status.commandComplete', fallback: 'Command complete' };
    if (command.ir.intent === 'todos.update_title') session.clearPendingInteraction();
    session.setLastInteraction({ kind: 'success', message: success });
    reviewed = null;
    if (!session.getState().continuationEnabled) session.reset();
    emit('success', success);
  };

  const stageResolved = async (
    command: ResolvedCommand,
    canonicalTranscript: string,
    semanticIntent: VoiceSemanticIntent | null,
    semanticSelection: VoiceSemanticSelection,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    if (!isVoiceMutationCommand(command)) {
      await completeResolved(command, owner, operation);
      return;
    }
    reviewed = Object.freeze({
      command,
      canonicalTranscript,
      semanticIntent,
      semanticSelection,
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
    emit(
      'confirmation',
      message('voice.agent.confirmation', 'Confirmation required'),
      Object.freeze({
        summary: display.summary,
        confirmLabel: display.confirmLabel,
        danger: command.danger,
      }),
    );
  };

  const completeInformation = (resolution: Extract<VoiceSemanticResolution, { kind: 'information' }>) => {
    session.clearPendingInteraction();
    session.setLastInteraction(resolution.interaction);
    reviewed = null;
    if (!session.getState().continuationEnabled) session.reset();
    emit('success', resolution.interaction.message);
  };

  const applySemanticResolution = async (
    resolution: VoiceSemanticResolution,
    intent: VoiceSemanticIntent,
    transcript: string,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    switch (resolution.kind) {
      case 'command':
        session.clearPendingInteraction();
        await stageResolved(
          resolution.command,
          transcript,
          intent,
          resolution.selection,
          owner,
          operation,
        );
        return;
      case 'question':
        session.setActiveTodo(resolution.target);
        session.setPendingInteraction({
          kind: 'missing-slot',
          action: 'todo.update_title',
          slot: 'title',
          target: resolution.target,
        });
        session.setLastInteraction(resolution.interaction);
        emit('question', resolution.interaction.message);
        return;
      case 'clarification':
        session.setPendingInteraction({
          kind: 'clarification',
          intent: resolution.intent,
          choices: resolution.choices,
          selection: resolution.selection,
        });
        session.setLastInteraction(resolution.interaction);
        emit(
          'question',
          resolution.interaction.message,
          null,
          Object.freeze({ options: resolution.interaction.options }),
        );
        return;
      case 'information':
        if (resolution.target) session.setActiveTodo(resolution.target);
        completeInformation(resolution);
        return;
    }
  };

  const interpretTranscript = async (
    transcript: string,
    owner: number,
    controller: AbortController,
  ): Promise<void> => {
    const normalized = transcript.trim();
    if (!normalized || !owns(owner, controller)) return;
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
    if (!owns(owner, controller)) return;

    if (interpretation.kind === 'unsupported') {
      fail(literal(localizeCommandFailure(interpretation.failure)));
      return;
    }
    if (interpretation.kind === 'candidate') {
      const resolved = await parseAndResolveVoiceCommand(
        interpretation.command,
        options,
        controller.signal,
      );
      if (!owns(owner, controller)) return;
      if (isCommandFailure(resolved)) {
        fail(literal(localizeCommandFailure(resolved)));
        return;
      }
      await stageResolved(
        resolved.value,
        interpretation.command,
        null,
        {},
        owner,
        controller,
      );
      return;
    }

    if (
      session.getState().pending?.kind === 'missing-slot'
      && (
        interpretation.intent.kind !== 'update-todo-title'
        || interpretation.intent.target.kind !== 'current'
      )
    ) {
      session.clearPendingInteraction();
    }
    const resolved = await resolveVoiceSemanticCommand(
      interpretation.intent,
      session,
      options,
      controller.signal,
    );
    if (!owns(owner, controller)) return;
    if (isCommandFailure(resolved)) {
      fail(literal(localizeCommandFailure(resolved)));
      return;
    }
    await applySemanticResolution(
      resolved.value,
      interpretation.intent,
      normalized,
      owner,
      controller,
    );
  };

  const beginTranscriptOperation = async (transcript: string): Promise<void> => {
    if (
      closed
      || operationController
      || reviewed
      || session.getState().pending?.kind === 'clarification'
    ) return;
    const controller = new AbortController();
    const owner = ++operationOwner;
    operationController = controller;
    const contextMonitor = monitorContext(owner, controller);
    try {
      await interpretTranscript(transcript, owner, controller);
    } catch (error) {
      if (!owns(owner, controller)) return;
      fail(message('voice.agent.aiFailed', 'On-device interpretation failed.'));
    } finally {
      globalThis.clearInterval(contextMonitor);
      if (operationOwner === owner && operationController === controller) {
        operationController = null;
      }
    }
  };

  const controller: VoiceAgentController = {
    getView: () => view,
    getConversationState: () => session.getState(),
    matchesContext(context) {
      return options.initialUserId === context.initialUserId
        && options.initialProjectId === context.initialProjectId
        && options.initialProjectSlug === context.initialProjectSlug;
    },
    async startListening() {
      if (
        closed
        || operationController
        || reviewed
        || session.getState().pending?.kind === 'clarification'
      ) return;
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
        if (!owns(owner, operation)) return;
        await interpretTranscript(result.transcript, owner, operation);
      } catch (error) {
        if (!owns(owner, operation)) return;
        if (error instanceof SpeechInputError) {
          if (error.code === 'cancelled') {
            emit('ready', speechFailureMessage(error));
          } else {
            fail(speechFailureMessage(error));
          }
        } else {
          fail(message('voice.agent.speechFailed', 'On-device speech recognition failed.'));
        }
      } finally {
        globalThis.clearInterval(contextMonitor);
        if (operationOwner === owner && operationController === operation) {
          operationController = null;
        }
      }
    },
    submitTranscript: beginTranscriptOperation,
    stopListening() {
      if (closed || !operationController) return;
      cancelOwnedOperation();
      emit('ready', message('voice.agent.stopped', 'Listening stopped.'));
    },
    async confirm() {
      if (closed || operationController || !reviewed) return;
      const pendingReview = reviewed;
      const operation = new AbortController();
      const owner = ++operationOwner;
      operationController = operation;
      const contextMonitor = monitorContext(owner, operation);
      emit('processing', message('voice.agent.processing', 'Processing…'));
      try {
        let freshlyResolved: ResolvedCommand;
        if (pendingReview.semanticIntent) {
          const resolved = await resolveVoiceSemanticCommand(
            pendingReview.semanticIntent,
            session,
            options,
            operation.signal,
            pendingReview.semanticSelection,
          );
          if (!owns(owner, operation)) return;
          if (isCommandFailure(resolved)) {
            fail(literal(localizeCommandFailure(resolved)));
            return;
          }
          if (resolved.value.kind !== 'command') {
            fail(message('voice.status.commandChanged', 'Command changed. Review again before running.'));
            return;
          }
          freshlyResolved = resolved.value.command;
        } else {
          const resolved = await parseAndResolveVoiceCommand(
            pendingReview.canonicalTranscript,
            options,
            operation.signal,
          );
          if (!owns(owner, operation)) return;
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
      } catch (error) {
        if (!owns(owner, operation)) return;
        fail(literal(error instanceof Error && error.message
          ? error.message
          : 'Command failed.'));
      } finally {
        globalThis.clearInterval(contextMonitor);
        if (operationOwner === owner && operationController === operation) {
          operationController = null;
        }
      }
    },
    cancelConfirmation() {
      if (closed || !reviewed) return;
      if (
        reviewed.command.ir.intent === 'todos.update_title'
        || reviewed.semanticIntent?.kind === 'update-todo-title'
      ) {
        session.clearPendingInteraction();
      }
      reviewed = null;
      session.setLastInteraction({
        kind: 'information',
        message: { key: 'voice.status.cancelled', fallback: 'Cancelled' },
      });
      emit('ready', message('voice.status.cancelled', 'Cancelled'));
    },
    async chooseClarification(index) {
      if (closed || operationController || reviewed || !Number.isInteger(index)) return;
      const pending = session.getState().pending;
      if (pending?.kind !== 'clarification' || index < 0 || index >= pending.choices.length) return;
      const choice = pending.choices[index];
      const selected: VoiceSemanticSelection = choice.kind === 'todo'
        ? {
            todo: {
              selectedLocalId: choice.reference.localId,
              allowedLocalIds: pending.choices
                .filter((candidate) => candidate.kind === 'todo')
                .map((candidate) => candidate.reference.localId),
            },
          }
        : choice.kind === 'member'
          ? {
            member: {
              selectedUserId: choice.userId,
              allowedUserIds: pending.choices
                .filter((candidate) => candidate.kind === 'member')
                .map((candidate) => candidate.userId),
            },
          }
          : {
              tag: {
                selectedName: choice.name,
                allowedNames: pending.choices
                  .filter((candidate) => candidate.kind === 'tag')
                  .map((candidate) => candidate.name),
              },
            };
      const selection: VoiceSemanticSelection = {
        ...pending.selection,
        ...selected,
      };
      const operation = new AbortController();
      const owner = ++operationOwner;
      operationController = operation;
      const contextMonitor = monitorContext(owner, operation);
      emit('processing', message('voice.agent.processing', 'Processing…'));
      try {
        const resolved = await resolveVoiceSemanticCommand(
          pending.intent,
          session,
          options,
          operation.signal,
          selection,
        );
        if (!owns(owner, operation)) return;
        if (isCommandFailure(resolved)) {
          fail(literal(localizeCommandFailure(resolved)));
          return;
        }
        session.clearPendingInteraction();
        await applySemanticResolution(
          resolved.value,
          pending.intent,
          '',
          owner,
          operation,
        );
      } catch (error) {
        if (!owns(owner, operation)) return;
        fail(literal(error instanceof Error && error.message ? error.message : 'Command failed.'));
      } finally {
        globalThis.clearInterval(contextMonitor);
        if (operationOwner === owner && operationController === operation) {
          operationController = null;
        }
      }
    },
    cancelClarification() {
      if (closed || session.getState().pending?.kind !== 'clarification') return;
      session.clearPendingInteraction();
      session.setLastInteraction({
        kind: 'information',
        message: { key: 'voice.status.cancelled', fallback: 'Cancelled' },
      });
      emit('ready', message('voice.status.cancelled', 'Cancelled'));
    },
    setContinuationEnabled(enabled) {
      if (closed) return;
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
      if (closed) return;
      cancelOwnedOperation();
      reviewed = null;
      if (invalidationOptions.clearConversation) session.reset();
      emit('ready', message('voice.agent.ready', 'Ready'));
    },
    close() {
      if (closed) return;
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
