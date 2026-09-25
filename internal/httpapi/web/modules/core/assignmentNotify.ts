/** Mute assignment toast+sound via localStorage key `scrumboy_assignment_notify_muted` = `'1'`. */

import { t } from '../i18n/index.js';
import { getAppRuntime } from '../platform/runtime.js';

const MUTE_KEY = "scrumboy_assignment_notify_muted";

let audio: HTMLAudioElement | null = null;

export type DesktopNotificationStatusKind = "unsupported" | "granted" | "denied" | "default";

function getNotifyAudio(): HTMLAudioElement {
  if (!audio) {
    // iOS Safari does not decode Ogg/Vorbis; MP3 first so mobile gets a playable source. Desktop keeps Ogg as fallback.
    audio = document.createElement("audio");
    audio.preload = "auto";
    const mp3 = document.createElement("source");
    mp3.src = "/static/sounds/notify.mp3";
    mp3.type = "audio/mpeg";
    const ogg = document.createElement("source");
    ogg.src = "/static/sounds/notify.ogg";
    ogg.type = "audio/ogg";
    audio.appendChild(mp3);
    audio.appendChild(ogg);
    audio.load();
  }
  return audio;
}

export function playAssignmentSound(): void {
  try {
    if (typeof localStorage !== "undefined" && localStorage.getItem(MUTE_KEY) === "1") {
      return;
    }
  } catch {
    // ignore storage errors
  }
  const a = getNotifyAudio();
  a.currentTime = 0;
  void a.play().catch(() => {});
}

/** OS Notification API (optional; permission requested from Settings → Customization). */
export function showAssignmentDesktopNotification(title: string): void {
  if (!getAppRuntime().supportsWebPush()) {
    return;
  }
  if (typeof Notification === "undefined") {
    return;
  }
  if (Notification.permission !== "granted") {
    return;
  }
  try {
    new Notification(t("notifications.desktop.title"), { body: title || t("realtime.todoFallback") });
  } catch {
    // ignore
  }
}

export async function requestDesktopNotificationPermission(): Promise<NotificationPermission> {
  if (!getAppRuntime().supportsWebPush()) {
    return "denied";
  }
  if (typeof Notification === "undefined") {
    return "denied";
  }
  try {
    return await Notification.requestPermission();
  } catch {
    return "denied";
  }
}

export function getDesktopNotificationStatusKind(): DesktopNotificationStatusKind {
  if (!getAppRuntime().supportsWebPush()) {
    return "unsupported";
  }
  if (typeof Notification === "undefined") {
    return "unsupported";
  }
  switch (Notification.permission) {
    case "granted":
      return "granted";
    case "denied":
      return "denied";
    default:
      return "default";
  }
}

export function getDesktopNotificationStatusDescription(): string {
  switch (getDesktopNotificationStatusKind()) {
    case "unsupported":
      return t("settings.customization.notifications.status.unsupported");
    case "granted":
      return t("settings.customization.notifications.status.granted");
    case "denied":
      return t("settings.customization.notifications.status.denied");
    default:
      return t("settings.customization.notifications.status.default");
  }
}
