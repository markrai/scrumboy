import { startMobileBootstrap } from './bootstrap-core.js';
import { installNativeLifecycle } from './native-lifecycle.js';
import { nativeOIDC } from './native-oidc.js';
import { createLocalTextGenerationComposition } from './local-text-generation-capability.js';

const localTextGeneration = createLocalTextGenerationComposition();

void Promise.all([
  installNativeLifecycle(),
  nativeOIDC.installURLCapture(),
]).then(() => startMobileBootstrap({
  capabilities: localTextGeneration.registry,
  invalidateCapabilities: localTextGeneration.invalidate,
}));
