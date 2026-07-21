# Roles and Permissions

## System Roles vs Project Roles

**System roles** (Owner, Admin, User) govern system-level permissions such as user management and admin APIs.

**Project roles** (Maintainer, Contributor, Viewer) govern project-level permissions and are stored in the `project_members` table.

**Important:** System roles (Owner, Admin, User) **never** grant project permissions. Project access is solely via `project_members`. A system Admin or Owner cannot access, modify, or delete a project without an explicit project membership.

---

## Project Role Hierarchy

| Role        | Rank | Description                                                                 |
|-------------|------|-----------------------------------------------------------------------------|
| Viewer      | 1    | Read-only: view board, todos, members                                       |
| Contributor | 2    | Body-only edit when assigned; no create, delete, move, or assign. Create tags. |
| Maintainer  | 3    | Project metadata, members, sprints, assign others, full todo CRUD           |

*Editor is deprecated; merged into Contributor. Owner is deprecated (Phase 2); migrated to Maintainer.*

---

## Backend Authorization (Store Layer)

| Action                         | Required Role | Notes                                  |
|--------------------------------|---------------|----------------------------------------|
| View project, board, todos     | Viewer+       |                                        |
| List project members           | Viewer+       | Response: userId, name, image, role (no email) |
| Create todo                    | Maintainer    |                                        |
| Edit todo (title, body, tags, sprint, estimation) | Maintainer | Except body when assigned (see below)   |
| Edit body when assigned        | Contributor   | Body-only; no title, tags, sprint, assign |
| Move todo                      | Maintainer    |                                        |
| Delete todo                    | Maintainer    |                                        |
| Self-assign todo               | Maintainer    | Contributor cannot self-assign          |
| Assign todo to others          | Maintainer    |                                        |
| Create tags                    | Contributor+  |                                        |
| Delete project                 | Maintainer+   |                                        |
| Update project name/image      | Maintainer+   |                                        |
| Update default sprint weeks    | Maintainer+   |                                        |
| Add/remove members             | Maintainer+   | Last maintainer cannot be removed      |
| List available users (add UI)  | Maintainer+   |                                        |
| Sprints CRUD (create/activate/close) | Maintainer+ | Via board settings                      |

---

## Permission Architecture

Permissions flow: `permissions.go` → store enforcement → API error mapping → UI affordances.

- **Backend is authoritative:** Store methods enforce role checks; API returns 401/404 on failure.
- **UI hides controls:** Contributors do not see create, delete, move, assign, or title/tag/sprint controls. Body-only edit is shown when assigned.

---

## Anonymous Temporary Boards

**Definition:** Projects with `expires_at IS NOT NULL` and `creator_user_id IS NULL`. Share-link style; no authentication required.

**Bypass rules:**
- Create, delete, move todos: allowed without auth
- Rename project: allowed without auth (active boards only; past `expires_at` → 404)
- Assignment: not allowed (validation error)
- Project image, delete project: immutable (404)

**Expiration:** Temporary boards use `expires_at` (initially 90 days from creation; board activity can roll the expiry forward). Once `expires_at` is in the past, board reads and mutations return **404** until the project row is removed. This applies to authenticated temporary boards in full mode as well as unowned anonymous boards.

**UI:** New Todo and drag-and-drop are enabled for anonymous boards (same as Maintainer on durable boards).

---

## UI Visibility (Board Topbar)

| Element          | Visible To                              |
|------------------|-----------------------------------------|
| New Todo         | Maintainer, or anonymous temp board     |
| Drag-and-drop    | Maintainer, or anonymous temp board      |
| Members button   | Maintainer, Contributor                 |
| Delete Project   | Maintainer (durable boards only)        |

**Members dialog:**
- **Maintainer:** Full UI — member list, add form, remove buttons
- **Contributor:** View-only — member list (name + role), no add/remove

---

## Settings Dialog Tabs

This table is **Settings tab visibility** in the SPA (`modules/dialogs/settings.ts` / `renderSettingsModal`), not the system-role or project-role permission matrices above.

| Tab | Visible when |
|-----|----------------|
| Profile | Full mode (auth status available) |
| Users | Full mode **and** system role Admin or Owner |
| Sprints | Board view (`slug` set) **and** project role Maintainer |
| Workflow | Board view **and** project role Maintainer |
| Customization | Always when Settings is open |
| Tag Colors | Always when Settings is open (replaces older “Tags” label) |
| Charts | Board view, full mode, **and** durable project (not temporary/anonymous expiry boards) |
| Backup | Always when Settings is open |

Contributor/Viewer on a durable board typically see Customization, Tag Colors, Charts, and Backup (not Sprints/Workflow). Anonymous/temporary boards omit Charts; Profile/Users require full mode.

---

## Access-Denied Response: 404 Only

For project resources, access denial must return **404** (not 403). Returning 403 would leak project existence. Always return 404 when the user lacks project access.

```mermaid
flowchart LR
    RequireRole[requireProjectRole] --> CheckProjectRole[CheckProjectRole]
    CheckProjectRole -->|Fail| Err404[404]
    CheckProjectRole -->|OK| InvokeStore[Call store operation]
```
