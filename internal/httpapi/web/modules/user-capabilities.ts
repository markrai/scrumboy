import type { User } from "./types.js";

export function canCreateBoards(user: User | null): boolean {
  return user?.capabilities?.createBoards ?? user?.systemRole !== "guest";
}
