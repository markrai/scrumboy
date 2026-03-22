"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.escapeHTML = escapeHTML;
exports.showToast = showToast;
var elements_js_1 = require("./dom/elements.js");
function escapeHTML(s) {
    return String(s)
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#039;");
}
function showToast(msg) {
    elements_js_1.toast.textContent = msg;
    elements_js_1.toast.classList.add("toast--show");
    setTimeout(function () { return elements_js_1.toast.classList.remove("toast--show"); }, 2500);
}
