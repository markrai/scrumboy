import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const root = resolve(import.meta.dirname, '../../../..');
const manifest = readFileSync(resolve(root, 'mobile/capacitor/android/app/src/main/AndroidManifest.xml'), 'utf8');
const fullBackup = readFileSync(resolve(root, 'mobile/capacitor/android/app/src/main/res/xml/backup_rules.xml'), 'utf8');
const extraction = readFileSync(resolve(root, 'mobile/capacitor/android/app/src/main/res/xml/data_extraction_rules.xml'), 'utf8');
const packageJSON = JSON.parse(readFileSync(resolve(root, 'mobile/capacitor/package.json'), 'utf8'));

describe('C4 Android OIDC boundary policy', () => {
  it('registers only the exact custom callback on the existing singleTask launcher', () => {
    expect(manifest).toContain('android:launchMode="singleTask"');
    expect(manifest).toContain('android:name="android.intent.action.VIEW"');
    expect(manifest).toContain('android:name="android.intent.category.BROWSABLE"');
    expect(manifest).toContain('android:scheme="com.markrai.scrumboy"');
    expect(manifest).toContain('android:host="oidc"');
    expect(manifest).toContain('android:path="/callback"');
    expect(manifest).not.toContain('android:autoVerify');
    expect(manifest).not.toMatch(/android:scheme="https?"/);
  });

  it('disables backup and excludes both proof/session preference files from every backup path', () => {
    expect(manifest).toContain('android:allowBackup="false"');
    expect(manifest).toContain('android:dataExtractionRules="@xml/data_extraction_rules"');
    expect(manifest).toContain('android:fullBackupContent="@xml/backup_rules"');
    for (const rules of [fullBackup, extraction]) {
      expect(rules).toContain('path="CapacitorStorage.xml"');
      expect(rules).toContain('path="scrumboy_transport_cookies_v1.xml"');
    }
    expect(extraction).toContain('<cloud-backup');
    expect(extraction).toContain('<device-transfer>');
  });

  it('pins the compatible Browser plugin without adding an authentication redesign', () => {
    expect(packageJSON.dependencies['@capacitor/browser']).toBe('8.0.4');
    expect(packageJSON.dependencies['@capacitor/preferences']).toBe('8.0.1');
    expect(packageJSON.dependencies).not.toHaveProperty('@capacitor/http');
  });
});
