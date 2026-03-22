# Board Rendering Performance Audit and Refactor Plan

**Scope:** `internal/httpapi/web/modules/views/board.ts` and related helpers  
**Constraints:** Framework-free, no virtual DOM, no new dependencies, incremental improvements only

---

## Phase 1 — Eliminate Redundant DOM Updates

### 1.1 Chip rendering guard

**Current state:**
- `updateChipsOnly` (line 360–361): **Has** `if (chipsHTML === lastRenderedChipsHTML) return` — correct.
- `updateBoardContent` (lines 877–894): **Missing** — always rebuilds `filtersEl.innerHTML` and calls `initMobileTagPagination()`.
- `renderBoardFromData` full-path (lines 1070–1073): Initial render only; no guard needed.

**Required changes:**
1. In `updateBoardContent`, before rebuilding the filters section:
   - Compute `chipsHTML` from `combinedChipData` (already done at line 875).
   - Add: `if (chipsHTML === lastRenderedChipsHTML)` then **skip** the filters `innerHTML` update and `initMobileTagPagination()`.
   - Still update `lastDisplayChipData` for consistency.
   - Update `lastRenderedChipsHTML` only when we actually write to DOM.

2. In `initMobileTagPagination` mobile path (lines 515–516): It overwrites `tagChipsEl.innerHTML` for measurement. The prev/next handlers (lines 579, 588) also do `innerHTML` — these are pagination-specific (different slice per page) and do not need the guard.

### 1.2 Board rebuild guard (implemented with full signature)

**Implemented:** Lightweight render signature (board reference + tag + search + sprintId). Skip full rebuild only when all four match. Avoids stale UI from board-reference-only comparison.

---

## Phase 2 — Reduce Expensive DOM Queries

### 2.1 Scope `hydrateAvatarsOnCards` to board

**Current state (line 642):**
```ts
document.querySelectorAll<HTMLElement>("[data-assignee-user-id]")
```
Scans the entire document.

**Required change:**
```ts
const boardEl = document.querySelector(".board");
if (!boardEl) return;
boardEl.querySelectorAll<HTMLElement>("[data-assignee-user-id]").forEach(...)
```

### 2.2 Track hydrated cards with dataset

**Current state (line 647):**
```ts
if (card.querySelector(".todo-avatar")) return; // already hydrated
```
Uses a DOM query per card.

**Required change:**
```ts
if (card.dataset.avatarHydrated === "1") return;
// ... after inserting avatar ...
card.dataset.avatarHydrated = "1";
```

Remove the `card.querySelector(".todo-avatar")` check in favor of the dataset flag.

### 2.3 Early exit when no cards need hydration

**Required change:**
Before the `forEach`, collect cards that need hydration. If none, return:

```ts
const cards = Array.from(boardEl.querySelectorAll<HTMLElement>("[data-assignee-user-id]"));
const toHydrate = cards.filter(c => c.dataset.avatarHydrated !== "1");
if (toHydrate.length === 0) return;
toHydrate.forEach((card) => { ... });
```

---

## Phase 3 — Avoid Repeated Render-Time Computations

### 3.1 `renderTodoCard` — precompute outside loop

**Current state:**
- `getTagColor(tagName)` called per tag per card (inside `.map` over `t.tags`).
- `isModifiedFibonacciModeEnabled()` called per card.

**Required changes:**
1. Add optional parameter `tagColors?: Record<string, string>` to `renderTodoCard`. When provided, use `tagColors[tagName] ?? null` instead of `getTagColor(tagName)`.
2. Precompute before the card loop in all call sites:
   - `const tagColors = getTagColors();`
   - `const showPointsMode = isModifiedFibonacciModeEnabled();`
3. Pass `tagColors` into `renderTodoCard` (or use a closure). Use `showPointsMode` instead of calling `isModifiedFibonacciModeEnabled()` inside the card.

**Call sites to update:**
- `updateBoardContent` (line 921): precompute `tagColors`, `showPointsMode`; pass to `renderTodoCard`.
- `renderBoardFromData` (line 1192): same.
- `handleLoadMore` (line 707): same.

### 3.2 Verify no other expensive work in `renderTodoCard`

**Current state:** `renderTodoCard` already receives `membersByUserId` and `columnColor` from outside. The only remaining per-card work is:
- `getTagColor` (to be precomputed).
- `isModifiedFibonacciModeEnabled` (to be precomputed).
- `escapeHTML` (necessary, keep).
- `renderAvatarContent` (necessary when assignee exists, keep).

---

## Phase 4 — Stabilize Members Lookup Cache

