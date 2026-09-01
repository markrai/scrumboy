import {
  getActiveVoiceCommandContext,
  isVoiceMutationCommand,
  parseAndResolveVoiceCommand,
  voiceCommandHash,
  type VoiceCommandOptions,
} from './command-resolution.js';
import { activeTodoTransitionAfterSuccessfulIR } from './conversation-resolve.js';
import {
  createVoiceConversationSession,
  type VoiceConversationSession,
} from './conversation-session.js';
import type {
  VoiceConversationSelection,
  VoiceConversationState,
  VoicePendingClarification,
  VoicePendingMissingSlot,
  VoiceTodoClarificationChoice,
} from './conversation-state.js';
import {
  resolvePendingClarificationSelector,
  resolveRetainedTodoSelector,
  type VoiceDialogueChoiceResolution,
} from './dialogue-resolver.js';
import type {
  VoiceCorrectionDialogueIntent,
  VoiceDialogueIntent,
  VoiceProvideSlotDialogueIntent,
} from './dialogue-intent.js';
import { voiceSemanticOperation } from './dialogue-intent.js';
import { executeCommandIR } from './execute.js';
import type {
  VoiceCommandInterpreter,
  VoiceInterpreterConversationContext,
} from './interpreter.js';
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
import { renderVoiceMessage, type VoiceMessageDescriptor } from './i18n.js';
import {
  SPEECH_INPUT_MAX_DURATION_MS,
  SpeechInputError,
  type SpeechInputCapability,
} from '../platform/speech-input.js';
import type { SpeechOutputCapability } from '../platform/speech-output.js';

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
  originalTodoChoices: readonly VoiceTodoClarificationChoice[];
}>;

