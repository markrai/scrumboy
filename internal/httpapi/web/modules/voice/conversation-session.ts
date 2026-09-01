import {
  createEmptyVoiceConversationState,
  type VoiceConversationState,
  type VoiceClarificationChoice,
  type VoiceConversationSelection,
  type VoicePendingInteraction,
  type VoiceProjectReference,
  type VoiceTodoReference,
} from './conversation-state.js';
import type {
  VoiceInteractionMessage,
  VoiceSemanticInteraction,
} from './semantic-interaction.js';
import type {
  VoiceSemanticIntent,
  VoiceSemanticMemberReference,
  VoiceSemanticTagReference,
  VoiceSemanticTodoReference,
} from './semantic-intent.js';

export interface VoiceConversationSession {
  getState(): VoiceConversationState;
  setActiveProject(project: VoiceProjectReference): void;
  setActiveTodo(todo: VoiceTodoReference): void;
  clearActiveTodo(): void;
  setPendingInteraction(pending: VoicePendingInteraction): void;
  clearPendingInteraction(): void;
  setLastInteraction(interaction: VoiceSemanticInteraction): void;
  setContinuationEnabled(enabled: boolean): void;
  reset(): void;
  dispose(): void;
}

function sameProject(
  left: VoiceProjectReference | null,
  right: VoiceProjectReference,
): boolean {
  return left?.projectId === right.projectId && left.projectSlug === right.projectSlug;
}

function freezeProject(project: VoiceProjectReference): VoiceProjectReference {
  return Object.freeze({
    projectId: project.projectId,
    projectSlug: project.projectSlug,
  });
}

function freezeTodo(todo: VoiceTodoReference): VoiceTodoReference {
  return Object.freeze({
    kind: 'todo',
    projectId: todo.projectId,
    projectSlug: todo.projectSlug,
    localId: todo.localId,
  });
}

function freezeSemanticTodoReference(
  target: VoiceSemanticTodoReference,
): VoiceSemanticTodoReference {
  return Object.freeze({ ...target });
}

function freezeSemanticMemberReference(
  member: VoiceSemanticMemberReference,
): VoiceSemanticMemberReference {
  return Object.freeze({ ...member });
}

function freezeSemanticTagReference(
  tag: VoiceSemanticTagReference,
): VoiceSemanticTagReference {
  return Object.freeze({ ...tag });
}

function freezeSemanticIntent(intent: VoiceSemanticIntent): VoiceSemanticIntent {
  switch (intent.kind) {
    case 'create-todo':
      return Object.freeze({ ...intent });
    case 'open-todo':
    case 'delete-todo':
      return Object.freeze({ ...intent, target: freezeSemanticTodoReference(intent.target) });
    case 'move-todo':
      return Object.freeze({
        ...intent,
        target: freezeSemanticTodoReference(intent.target),
        destination: intent.destination ? Object.freeze({ ...intent.destination }) : null,
      });
    case 'assign-todo':
      return Object.freeze({
        ...intent,
        target: freezeSemanticTodoReference(intent.target),
        assignee: intent.assignee ? freezeSemanticMemberReference(intent.assignee) : null,
      });
    case 'unassign-todo':
      return Object.freeze({
        ...intent,
        target: freezeSemanticTodoReference(intent.target),
        ...(intent.assignee ? { assignee: freezeSemanticMemberReference(intent.assignee) } : {}),
      });
    case 'update-todo-title':
    case 'append-todo-notes':
    case 'replace-todo-notes':
    case 'inspect-todo':
      return Object.freeze({ ...intent, target: freezeSemanticTodoReference(intent.target) });
    case 'add-todo-tag':
    case 'remove-todo-tag':
      return Object.freeze({
        ...intent,
        target: freezeSemanticTodoReference(intent.target),
        tag: intent.tag ? freezeSemanticTagReference(intent.tag) : null,
      });
    case 'count-completed-todos':
      return Object.freeze({ ...intent, period: Object.freeze({ ...intent.period }) });
  }
}

function freezeMessage(message: VoiceInteractionMessage): VoiceInteractionMessage {
  const values = message.values ? Object.freeze({ ...message.values }) : undefined;
  return Object.freeze({
    key: message.key,
    fallback: message.fallback,
    ...(values ? { values } : {}),
  });
}

