import { toast } from './dom/elements.js';
/**
 * Returns true if the board is anonymous (temporary, no creator).
 * Use this helper everywhere instead of duplicating expiresAt/creatorUserId logic.
 */
export function isAnonymousBoard(board) {
    return !!(board?.project && board.project.expiresAt != null && board.project.creatorUserId == null);
}
/** Any temporary board (expiresAt set): unowned anonymous temp or FULL-mode temp with creator. Not durable. */
export function isTemporaryBoard(board) {
    return !!(board?.project?.expiresAt != null);
}
/** Single source of truth for hex color validation. Matches backend colorHexRe. Must be {6} not +. */
export const HEX_COLOR_RE = /^#[0-9a-fA-F]{6}$/;
/** Returns valid hex color, or fallback if provided, or null. Use when rendering to avoid XSS. */
export function sanitizeHexColor(color, fallback) {
    if (!color || typeof color !== "string")
        return fallback ?? null;
    const c = color.trim();
    if (HEX_COLOR_RE.test(c))
        return c;
    return fallback ?? null;
}
function escapeHTML(s) {
    return String(s)
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#039;");
}
function showToast(msg) {
    toast.textContent = msg;
    toast.classList.add("toast--show");
    setTimeout(() => toast.classList.remove("toast--show"), 2500);
}
/**
 * Process an image file: validate size (max 1MB), crop to center square, resize to 128x128 PNG.
 * Returns a data URL. Rejects on validation or load error.
 */
const WALLPAPER_MAX_INPUT_BYTES = 8 * 1024 * 1024;
const WALLPAPER_MAX_DIM = 2560;
/**
 * Resize wallpaper client-side (JPEG) to reduce upload size; max dimension 2560px.
 */
export function processWallpaperFileForUpload(file) {
    if (!file.type.startsWith('image/')) {
        return Promise.reject(new Error('Please choose an image file'));
    }
    if (file.size > WALLPAPER_MAX_INPUT_BYTES) {
        return Promise.reject(new Error('Image must be smaller than 8MB'));
    }
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = (e) => {
            const dataUrl = e.target?.result;
            if (!dataUrl) {
                reject(new Error('Failed to read file'));
                return;
            }
            const img = new Image();
            img.onload = () => {
                let w = img.width;
                let h = img.height;
                const maxSide = Math.max(w, h);
                const scale = maxSide > WALLPAPER_MAX_DIM ? WALLPAPER_MAX_DIM / maxSide : 1;
                w = Math.max(1, Math.round(w * scale));
                h = Math.max(1, Math.round(h * scale));
                const canvas = document.createElement('canvas');
                canvas.width = w;
                canvas.height = h;
                const ctx = canvas.getContext('2d');
                if (!ctx) {
                    reject(new Error('Failed to get canvas context'));
                    return;
                }
                ctx.drawImage(img, 0, 0, w, h);
                canvas.toBlob((blob) => {
                    if (!blob) {
                        reject(new Error('Failed to encode image'));
                        return;
                    }
                    resolve(blob);
                }, 'image/jpeg', 0.85);
            };
            img.onerror = () => reject(new Error('Failed to load image'));
            img.src = dataUrl;
        };
        reader.onerror = () => reject(new Error('Failed to read file'));
        reader.readAsDataURL(file);
    });
}
export function processImageFile(file) {
    if (file.size > 1024 * 1024) {
        return Promise.reject(new Error('Image size must be less than 1MB'));
    }
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = (e) => {
            const dataUrl = e.target?.result;
            if (!dataUrl) {
                reject(new Error('Failed to read file'));
                return;
            }
            const img = new Image();
            img.onload = () => {
                const canvas = document.createElement('canvas');
                const size = Math.min(img.width, img.height);
                canvas.width = size;
                canvas.height = size;
                const ctx = canvas.getContext('2d');
                if (!ctx) {
                    reject(new Error('Failed to get canvas context'));
                    return;
                }
                const sx = (img.width - size) / 2;
                const sy = (img.height - size) / 2;
                ctx.drawImage(img, sx, sy, size, size, 0, 0, size, size);
                const maxSize = 128;
                let finalDataUrl;
                if (size > maxSize) {
                    const resizedCanvas = document.createElement('canvas');
                    resizedCanvas.width = maxSize;
                    resizedCanvas.height = maxSize;
                    const resizedCtx = resizedCanvas.getContext('2d');
                    if (!resizedCtx) {
                        reject(new Error('Failed to get canvas context'));
                        return;
                    }
                    resizedCtx.drawImage(canvas, 0, 0, maxSize, maxSize);
                    finalDataUrl = resizedCanvas.toDataURL('image/png');
                }
                else {
                    finalDataUrl = canvas.toDataURL('image/png');
                }
                resolve(finalDataUrl);
            };
            img.onerror = () => reject(new Error('Failed to load image'));
            img.src = dataUrl;
        };
        reader.onerror = () => reject(new Error('Failed to read file'));
        reader.readAsDataURL(file);
    });
}
export function getUserInitials(user) {
    if (!user)
        return "?";
    if (user.name && user.name.trim()) {
        const parts = user.name.trim().split(/\s+/);
        if (parts.length >= 2) {
            return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
        }
        else if (parts[0].length >= 2) {
            return parts[0].substring(0, 2).toUpperCase();
        }
        else {
            return parts[0][0].toUpperCase();
        }
    }
    if (user.email && user.email.trim()) {
        const emailPart = user.email.split('@')[0];
        return emailPart[0].toUpperCase();
    }
    return "?";
}
/**
 * Base avatar visual (image or initials). No wrapper, no IDs, no behavior, no ARIA.
 * Reusable in any context (topbar, todo cards, comments, member lists).
 * Returns a single element: <img class="user-avatar__img" /> or <span class="user-avatar__initials">...</span>.
 */