export type VoiceAgentControllerOptions = VoiceCommandOptions & Readonly<{
  speechInput: SpeechInputCapability;
  speechOutput?: SpeechOutputCapability | null;
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

const MAX_DIALOGUE_TURNS = 8;
const LONG_AUTHORED_SPEECH_CODE_UNITS = 160;

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

function pendingInterpreterContext(
  session: VoiceConversationSession,
): VoiceInterpreterConversationContext | undefined {
  const pending = session.getState().pending;
  if (!pending) return undefined;
  if (pending.kind === 'missing-slot') {
    return {
      pending: {
        kind: 'missing-slot' as const,
        operation: pending.operation,
        slot: pending.slot,
      } as VoiceInterpreterConversationContext['pending'],
    };
  }
  if (pending.kind === 'confirmation') {
    return {
      pending: {
        kind: 'confirmation' as const,
        operation: pending.operation,
      },
    };
  }
  const first = pending.choices[0];
  if (first?.kind === 'member') return { pending: { kind: 'member-choice' as const } };
  if (first?.kind === 'tag') return { pending: { kind: 'tag-choice' as const } };
  return { pending: { kind: 'todo-choice' as const } };
}

function confirmationSpeech(command: ResolvedCommand): string {
  const display = formatResolvedCommand(command);
  switch (command.ir.intent) {
    case 'todos.append_notes':
    case 'todos.replace_notes':
      if (command.ir.entities.notes.length > LONG_AUTHORED_SPEECH_CODE_UNITS) {
        return renderVoiceMessage(command.ir.intent === 'todos.append_notes'
          ? {
              key: 'voice.prompt.appendNotesLong',
              fallback: 'Add the dictated text to the notes of {title}?',
              values: { title: command.storyTitle ?? 'this todo' },
            }
          : {
              key: 'voice.prompt.replaceNotesLong',
              fallback: 'Replace the notes of {title} with the dictated text?',
              values: { title: command.storyTitle ?? 'this todo' },
            });
      }
      break;
    default:
      break;
  }
  return `${display.summary.replace(/[.!?]+$/, '')}?`;
}

function questionPending(
  resolution: Extract<VoiceSemanticResolution, { kind: 'question' }>,
): VoicePendingMissingSlot | null {
  switch (resolution.pendingSlot.operation) {
    case 'todo.create':
      return resolution.intent.kind === 'create-todo'
        ? {
            kind: 'missing-slot',
            operation: 'todo.create',
            slot: 'title',
            intent: resolution.intent,
            selection: resolution.selection,
          }
        : null;
    case 'todo.move':
      return resolution.target && resolution.intent.kind === 'move-todo'
        ? {
            kind: 'missing-slot',
            operation: 'todo.move',
            slot: 'destination',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
    case 'todo.assign':
      return resolution.target && resolution.intent.kind === 'assign-todo'
        ? {
            kind: 'missing-slot',
            operation: 'todo.assign',
            slot: 'assignee',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
    case 'todo.update_title':
      return resolution.target && resolution.intent.kind === 'update-todo-title'
        ? {
            kind: 'missing-slot',
            operation: 'todo.update_title',
            slot: 'title',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
    case 'todo.append_notes':
      return resolution.target && resolution.intent.kind === 'append-todo-notes'
        ? {
            kind: 'missing-slot',
            operation: 'todo.append_notes',
            slot: 'notes',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
    case 'todo.replace_notes':
      return resolution.target && resolution.intent.kind === 'replace-todo-notes'
        ? {
            kind: 'missing-slot',
            operation: 'todo.replace_notes',
            slot: 'notes',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
    case 'todo.add_tag':
      return resolution.target && resolution.intent.kind === 'add-todo-tag'
        ? {
            kind: 'missing-slot',
            operation: 'todo.add_tag',
            slot: 'tag',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
    case 'todo.remove_tag':
      return resolution.target && resolution.intent.kind === 'remove-todo-tag'
        ? {
            kind: 'missing-slot',
            operation: 'todo.remove_tag',
            slot: 'tag',
            intent: resolution.intent,
            selection: resolution.selection,
            target: resolution.target,
          }
        : null;
  }
}

function filledSlotIntent(
  pending: VoicePendingMissingSlot,
  dialogue: VoiceProvideSlotDialogueIntent,
): VoiceSemanticIntent | null {
  if (dialogue.operation !== pending.operation || dialogue.slot !== pending.slot) return null;
  switch (pending.operation) {
    case 'todo.create':
      return dialogue.operation === 'todo.create'
        ? { ...pending.intent, title: dialogue.value }
        : null;
    case 'todo.move':
      return dialogue.operation === 'todo.move'
        ? { ...pending.intent, destination: dialogue.value }
        : null;
    case 'todo.assign':
      return dialogue.operation === 'todo.assign'
        ? { ...pending.intent, assignee: dialogue.value }
        : null;
    case 'todo.update_title':
      return dialogue.operation === 'todo.update_title'
        ? { ...pending.intent, title: dialogue.value }
        : null;
    case 'todo.append_notes':
    case 'todo.replace_notes':
      return (dialogue.operation === 'todo.append_notes' || dialogue.operation === 'todo.replace_notes')
        ? { ...pending.intent, notes: dialogue.value }
        : null;
    case 'todo.add_tag':
    case 'todo.remove_tag':
      return (dialogue.operation === 'todo.add_tag' || dialogue.operation === 'todo.remove_tag')
        ? { ...pending.intent, tag: dialogue.value }
        : null;
  }
}

function correctedAuthoredIntent(
  intent: VoiceSemanticIntent,
  correction: Extract<VoiceCorrectionDialogueIntent, { kind: 'correct-value' }>,
): VoiceSemanticIntent | null {
  if (voiceSemanticOperation(intent) !== correction.operation) return null;
  if (correction.slot === 'title') {
    if (intent.kind === 'create-todo' || intent.kind === 'update-todo-title') {
      return { ...intent, title: correction.value };
    }
    return null;
  }
  if (intent.kind === 'append-todo-notes' || intent.kind === 'replace-todo-notes') {
    return { ...intent, notes: correction.value };
  }
  return null;
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
  let speaking = false;
  let taskModality: 'voice' | 'typed' | null = null;
  let dialogueTurns = 0;
  let retainedTodoChoices: readonly VoiceTodoClarificationChoice[] = Object.freeze([]);

  const emit = (
    phase: VoiceAgentPhase,
    status: VoiceAgentMessage,
    confirmation: VoiceAgentView['confirmation'] = null,
    clarification: VoiceAgentView['clarification'] = null,
  ) => {
    view = Object.freeze({ phase, status, confirmation, clarification });
    options.onView(view);
  };

  const owns = (owner: number, operation: AbortController): boolean =>
    !closed
    && operationOwner === owner
    && operationController === operation
    && !operation.signal.aborted;

  const contextIsCurrent = (): boolean => !isCommandFailure(getActiveVoiceCommandContext(options));

  const cancelOwnedOperation = () => {
    operationOwner += 1;
    operationController?.abort();
    operationController = null;
    speaking = false;
  };

  const monitorContext = (
    owner: number,
    operation: AbortController,
  ): ReturnType<typeof globalThis.setInterval> =>
    globalThis.setInterval(() => {
      if (!owns(owner, operation) || contextIsCurrent()) return;
      cancelOwnedOperation();
      reviewed = null;
      session.clearActiveTodo();
      fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
    }, 100);

  const fail = (status: VoiceAgentMessage) => {
    emit('error', status);
  };

  const restoreView = (snapshot: VoiceAgentView) => {
    emit(snapshot.phase, snapshot.status, snapshot.confirmation, snapshot.clarification);
  };

  const speakOwned = async (
    text: string,
    owner: number,
    operation: AbortController,
  ): Promise<boolean> => {
    const speechOutput = options.speechOutput;
    if (!speechOutput || !text.trim() || !owns(owner, operation)) return false;
    try {
      const status = await speechOutput.status({ signal: operation.signal });
      if (!owns(owner, operation) || status.state !== 'ready') return false;
      speaking = true;
      await speechOutput.speak({
        text: text.slice(0, 600),
        language: 'en-US',
        signal: operation.signal,
      });
      if (!owns(owner, operation)) return false;
      speaking = false;
      return true;
    } catch {
      if (owns(owner, operation)) speaking = false;
      return false;
    }
  };

  const listenOwned = async (
    owner: number,
    operation: AbortController,
    automatic: boolean,
    interpret: (transcript: string, owner: number, operation: AbortController) => Promise<void>,
  ): Promise<void> => {
    if (!owns(owner, operation)) return;
    const previous = view;
    if (automatic) {
      try {
        const status = await options.speechInput.status({ signal: operation.signal });
        if (!owns(owner, operation) || status.state !== 'ready') return;
      } catch {
        return;
      }
    }
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
      await interpret(result.transcript, owner, operation);
    } catch (error) {
      if (!owns(owner, operation)) return;
      if (
        automatic
        && error instanceof SpeechInputError
        && ['no_speech', 'timeout', 'cancelled', 'recognition_failed'].includes(error.code)
      ) {
        restoreView(previous);
        return;
      }
      if (error instanceof SpeechInputError && error.code === 'cancelled') {
        emit('ready', speechFailureMessage(error));
        return;
      }
      fail(error instanceof SpeechInputError
        ? speechFailureMessage(error)
        : message('voice.agent.speechFailed', 'On-device speech recognition failed.'));
    }
  };

  let interpretTranscript: (
    transcript: string,
    owner: number,
    operation: AbortController,
  ) => Promise<void>;

  const speakThenMaybeListen = async (
    speechText: string,
    responseRequired: boolean,
    terminal: boolean,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const spoken = await speakOwned(speechText, owner, operation);
    if (!spoken || !owns(owner, operation) || taskModality !== 'voice') {
      if (terminal) taskModality = null;
      return;
    }
    if (responseRequired || (terminal && session.getState().continuationEnabled)) {
      await listenOwned(owner, operation, true, interpretTranscript);
    }
    if (terminal && owns(owner, operation) && !session.getState().pending && !reviewed) {
      taskModality = null;
    }
  };

  const finishTask = () => {
    reviewed = null;
    dialogueTurns = 0;
    retainedTodoChoices = Object.freeze([]);
    session.clearPendingInteraction();
    if (!session.getState().continuationEnabled) session.reset();
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

    const success = { key: 'voice.status.done', fallback: 'Done.' };
    session.setLastInteraction({ kind: 'success', message: success });
    finishTask();
    emit('success', success);
    await speakThenMaybeListen(renderVoiceMessage(success), false, true, owner, operation);
  };

  const completeInformation = async (
    resolution: Extract<VoiceSemanticResolution, { kind: 'information' }>,
    owner: number,
    operation: AbortController,
  ) => {
    session.setLastInteraction(resolution.interaction);
    const speechText = renderVoiceMessage(resolution.interaction.speech ?? resolution.interaction.message);
    finishTask();
    emit('success', resolution.interaction.message);
    await speakThenMaybeListen(speechText, false, true, owner, operation);
  };

  const exceedDialogueLimit = async (owner: number, operation: AbortController): Promise<boolean> => {
    dialogueTurns += 1;
    if (dialogueTurns <= MAX_DIALOGUE_TURNS) return false;
    finishTask();
    const status = message(
      'voice.dialogue.turnLimit',
      'I could not finish that safely. The task was cancelled.',
    );
    emit('error', status);
    await speakThenMaybeListen(renderVoiceMessage(status), false, true, owner, operation);
    return true;
  };

  const stageResolved = async (
    command: ResolvedCommand,
    canonicalTranscript: string,
    semanticIntent: VoiceSemanticIntent | null,
    semanticSelection: VoiceSemanticSelection,
    originalTodoChoices: readonly VoiceTodoClarificationChoice[],
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
      originalTodoChoices: Object.freeze([...originalTodoChoices]),
    });
    const display = formatResolvedCommand(command);
    const interaction = {
      kind: 'confirmation' as const,
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
    };
    session.setPendingInteraction({
      kind: 'confirmation',
      operation: semanticIntent
        ? voiceSemanticOperation(semanticIntent)
        : command.ir.intent === 'open_todo'
          ? 'todo.open'
          : command.ir.intent.replace('todos.', 'todo.') as ReturnType<typeof voiceSemanticOperation>,
    });
    session.setLastInteraction(interaction);
    emit(
      'confirmation',
      message('voice.agent.confirmation', 'Confirmation required'),
      Object.freeze({
        summary: display.summary,
        confirmLabel: display.confirmLabel,
        danger: command.danger,
      }),
    );
    if (await exceedDialogueLimit(owner, operation)) return;
    await speakThenMaybeListen(confirmationSpeech(command), true, false, owner, operation);
  };

  const applySemanticResolution = async (
    resolution: VoiceSemanticResolution,
    intent: VoiceSemanticIntent,
    transcript: string,
    originalTodoChoices: readonly VoiceTodoClarificationChoice[],
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
          originalTodoChoices,
          owner,
          operation,
        );
        return;
      case 'question': {
        const pending = questionPending(resolution);
        if (!pending) {
          fail(message('voice.agent.aiFailed', 'On-device interpretation failed.'));
          return;
        }
        if (resolution.target) session.setActiveTodo(resolution.target);
        retainedTodoChoices = Object.freeze([...originalTodoChoices]);
        session.setPendingInteraction(pending);
        session.setLastInteraction(resolution.interaction);
        emit('question', resolution.interaction.message);
        if (await exceedDialogueLimit(owner, operation)) return;
        await speakThenMaybeListen(
          renderVoiceMessage(resolution.interaction.speech ?? resolution.interaction.message),
          true,
          false,
          owner,
          operation,
        );
        return;
      }
      case 'clarification':
        if (originalTodoChoices.length > 0) {
          retainedTodoChoices = Object.freeze([...originalTodoChoices]);
        }
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
        if (await exceedDialogueLimit(owner, operation)) return;
        await speakThenMaybeListen(
          renderVoiceMessage(resolution.interaction.speech ?? resolution.interaction.message),
          true,
          false,
          owner,
          operation,
        );
        return;
      case 'information':
        if (resolution.target) session.setActiveTodo(resolution.target);
        await completeInformation(resolution, owner, operation);
        return;
    }
  };

  const resolveAndApplySemantic = async (
    intent: VoiceSemanticIntent,
    selection: VoiceSemanticSelection,
    transcript: string,
    originalTodoChoices: readonly VoiceTodoClarificationChoice[],
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const resolved = await resolveVoiceSemanticCommand(
      intent,
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
    await applySemanticResolution(
      resolved.value,
      intent,
      transcript,
      originalTodoChoices,
      owner,
      operation,
    );
  };

  const confirmationView = (): VoiceAgentView['confirmation'] => reviewed
    ? Object.freeze({
        summary: formatResolvedCommand(reviewed.command).summary,
        confirmLabel: formatResolvedCommand(reviewed.command).confirmLabel,
        danger: reviewed.command.danger,
      })
    : null;

  const showInvalidPendingResponse = () => {
    const pending = session.getState().pending;
    const status = message(
      'voice.dialogue.invalidResponse',
      'That response does not match the current question.',
    );
    if (pending?.kind === 'confirmation') {
      emit('confirmation', status, confirmationView());
      return;
    }
    if (pending?.kind === 'clarification') {
      const interaction = session.getState().lastInteraction;
      emit(
        'question',
        status,
        null,
        interaction?.kind === 'clarification'
          ? Object.freeze({ options: interaction.options })
          : null,
      );
      return;
    }
    if (pending?.kind === 'missing-slot') {
      emit('question', status);
      return;
    }
    fail(status);
  };

  const retryClarification = async (
    pending: VoicePendingClarification,
    status: VoiceMessageDescriptor,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const interaction = session.getState().lastInteraction;
    emit(
      'question',
      status,
      null,
      interaction?.kind === 'clarification'
        ? Object.freeze({ options: interaction.options })
        : null,
    );
    if (await exceedDialogueLimit(owner, operation)) return;
    await speakThenMaybeListen(renderVoiceMessage(status), true, false, owner, operation);
    if (owns(owner, operation) && session.getState().pending !== pending) return;
  };

  const selectPendingClarification = async (
    pending: VoicePendingClarification,
    result: VoiceDialogueChoiceResolution,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    if (result.kind === 'no-match') {
      await retryClarification(
        pending,
        message('voice.dialogue.choiceNoMatch', "I couldn't match that to one of those choices."),
        owner,
        operation,
      );
      return;
    }
    if (result.kind === 'ambiguous') {
      await retryClarification(
        pending,
        message(
          'voice.dialogue.choiceAmbiguous',
          'That still matches more than one choice. Please be more specific.',
        ),
        owner,
        operation,
      );
      return;
    }
    const originalTodoChoices = pending.choices.filter(
      (choice): choice is VoiceTodoClarificationChoice => choice.kind === 'todo',
    );
    if (originalTodoChoices.length > 0) {
      retainedTodoChoices = Object.freeze([...originalTodoChoices]);
    }
    session.clearPendingInteraction();
    await resolveAndApplySemantic(
      pending.intent,
      result.selection,
      '',
      retainedTodoChoices,
      owner,
      operation,
    );
  };

  const confirmReviewed = async (
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const pendingReview = reviewed;
    if (!pendingReview) return;
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
  };

  const finishCancelled = async (
    declined: boolean,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    reviewed = null;
    retainedTodoChoices = Object.freeze([]);
    session.clearPendingInteraction();
    const status = declined
      ? { key: 'voice.dialogue.declined', fallback: 'Okay. I did not make that change.' }
      : { key: 'voice.status.cancelled', fallback: 'Cancelled.' };
    session.setLastInteraction({ kind: 'information', message: status });
    dialogueTurns = 0;
    if (!session.getState().continuationEnabled) session.reset();
    emit('success', status);
    await speakThenMaybeListen(renderVoiceMessage(status), false, true, owner, operation);
  };

  const handleCorrection = async (
    dialogue: VoiceCorrectionDialogueIntent,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const pendingReview = reviewed;
    if (!pendingReview?.semanticIntent) {
      fail(message('voice.dialogue.invalidResponse', 'That response does not match the current question.'));
      return;
    }
    if (dialogue.kind === 'correct-choice') {
      const result = resolveRetainedTodoSelector(
        pendingReview.originalTodoChoices,
        pendingReview.semanticSelection,
        dialogue.selector,
      );
      if (result.kind !== 'selected') {
        const status = result.kind === 'ambiguous'
          ? message('voice.dialogue.correctionAmbiguous', 'That still matches more than one offered todo.')
          : message(
              'voice.dialogue.correctionNoMatch',
              "I couldn't match that to one of the original choices.",
            );
        emit('confirmation', status, confirmationView());
        if (await exceedDialogueLimit(owner, operation)) return;
        await speakThenMaybeListen(renderVoiceMessage(status), true, false, owner, operation);
        return;
      }
      reviewed = null;
      session.clearPendingInteraction();
      await resolveAndApplySemantic(
        pendingReview.semanticIntent,
        result.selection,
        '',
        pendingReview.originalTodoChoices,
        owner,
        operation,
      );
      return;
    }
    const corrected = correctedAuthoredIntent(pendingReview.semanticIntent, dialogue);
    if (!corrected) {
      await finishCancelled(true, owner, operation);
      return;
    }
    reviewed = null;
    session.clearPendingInteraction();
    await resolveAndApplySemantic(
      corrected,
      pendingReview.semanticSelection,
      '',
      pendingReview.originalTodoChoices,
      owner,
      operation,
    );
  };

  const handleDialogue = async (
    dialogue: VoiceDialogueIntent,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const pending = session.getState().pending;
    if (!pending) {
      fail(message('voice.dialogue.invalidResponse', 'That response does not match a pending question.'));
      return;
    }
    if (dialogue.kind === 'cancel') {
      await finishCancelled(false, owner, operation);
      return;
    }
    if (pending.kind === 'clarification') {
      if (dialogue.kind !== 'select-choice') {
        await retryClarification(
          pending,
          message(
            'voice.dialogue.choiceRequired',
            'Please choose one of the offered options or cancel.',
          ),
          owner,
          operation,
        );
        return;
      }
      await selectPendingClarification(
        pending,
        resolvePendingClarificationSelector(pending, dialogue.selector),
        owner,
        operation,
      );
      return;
    }
    if (pending.kind === 'missing-slot') {
      if (dialogue.kind !== 'provide-slot') {
        fail(message('voice.dialogue.invalidResponse', 'That response does not match the current question.'));
        return;
      }
      const intent = filledSlotIntent(pending, dialogue);
      if (!intent) {
        fail(message('voice.dialogue.invalidResponse', 'That response does not match the current question.'));
        return;
      }
      await resolveAndApplySemantic(
        intent,
        pending.selection,
        '',
        retainedTodoChoices,
        owner,
        operation,
      );
      return;
    }
    if (dialogue.kind === 'decline') {
      await finishCancelled(true, owner, operation);
      return;
    }
    if (dialogue.kind === 'confirm') {
      emit('processing', message('voice.agent.processing', 'Processing…'));
      await confirmReviewed(owner, operation);
      return;
    }
    if (dialogue.kind === 'correct-choice' || dialogue.kind === 'correct-value') {
      await handleCorrection(dialogue, owner, operation);
      return;
    }
    fail(message('voice.dialogue.invalidResponse', 'That response does not match the current confirmation.'));
  };

  interpretTranscript = async (
    transcript: string,
    owner: number,
    operation: AbortController,
  ): Promise<void> => {
    const normalized = transcript.trim();
    if (!normalized || !owns(owner, operation)) return;
    if (!contextIsCurrent()) {
      reviewed = null;
      session.clearActiveTodo();
      fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
      return;
    }
    const wasPending = session.getState().pending !== null;
    if (!wasPending) {
      dialogueTurns = 0;
      retainedTodoChoices = Object.freeze([]);
    }
    emit('processing', message('voice.agent.processing', 'Processing…'));
    const interpretation = await options.interpreter.interpret(normalized, {
      signal: operation.signal,
      conversation: pendingInterpreterContext(session),
    });
    if (!owns(owner, operation)) return;
    if (interpretation.kind === 'unsupported') {
      if (wasPending) showInvalidPendingResponse();
      else fail(literal(localizeCommandFailure(interpretation.failure)));
      return;
    }
    if (interpretation.kind === 'dialogue') {
      await handleDialogue(interpretation.intent, owner, operation);
      return;
    }
    if (wasPending) {
      showInvalidPendingResponse();
      return;
    }
    if (interpretation.kind === 'candidate') {
      const resolved = await parseAndResolveVoiceCommand(
        interpretation.command,
        options,
        operation.signal,
      );
      if (!owns(owner, operation)) return;
      if (isCommandFailure(resolved)) {
        fail(literal(localizeCommandFailure(resolved)));
        return;
      }
      await stageResolved(resolved.value, interpretation.command, null, {}, [], owner, operation);
      return;
    }
    await resolveAndApplySemantic(interpretation.intent, {}, normalized, [], owner, operation);
  };

  const runOwned = async (
    work: (owner: number, operation: AbortController) => Promise<void>,
  ): Promise<void> => {
    if (closed || operationController) return;
    if (!contextIsCurrent()) {
      reviewed = null;
      session.clearActiveTodo();
      fail(message('voice.errors.staleContext', 'The board changed before the command could run.'));
      return;
    }
    const operation = new AbortController();
    const owner = ++operationOwner;
    operationController = operation;
    const contextMonitor = monitorContext(owner, operation);
    try {
      await work(owner, operation);
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
      if (closed) return;
      taskModality = 'voice';
      if (speaking) {
        cancelOwnedOperation();
        await options.speechOutput?.stop().catch(() => undefined);
      }
      await runOwned((owner, operation) =>
        listenOwned(owner, operation, false, interpretTranscript));
    },
    async submitTranscript(transcript) {
      taskModality = 'typed';
      await runOwned((owner, operation) => interpretTranscript(transcript, owner, operation));
    },
    stopListening() {
      if (closed || !operationController) return;
      cancelOwnedOperation();
      emit('ready', message('voice.agent.stopped', 'Listening stopped.'));
    },
    async confirm() {
      if (closed || !reviewed) return;
      if (speaking) {
        cancelOwnedOperation();
        await options.speechOutput?.stop().catch(() => undefined);
      }
      if (operationController) return;
      await runOwned(async (owner, operation) => {
        emit('processing', message('voice.agent.processing', 'Processing…'));
        await confirmReviewed(owner, operation);
      });
    },
    cancelConfirmation() {
      if (closed || !reviewed) return;
      const wasSpeaking = speaking;
      cancelOwnedOperation();
      void (async () => {
        if (wasSpeaking) await options.speechOutput?.stop().catch(() => undefined);
        await runOwned((owner, operation) => finishCancelled(false, owner, operation));
      })();
    },
    async chooseClarification(index) {
      const pending = session.getState().pending;
      if (
        closed
        || pending?.kind !== 'clarification'
        || !Number.isInteger(index)
        || index < 0
        || index >= pending.choices.length
      ) return;
      if (speaking) {
        cancelOwnedOperation();
        await options.speechOutput?.stop().catch(() => undefined);
      }
      if (operationController) return;
      await runOwned((owner, operation) => selectPendingClarification(
        pending,
        { kind: 'selected', index, selection: (() => {
          const choice = pending.choices[index];
          const synthetic = choice.kind === 'todo'
            ? { kind: 'local-id' as const, localId: choice.reference.localId }
            : { kind: 'ordinal' as const, index: index + 1 };
          const result = resolvePendingClarificationSelector(pending, synthetic);
          return result.kind === 'selected' ? result.selection : pending.selection;
        })() },
        owner,
        operation,
      ));
    },
    cancelClarification() {
      if (closed || session.getState().pending?.kind !== 'clarification') return;
      const wasSpeaking = speaking;
      cancelOwnedOperation();
      void (async () => {
        if (wasSpeaking) await options.speechOutput?.stop().catch(() => undefined);
        await runOwned((owner, operation) => finishCancelled(false, owner, operation));
      })();
    },
    setContinuationEnabled(enabled) {
      if (closed) return;
      session.setContinuationEnabled(!!enabled);
      if (!enabled && !operationController && !session.getState().pending && !reviewed) {
        session.reset();
        taskModality = null;
      }
      if (!session.getState().pending && !reviewed) {
        emit('ready', message('voice.agent.ready', 'Ready'));
      }
    },
    invalidate(invalidationOptions = {}) {
      if (closed) return;
      cancelOwnedOperation();
      taskModality = null;
      void options.speechOutput?.invalidate().catch(() => undefined);
      if (invalidationOptions.clearConversation) {
        reviewed = null;
        dialogueTurns = 0;
        retainedTodoChoices = Object.freeze([]);
        session.reset();
      }
      if (!session.getState().pending && !reviewed) {
        emit('ready', message('voice.agent.ready', 'Ready'));
      }
    },
    close() {
      if (closed) return;
      closed = true;
      cancelOwnedOperation();
      taskModality = null;
      reviewed = null;
      retainedTodoChoices = Object.freeze([]);
      void options.speechOutput?.invalidate().catch(() => undefined);
      session.dispose();
      emit('closed', message('voice.agent.closed', 'VoiceFlow closed.'));
    },
  };

  options.onView(view);
  return Object.freeze(controller);
}
