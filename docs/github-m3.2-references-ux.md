# M3.2: Todo References UX refinement

This change is part of cumulative PR #366 and remains **trusted administrator-managed GitHub App mode only**. It does not implement the planned App Manifest onboarding wizard or give unrelated users access to the instance-owned installation.

## Phases

- [x] **R1: Browse API.** Read-only `GET /api/board/{slug}/integrations/github/browse?resourceId=ID&kind=pr&state=open[&q=title]`. The repository must be available, connected and associated with the caller's project. Authenticated project Viewers or higher may browse. `kind` is `issue` or `pr`, `state` is `open`, `closed` or `all`, and optional `q` is 2–100 characters. Responses include at most 20 items, sorted by recent updates. No arbitrary provider query or GitHub writes are accepted; result titles and URLs are validated, and installation tokens stay repository-ID-scoped and read-only.
- [x] **R2: Todo modal.** Both New and Edit Todo have **Details** and **References** tabs on durable, authenticated project boards. Details remains default; references load lazily on selecting the tab. Viewers may read linked references but have no mutation controls. Public and temporary boards do not expose the tab.
- [x] **R3: Discoverability.** The References tab separates linked items from a browsable list of available issues/PRs. Default is open pull requests in the selected linked repository; type, state and repository dropdowns load a new bounded result. The title input filters automatically, including debounced authenticated provider search for two or more characters. The Search button is removed; result rows expose a Link control, suppress duplicates, and keep URL paste as an option.
- [x] **R4: New Todo staging.** Repository and issue/PR selections are kept locally until a Todo is created and a canonical local ID is returned. Save then attaches staged links through the existing role-checked endpoints. Link failures cannot roll back a successfully created Todo and are surfaced as an actionable warning; they must be retried from the saved Todo. Unsaved changes to staged references participate in the Todo close guard.
- [ ] **R5: Automated and live acceptance.** Complete the final Go/frontend/migration/security CI and generated-JavaScript parity; verify New/Edit Todo, mobile layout, Viewer/Contributor/Maintainer, cross-project/private-repository denial, provider outage, cancellation, unavailable resources, and partial linking failures on isolated Vega staging.

## Operator walkthrough

1. On a maintained durable project with an administrator-connected GitHub App, open **New Todo → References** before saving. Verify the project-associated repository list, open PRs (up to 20) and Issues switcher. Pick an item and an external repository; verify both appear as selected while the Todo is still unsaved.
2. Return to **Details**, enter a title, and save. Open the created Todo → References; the selected repository and issue/PR must now appear as real persisted references. Verify that the Todo title/body were not altered by GitHub and that no GitHub write occurred.
3. Filter by type, open/closed status and title (without a Search click); select another item. Reopen the Todo to verify persistence and then unlink the item.
4. Check Viewer mode (cached references visible, no link/edit actions), unrelated project membership, unavailable repositories and a deliberately disconnected provider. Error handling should not remove previous references or silently create duplicate Todos.
5. Record test revision, digest and redacted acceptance evidence. The App Manifest setup wizard is tracked in #371 and is not implemented or live-qualified.

## Boundaries

Browsing is lazy, read-only, project-scoped and capped. GitHub Search API limits still apply. The first 20 recently updated results do not constitute a complete issue/PR archive; a two-character title filter queries GitHub again for more targeted results. New Todo staging does not modify GitHub and is not a transactional batch operation: a transient error after Todo creation may leave some references unlinked, explicitly reported to the operator. M4 webhook automation and M5 operator opt-in gates are unchanged.
