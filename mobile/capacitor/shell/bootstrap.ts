import { startMobileBootstrap } from './bootstrap-core.js';
import { installNativeLifecycle } from './native-lifecycle.js';
import { nativeOIDC } from './native-oidc.js';
import { createLocalTextGenerationComposition } from './local-text-generation-capability.js';
import {
  createClientCapabilityRegistry,
  type AppCapabilityMap,
} from '../../../internal/httpapi/web/modules/platform/client-capabilities.js';
import { LOCAL_TEXT_GENERATION_CAPABILITY } from '../../../internal/httpapi/web/modules/platform/local-text-generation.js';
import { SPEECH_INPUT_CAPABILITY } from '../../../internal/httpapi/web/modules/platform/speech-input.js';
import { createSpeechInputComposition } from './speech-input-capability.js';
import { SPEECH_OUTPUT_CAPABILITY } from '../../../internal/httpapi/web/modules/platform/speech-output.js';
import { createSpeechOutputComposition } from './speech-output-capability.js';

const localTextGeneration = createLocalTextGenerationComposition();
const speechInput = createSpeechInputComposition();
const speechOutput = createSpeechOutputComposition();
const localTextGenerationCapability = localTextGeneration.registry.get(LOCAL_TEXT_GENERATION_CAPABILITY);
const speechInputCapability = speechInput.registry.get(SPEECH_INPUT_CAPABILITY);
const speechOutputCapability = speechOutput.registry.get(SPEECH_OUTPUT_CAPABILITY);
if (!localTextGenerationCapability || !speechInputCapability || !speechOutputCapability) {
  throw new Error('Native capability composition is incomplete.');
}
const capabilities = createClientCapabilityRegistry<AppCapabilityMap>({
  [LOCAL_TEXT_GENERATION_CAPABILITY]: localTextGenerationCapability,
  [SPEECH_INPUT_CAPABILITY]: speechInputCapability,
  [SPEECH_OUTPUT_CAPABILITY]: speechOutputCapability,
});

void Promise.all([
  installNativeLifecycle(),
  nativeOIDC.installURLCapture(),
]).then(() => startMobileBootstrap({
  capabilities,
  invalidateCapabilities: async () => {
    await Promise.all([
      localTextGeneration.invalidate(),
      speechInput.invalidate(),
      speechOutput.invalidate(),
    ]);
  },
}));
