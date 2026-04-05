/** Mute assignment toast+sound via localStorage key `scrumboy_assignment_notify_muted` = `'1'`. */
const MUTE_KEY = "scrumboy_assignment_notify_muted";
let audio = null;
function getNotifyAudio() {
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
export function playAssignmentSound() {
    try {
        if (typeof localStorage !== "undefined" && localStorage.getItem(MUTE_KEY) === "1") {
            return;
        }
    }
    catch {
        // ignore storage errors
    }
    const a = getNotifyAudio();
    a.currentTime = 0;
    void a.play().catch(() => { });
}
/** OS Notification API (optional; permission requested from Settings → Customization). */
export function showAssignmentDesktopNotification(title) {
    if (typeof Notification === "undefined") {
        return;
    }
    if (Notification.permission !== "granted") {
        return;
    }
    try {
        new Notification("New assignment", { body: title || "Todo" });
    }
    catch {
        // ignore
    }
}
export async function requestDesktopNotificationPermission() {
    if (typeof Notification === "undefined") {
        return "denied";
    }
    try {
        return await Notification.requestPermission();
    }
    catch {
        return "denied";
    }
}
export function getDesktopNotificationStatusDescription() {
    if (typeof Notification === "undefined") {
        return "Not supported in this browser.";
    }
    switch (Notification.permission) {
        case "granted":
            return "Enabled - you will receive OS notifications for new assignments.";
        case "denied":
            return "Blocked — allow notifications for this site in your browser settings.";
        default:
            return "Not enabled yet — click the button below (your browser will ask for permission).";
    }
}
