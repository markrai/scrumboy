// Scrumbaby wall permissions. The backend is the source of truth (durable +
// contributor or higher), but the UI mirrors the rule to keep the dialog
// read-only for viewers and to avoid obviously doomed write attempts.

export type WallRole = string | null | undefined;

export function canEditWall(role: WallRole): boolean {
  return role === "maintainer" || role === "contributor";
}
