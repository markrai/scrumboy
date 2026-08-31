import {
  createEmptyVoiceConversationState,
  type VoiceConversationState,
  type VoicePendingInteraction,
  type VoiceProjectReference,
  type VoiceTodoReference,
} from './conversation-state.js';
import type {
  VoiceInteractionMessage,
  VoiceSemanticInteraction,
} from './semantic-interaction.js';

export interface VoiceConversationSession {
  getState(): VoiceConversationState;
  setActiveProject(project: VoiceProjectReference): void;
  setActiveTodo(todo: VoiceTodoReference): void;
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

function sameTodo(
  left: VoiceTodoReference | null,
  right: VoiceTodoReference,
): boolean {
  return left?.projectId === right.projectId
    && left.projectSlug === right.projectSlug
    && left.localId === right.localId;
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
  switch (interaction.kind) {
    case 'question':
    case 'clarification':
      return Object.freeze({ ...interaction, message });
    case 'confirmation':
      return Object.freeze({
        ...interaction,
        message,
        confirmLabel: freezeMessage(interaction.confirmLabel),
      });
    case 'refusal':
    case 'error':
      return Object.freeze({ ...interaction, message });
    case 'unsupported-residue':
      return Object.freeze({
        ...interaction,
        message,
        residue: Object.freeze([...interaction.residue]),
      });
    case 'information':
    case 'success':
      return Object.freeze({ ...interaction, message });
  }
}

function freezePending(pending: VoicePendingInteraction): VoicePendingInteraction {
  return Object.freeze({
    kind: pending.kind,
    action: pending.action,
    slot: pending.slot,
    target: freezeTodo(pending.target),
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
      const todoChanged = !sameTodo(state.activeTodo, todo);
      const activeTodo = freezeTodo(todo);
      state = freezeState({
        ...state,
        activeProject: freezeProject(todo),
        activeTodo,
        pending: todoChanged ? null : state.pending,
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
