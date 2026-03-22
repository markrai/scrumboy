import { toast } from './dom/elements.js';
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
export { escapeHTML, showToast };
