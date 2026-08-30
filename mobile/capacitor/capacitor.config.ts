import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.markrai.scrumboy',
  appName: 'Scrumboy',
  webDir: 'www',
  android: {
    loggingBehavior: 'none',
  },
};

export default config;
