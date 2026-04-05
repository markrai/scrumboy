/**
 * Debounced + cooldown-scoped resync after foreground resume (missed SSE events).
 */

import { invalidateBoard } from "../orchestration/board-refresh.js";
import {
  getAuthStatusAvailable,
  getSlug,
  getTag,
  getSearch,
  getSprintIdFromUrl,
  getUser,
} from "../state/selectors.js";
import { hydrateNotificationsForUser } from "./notifications.js";

const RESUME_DEBOUNCE_MS = 400;
const RESUME_COOLDOWN_MS = 15_000;

let resumeTimer: ReturnType<typeof setTimeout> | null = null;
let lastResumeResyncAt = 0;

export function scheduleResumeResync(reason: string): void {
  if (resumeTimer !== null) {
    clearTimeout(resumeTimer);
  }
  resumeTimer = setTimeout(() => {
    resumeTimer = null;
    void runResumeResync(reason);
  }, RESUME_DEBOUNCE_MS);
}

async function runResumeResync(reason: string): Promise<void> {
  const now = Date.now();
  if (now - lastResumeResyncAt < RESUME_COOLDOWN_MS) {
    if (typeof localStorage !== "undefined" && localStorage.getItem("scrumboy_debug_realtime") === "1") {
      console.log("[resume] skip resync (cooldown)", reason);
    }
    return;
  }
  lastResumeResyncAt = now;

  if (typeof localStorage !== "undefined" && localStorage.getItem("scrumboy_debug_realtime") === "1") {
    console.log("[resume] resync", reason);
  }

  const slug = getSlug();
  if (slug) {
    try {
      await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl());
    } catch (err) {
      console.warn("Resume board resync failed:", err);
    }
  }

  const u = getUser();
  if (getAuthStatusAvailable() && u) {
    hydrateNotificationsForUser(u.id);
  }
}