function freezeInteraction(interaction: VoiceSemanticInteraction): VoiceSemanticInteraction {
  const message = freezeMessage(interaction.message);
  const speech = interaction.speech ? freezeMessage(interaction.speech) : undefined;
  switch (interaction.kind) {
    case 'question':
      return Object.freeze({ ...interaction, message, ...(speech ? { speech } : {}) });
    case 'clarification':
      return Object.freeze({
        ...interaction,
        message,
        ...(speech ? { speech } : {}),
        options: Object.freeze(interaction.options.map((option) => Object.freeze({ ...option }))),
      });
    case 'confirmation':
      return Object.freeze({
        ...interaction,
        message,
        ...(speech ? { speech } : {}),
        confirmLabel: freezeMessage(interaction.confirmLabel),
      });
    case 'refusal':
    case 'error':
      return Object.freeze({ ...interaction, message, ...(speech ? { speech } : {}) });
    case 'unsupported-residue':
      return Object.freeze({
        ...interaction,
        message,
        ...(speech ? { speech } : {}),
        residue: Object.freeze([...interaction.residue]),
      });
    case 'information':
    case 'success':
      return Object.freeze({ ...interaction, message, ...(speech ? { speech } : {}) });
  }
}

function freezePending(pending: VoicePendingInteraction): VoicePendingInteraction {
  if (pending.kind === 'clarification') {
    return Object.freeze({
      kind: pending.kind,
      intent: freezeSemanticIntent(pending.intent),
      choices: Object.freeze(pending.choices.map(freezeClarificationChoice)),
      selection: freezeConversationSelection(pending.selection),
    });
  }
  if (pending.kind === 'confirmation') {
    return Object.freeze({ kind: pending.kind, operation: pending.operation });
  }
  return Object.freeze({
    kind: pending.kind,
    operation: pending.operation,
    slot: pending.slot,
    intent: freezeSemanticIntent(pending.intent),
    selection: freezeConversationSelection(pending.selection),
    ...('target' in pending ? { target: freezeTodo(pending.target) } : {}),
  }) as VoicePendingInteraction;
}

function freezeConversationSelection(
  selection: VoiceConversationSelection,
): VoiceConversationSelection {
  return Object.freeze({
    ...(selection.todo
      ? {
          todo: Object.freeze({
            selectedLocalId: selection.todo.selectedLocalId,
            allowedLocalIds: Object.freeze([...selection.todo.allowedLocalIds]),
          }),
        }
      : {}),
    ...(selection.member
      ? {
          member: Object.freeze({
            selectedUserId: selection.member.selectedUserId,
            allowedUserIds: Object.freeze([...selection.member.allowedUserIds]),
          }),
        }
      : {}),
    ...(selection.tag
      ? {
          tag: Object.freeze({
            selectedName: selection.tag.selectedName,
            allowedNames: Object.freeze([...selection.tag.allowedNames]),
          }),
        }
      : {}),
  });
}

function freezeClarificationChoice(choice: VoiceClarificationChoice): VoiceClarificationChoice {
  if (choice.kind === 'member' || choice.kind === 'tag') return Object.freeze({ ...choice });
  return Object.freeze({
    ...choice,
    reference: freezeTodo(choice.reference),
  });
}

function freezeState(state: VoiceConversationState): VoiceConversationState {
  return Object.freeze({ ...state });
}

export function createVoiceConversationSession(): VoiceConversationSession {
  let state = createEmptyVoiceConversationState();
  let disposed = false;

  const requireActive = (): void => {
    if (disposed) throw new Error('Voice conversation session is disposed.');
  };

  const session: VoiceConversationSession = {
    getState() {
      return state;
    },

    setActiveProject(project) {
      requireActive();
      const projectChanged = !sameProject(state.activeProject, project);
      state = freezeState({
        ...state,
        activeProject: freezeProject(project),
        activeTodo: projectChanged ? null : state.activeTodo,
        pending: projectChanged ? null : state.pending,
      });
    },

    setActiveTodo(todo) {
      requireActive();
      const projectChanged = !sameProject(state.activeProject, todo);
      const activeTodo = freezeTodo(todo);
      state = freezeState({
        ...state,
        activeProject: freezeProject(todo),
        activeTodo,
        pending: projectChanged ? null : state.pending,
      });
    },

    clearActiveTodo() {
      requireActive();
      state = freezeState({
        ...state,
        activeTodo: null,
        pending: null,
      });
    },

    setPendingInteraction(pending) {
      requireActive();
      state = freezeState({ ...state, pending: freezePending(pending) });
    },

    clearPendingInteraction() {
      requireActive();
      state = freezeState({ ...state, pending: null });
    },

    setLastInteraction(interaction) {
      requireActive();
      state = freezeState({
        ...state,
        lastInteraction: freezeInteraction(interaction),
      });
    },

    setContinuationEnabled(enabled) {
      requireActive();
      state = freezeState({ ...state, continuationEnabled: enabled });
    },

    reset() {
      requireActive();
      state = createEmptyVoiceConversationState();
    },

    dispose() {
      if (disposed) return;
      state = createEmptyVoiceConversationState();
      disposed = true;
    },
  };

  return Object.freeze(session);
}