**Current state (lines 96–102):**
```ts
function getMembersByUserId(): Record<number, BoardMember> {
  const members = getBoardMembers();
  if (members !== membersByUserIdCacheSource) {
    membersByUserIdCacheSource = members;
    membersByUserIdCache = Object.fromEntries(members.map((m) => [m.userId, m]));
  }
  return membersByUserIdCache;
}
```

**Required change:**
Add a length check so we rebuild when the array length changes (e.g. same reference but mutated):

```ts
if (
  members !== membersByUserIdCacheSource ||
  Object.keys(membersByUserIdCache).length !== members.length
) {
  membersByUserIdCacheSource = members;
  membersByUserIdCache = Object.fromEntries(members.map((m) => [m.userId, m]));
}
```

---

## Phase 5 — Prevent Unnecessary Avatar Hydration

**Current state:** `hydrateAvatarsOnCards` is only called from:
- `loadBoardBySlug` fetch callback (line 1869), after `setBoardMembers(members)`.
- Prefetched path fetch callback (line 2041), after `setBoardMembers(members)`.

**Verification:** It is **not** called during normal board renders (e.g. `updateBoardContent`, `renderBoardFromData`). No change required.

**Optional guard:** Add an early exit if there are no cards with `[data-assignee-user-id]` without avatars (covered in Phase 2.3).

---

## Phase 6 — Event Bus Listener Safety

**Current state (lines 384–393):**
```ts
let sprintEventSubscribed = false;
function ensureSprintSubscription(): void {
  if (sprintEventSubscribed) return;
  sprintEventSubscribed = true;
  on("sprint-updated", (payload: ...) => { ... });
}
```

**Verification:**
- `sprintEventSubscribed` prevents multiple registrations.
- `ensureSprintSubscription()` is called once at module load (line 1900).
- The event bus `on()` adds to a `Set`; each registration would add a new handler, but the guard ensures we only register once.
- **No change required.**

---

## Phase 7 — Avoid Style/Layout Thrashing

**Current state:** `initMobileTagPagination` is called from:
1. `updateChipsOnly` (line 367) — after chip DOM update.
2. `updateBoardContent` (line 894) — after filters DOM update.
3. `renderBoardFromData` (line 1636) — after full `app.innerHTML` (initial render).

**`getBoundingClientRect` usage (line 524):**
- Only runs when `isMobile` is true.
- Runs after `tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData)` (line 516) — chip DOM was just changed.
- Resize listener (lines 476–478) debounces with 150ms before re-running.

**Required change:**
- In `updateBoardContent`, add the chips guard (Phase 1.1). When `chipsHTML === lastRenderedChipsHTML`, **do not** call `initMobileTagPagination()`. This avoids layout measurement when chips did not change.
- Ensure `initMobileTagPagination` is only invoked when chip DOM has actually changed (after filters/chips update). With Phase 1.1, we skip the filters update when chips are identical, so we also skip `initMobileTagPagination`.

---

## Phase 8 — Performance Summary (Post-Implementation)

### Functions to modify

| Function | Changes |
|----------|---------|
| `updateBoardContent` | Chips guard, board reference guard, precompute tagColors/showPointsMode, pass to renderTodoCard |
| `renderBoardFromData` | Precompute tagColors/showPointsMode, pass to renderTodoCard |
| `renderTodoCard` | Accept optional tagColors, use showPointsMode param instead of calling isModifiedFibonacciModeEnabled |
| `handleLoadMore` | Precompute tagColors/showPointsMode, pass to renderTodoCard |
| `hydrateAvatarsOnCards` | Scope to `.board`, use dataset.avatarHydrated, early exit when nothing to hydrate |
| `getMembersByUserId` | Add length check to cache invalidation |

### Operations removed from render loops

- `getTagColor(tagName)` — replaced with precomputed `tagColors` lookup.
- `isModifiedFibonacciModeEnabled()` — replaced with single precomputed `showPointsMode`.
- `card.querySelector(".todo-avatar")` — replaced with `dataset.avatarHydrated`.

### DOM queries reduced

- `document.querySelectorAll("[data-assignee-user-id]")` → scoped to `boardEl.querySelectorAll(...)`.
- Fewer cards iterated when early-exit on empty `toHydrate`.
- `initMobileTagPagination` skipped when chips HTML unchanged (avoids `getBoundingClientRect` on unchanged DOM).

### Estimated improvement

| Scenario | Improvement |
|----------|-------------|
| Board navigation | Skip redundant `updateBoardContent` when board/chips unchanged; skip `initMobileTagPagination` when chips unchanged. |
| Large boards (40–100 cards) | ~80–200 fewer `getTagColor` calls; ~40–100 fewer `isModifiedFibonacciModeEnabled` calls; faster avatar hydration via scoped query + dataset flag. |

### UI behavior

- No changes to visible UI.
- All changes are performance-only; rendering output remains identical.
