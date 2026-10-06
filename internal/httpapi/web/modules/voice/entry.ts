import { voiceText } from './i18n.js';
import {
  selectVoiceFlowExperience,
  type VoiceFlowExperienceSelection,
} from './experience-selection.js';
import type { VoiceCommandOptions } from './command-resolution.js';

export type OpenVoiceFlowDependencies = Readonly<{
  selectExperience?: () => Promise<VoiceFlowExperienceSelection>;
}>;

export async function openVoiceFlow(
  options: VoiceCommandOptions,
  dependencies: OpenVoiceFlowDependencies = {},
): Promise<void> {
  const selection = await (dependencies.selectExperience?.() ?? selectVoiceFlowExperience());
  const openBasic = async () => {
    const { closeVoiceAgent } = await import('./agent.js');
    closeVoiceAgent();
    const { openVoiceCommandDialog } = await import('./flow.js');
    openVoiceCommandDialog(options);
  };

  if (selection.kind === 'legacy-deterministic') {
    await openBasic();
    return;
  }

  const { openVoiceAgent, openVoiceAgentNotReady } = await import('./agent.js');
  if (selection.kind === 'enhanced-agent') {
    openVoiceAgent({
      ...options,
      localTextGeneration: selection.localTextGeneration,
      speechInput: selection.speechInput,
      speechOutput: selection.speechOutput,
      onUseBasic: () => void openBasic(),
    });
    return;
  }

  const status = selection.reason === 'speech'
    ? voiceText(
        'voice.agent.speechNotReady',
        'On-device speech input is temporarily unavailable.',
      )
    : selection.reason === 'ai'
      ? voiceText(
          'voice.agent.aiNotReady',
          'On-device interpretation is preparing or needs attention.',
        )
      : voiceText(
          'voice.agent.notReady',
          'AI VoiceFlow is temporarily unavailable.',
        );
  openVoiceAgentNotReady({ status, onUseBasic: () => void openBasic() });
}
