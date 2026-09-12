import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.markrai.scrumboy',
  appName: 'Scrumboy',
  webDir: 'www',
  android: {
    loggingBehavior: 'none',
  },
  plugins: {
    SystemBars: {
      hidden: true,
      insetsHandling: 'css',
    },
  },
};

export default config;
