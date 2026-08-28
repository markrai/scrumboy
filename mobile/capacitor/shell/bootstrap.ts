import { startMobileBootstrap } from './bootstrap-core.js';
import { installNativeLifecycle } from './native-lifecycle.js';

void installNativeLifecycle().then(() => startMobileBootstrap());
