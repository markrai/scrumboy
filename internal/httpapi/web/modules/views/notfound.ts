import { app } from '../dom/elements.js';
import { hasI18nKey, t } from '../i18n/index.js';
import { escapeHTML } from '../utils.js';
import { appHomePath } from '../app-home.js';

function notFoundText(key: string, fallback: string): string {
  return hasI18nKey(key) ? t(key) : fallback;
}

export function renderNotFound(): void {
  app.innerHTML = `
    <div class="page">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
        <button class="btn" id="homeBtn" data-i18n-text="notFound.home">${escapeHTML(notFoundText("notFound.home", "Home"))}</button>
      </div>
      <div class="empty">
        <div class="empty__title" data-i18n-text="notFound.title">${escapeHTML(notFoundText("notFound.title", "Not found"))}</div>
      </div>
    </div>
  `;
  // Force a full navigation so "/" can be handled server-side (landing in anonymous mode).
  const homeBtn = document.getElementById("homeBtn");
  if (homeBtn) {
    homeBtn.addEventListener("click", () => (window.location.href = appHomePath()));
  }
}
