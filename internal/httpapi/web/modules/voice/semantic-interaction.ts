export type VoiceInteractionMessageValue = string | number;

export type VoiceInteractionMessage = Readonly<{
  key: string;
  fallback: string;
  values?: Readonly<Record<string, VoiceInteractionMessageValue>>;
}>;

export type VoiceInteractionReason =
  | 'missing_information'
  | 'ambiguous'
  | 'unsupported'
  | 'unsupported_residue'
  | 'unauthorized'
  | 'invalid'
  | 'stale_context'
  | 'execution_failed';

export type VoiceSemanticInteraction =
  | Readonly<{
      kind: 'information';
      message: VoiceInteractionMessage;
    }>
  | Readonly<{
      kind: 'question';
      message: VoiceInteractionMessage;
      response: 'free-text' | 'choice';
    }>
  | Readonly<{
      kind: 'clarification';
      message: VoiceInteractionMessage;
      response: 'choice';
    }>
  | Readonly<{
      kind: 'confirmation';
      message: VoiceInteractionMessage;
      confirmLabel: VoiceInteractionMessage;
      danger: boolean;
    }>
  | Readonly<{
      kind: 'refusal';
      message: VoiceInteractionMessage;
      reason: VoiceInteractionReason;
    }>
  | Readonly<{
      kind: 'unsupported-residue';
      message: VoiceInteractionMessage;
      residue: readonly string[];
    }>
  | Readonly<{
      kind: 'success';
      message: VoiceInteractionMessage;
    }>
  | Readonly<{
      kind: 'error';
      message: VoiceInteractionMessage;
      reason: VoiceInteractionReason;
    }>;