export function renderAvatarContent(user) {
    if (!user)
        return '';
    return user.image
        ? `<img src="${user.image}" alt="" class="user-avatar__img" />`
        : `<span class="user-avatar__initials">${getUserInitials(user)}</span>`;
}
export function renderUserAvatar(user, options) {
    if (!user)
        return '';
    const label = user.name || user.email || 'User';
    const content = renderAvatarContent(user);
    const id = options?.id ?? 'userAvatarBtn';
    const ariaLabel = options?.ariaLabel ?? 'Open profile settings';
    return `
    <button type="button" class="user-avatar" id="${escapeHTML(id)}" title="${escapeHTML(label)}" aria-label="${escapeHTML(ariaLabel)}">
      ${content}
    </button>
  `;
}
/**
 * Redirect to a path with a cache-busting query param so the browser always does a fresh load.
 * Required when redirecting to the same URL (e.g. / after login or logout) — otherwise the browser
 * may serve from cache and the UI won't reflect the new auth state.
 */
export function redirectAfterAuth(path) {
    const base = path || "/";
    const sep = base.includes("?") ? "&" : "?";
    window.location.replace(base + sep + "_=" + Date.now());
}
/**
 * Get the app version from the meta tag embedded in the HTML.
 * This version is injected at build time from internal/version/version.go
 */
export function getAppVersion() {
    const meta = document.querySelector('meta[name="app-version"]');
    return meta ? (meta.getAttribute("content") || "") : "";
}
/**
 * Shows a custom confirmation dialog matching the site's design.
 * Returns a Promise that resolves to true if confirmed, false if cancelled.
 * @param message - Body text
 * @param title - Dialog title (default "Confirm")
 * @param confirmLabel - Label for confirm button (default "Confirm")
 */
export function showConfirmDialog(message, title = "Confirm", confirmLabel = "Confirm") {
    return new Promise((resolve) => {
        // Create dialog element
        const dialog = document.createElement('dialog');
        dialog.className = 'dialog';
        // Create dialog content
        dialog.innerHTML = `
      <div class="dialog__form">
        <div class="dialog__header">
          <div class="dialog__title">${escapeHTML(title)}</div>
          <button class="btn btn--ghost" type="button" id="confirmDialogClose" aria-label="Close">✕</button>
        </div>
        <div class="dialog__content">
          <p>${escapeHTML(message)}</p>
        </div>
        <div class="dialog__footer">
          <div class="spacer"></div>
          <button class="btn btn--ghost" type="button" id="confirmDialogCancel">Cancel</button>
          <button class="btn btn--danger" type="button" id="confirmDialogConfirm">${escapeHTML(confirmLabel)}</button>
        </div>
      </div>
    `;
        // Add to DOM
        document.body.appendChild(dialog);
        // Show dialog
        dialog.showModal();
        // Handle close button
        const closeBtn = dialog.querySelector('#confirmDialogClose');
        closeBtn.addEventListener('click', () => {
            dialog.close();
            dialog.remove();
            resolve(false);
        });
        // Handle cancel button
        const cancelBtn = dialog.querySelector('#confirmDialogCancel');
        cancelBtn.addEventListener('click', () => {
            dialog.close();
            dialog.remove();
            resolve(false);
        });
        // Handle confirm button
        const confirmBtn = dialog.querySelector('#confirmDialogConfirm');
        confirmBtn.addEventListener('click', () => {
            dialog.close();
            dialog.remove();
            resolve(true);
        });
        // Handle ESC key (native dialog behavior)
        dialog.addEventListener('cancel', (e) => {
            e.preventDefault();
            dialog.close();
            dialog.remove();
            resolve(false);
        });
        // Focus the cancel button by default (safer)
        cancelBtn.focus();
    });
}
/**
 * Standardized delete confirmation wrapper for destructive actions.
 */
export function confirmDelete(options) {
    const normalized = typeof options === "string"
        ? { message: options }
        : options;
    return showConfirmDialog(normalized.message, normalized.title ?? "Delete", normalized.confirmLabel ?? "Delete");
}
export { escapeHTML, showToast };
