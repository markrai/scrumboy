// Scrumbaby wall permissions. The backend is the source of truth (durable +
// contributor or higher), but the UI mirrors the rule to keep the dialog
// read-only for viewers and to avoid obviously doomed write attempts.
export function canEditWall(role) {
    return role === "maintainer" || role === "contributor";
}
