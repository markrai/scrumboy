import { app } from '../dom/elements.js';

export function renderNotFound(): void {
  app.innerHTML = `
    <div class="page">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
        <button class="btn" id="homeBtn">Home</button>
      </div>
      <div class="empty">
        <div class="empty__title">Not found</div>
      </div>
    </div>
  `;
  // Force a full navigation so "/" can be handled server-side (landing in anonymous mode).
  const homeBtn = document.getElementById("homeBtn");
  if (homeBtn) {
    homeBtn.addEventListener("click", () => (window.location.href = "/"));
  }
}
