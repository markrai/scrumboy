import { startMobileBootstrap } from './bootstrap-core.js';
import { installNativeLifecycle } from './native-lifecycle.js';
import { nativeOIDC } from './native-oidc.js';

void Promise.all([
  installNativeLifecycle(),
  nativeOIDC.installURLCapture(),
]).then(() => startMobileBootstrap());
