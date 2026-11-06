# Service accounts: architecture decision record

**Status:** policy decisions accepted by the maintainer (below). Tracks [issue #319](https://github.com/markrai/scrumboy/issues/319) and the maintainer's [guardrails](https://github.com/markrai/scrumboy/issues/319#issuecomment-6474883032).

**Scope:** Slice 1 (identity foundation) is implemented. Slice 2 (accounts and tokens over MCP) and Slice 3 (webhooks over MCP) are designs only.

Each statement below is marked:

- **Implemented**: in the Slice 1 code, covered by tests.
- **Existing**: behaviour that predates this work and is preserved.
- **Accepted**: a maintainer decision. Where it concerns a later slice it is a design, not built code.
- **Proposed**: a design detail for a later slice, not built.

## Accepted decisions

| | Decision | Rule | Detail |
|---|---|---|---|
| A | Service-token minting | Only the system Owner may mint service-account credentials in the initial implementation. Admins may create and list service accounts but cannot mint for them | Section 9 |
| B | Anonymous and temporary boards | An authenticated service identity needs explicit membership on every project, including link-access boards. Unauthenticated visitors and human accounts are unchanged. This does not make a publicly accessible board private | Section 7 |
| C | Service-account Maintainer | An explicitly assigned service account receives the existing Maintainer permissions, including project administration and deletion where permitted. There is no separate role hierarchy. System administration stays prohibited | Section 9 |
| D | Identity-reference integrity | A pre-existing issue, handled independently of this slice through the private security process. Existing service-account protections stay | Section 6 |
| E | Instance-level auditing | A small dedicated instance-audit stream, rather than rebuilding the project-scoped table for nullable project IDs. Not implemented in this slice | Section 12 |
| F | Webhook delegation | A project Maintainer may register a webhook owned by an assigned service-account member. Viewer membership suffices for the receiving identity. Not implemented yet | Section 13 |

## 1. Problem and security boundaries

An unattended integration today needs a human to mint a token, and that token authenticates as the human with all of the human's authority. In practice a bot gets an admin's token and can call `admin_deleteUser`. A service account gives the integration its own identity, so its blast radius is the projects it was explicitly added to.

The boundaries this design has to hold:

1. A service identity never authenticates interactively.
2. A service identity never holds instance authority: no Owner, no Admin, no credential provisioning.
3. A service identity reaches a project only through an explicit `project_members` row.
4. Provisioning a credential can never become a way to impersonate a human or to gain project access the provisioner lacks.
5. Deleting an account or revoking a token stops all later authentication at commit, whether or not auditing is available. Section 11 states exactly what this does to requests already in flight.
6. Deleting an account never leaves a surviving project without a Maintainer, for humans and service accounts alike.

It does not try to make a link-accessible board private. See section 7.

## 2. Why `users`, not a separate principal table

**Implemented.** Service accounts are rows in `users` with `is_service = 1`. The maintainer stated this is acceptable if it fits the architecture, and it does:

- `project_members`, `api_tokens`, `webhooks`, assignment, and attribution all key on `users.id`, so a service account works with the existing membership model unchanged.
- A separate table would require every one of those references to become polymorphic, or a parallel set of membership and token tables.
- `users.public_id` (migration 076) already gives a permanent external identifier.

The cost is that every interactive path has to exclude service accounts. Section 5 records how that is kept from sprawling.

`users.email` is `NOT NULL UNIQUE`, so a service account has a generated placeholder, `svc-<uuid>@service.invalid`. **No authorization or identity decision reads the email.** Account type comes only from `is_service`.

## 3. `users.is_service` and `api_tokens.is_service`

These are unrelated and both stay.

| | `users.is_service` | `api_tokens.is_service` |
|---|---|---|
| Describes | The kind of account | A token's lifecycle handling |
| Set by | `CreateServiceAccount` only, immutable | The token's creator |
| Effect on authority | Non-interactive, role fixed to `user`, membership-only | None. The token acts as its owner |
| Effect on deletion | None directly | Metadata is copied to `archived_service_api_tokens` when the owner is deleted |

**Existing:** a human's token with `api_tokens.is_service = 1` authenticates as that human with that human's full authority, including Admin or Owner tools. A service-flagged human token is not a service identity, and tests pin this.

A principal has four separate attributes that must not be conflated:

| Concept | Source | Mutable |
|---|---|---|
| Account type | `users.is_service` | No |
| System role | `users.system_role` | Humans only |
| Project role | `project_members.role`, per project | Yes |
| Credential provenance | Session, API token, or OAuth token used on this request | Per request |

## 4. Principal propagation

**Implemented.** The account type travels with the existing actor envelope in `internal/store/authctx.go`.

- `store.WithServiceIdentity(ctx, userID, isService)` records what the transport read when it authenticated the request.
- `store.ServiceIdentityFromContext(ctx, userID)` returns it only for that same `userID`. It cannot answer for anyone else, and `ok` is false when nothing was recorded.
- `isServiceUser` in `internal/store/service_accounts.go` is the single resolver: the recorded value if present, otherwise the `users` row.

| Transport | Credential | Lookup | Recorded |
|---|---|---|---|
| HTTP `requestContext` | Session cookie | `GetUserBySessionToken`, excludes service accounts | Always human |
| MCP `resolveRequestAuth` | Session cookie | Same | Always human |
| MCP `resolveRequestAuth` | API token | `GetUserByAPIToken`, returns either type | The account's real type |
| MCP `resolveRequestAuth` | OAuth access token | `GetUserByOAuthAccessToken`, excludes service accounts | Always human |
| Background work, direct store callers | None | n/a | Nothing, so the store reads the row |

**Why this is safe to trust:** account type is immutable (trigger below), so it cannot go stale during a request. Roles and memberships can, so they are never captured this way and are always read inside the mutating transaction.

**What it does not replace:** a wrong value here could only skip the link-board check or the project-creation check. The schema triggers still refuse a service account as a project owner, creator, or holder of the legacy `owner` member role, whatever the context says.

**Boundaries verified by audit and tests:**

- `requestContext` and `resolveRequestAuth` are the only callers of `WithUserID` outside tests, and each pairs it with `WithServiceIdentity`, using the row returned by the credential lookup. The context key is unexported, and no request field reaches either call. `TestMCP_ForgedIdentityFieldsAreIgnored` sends forged identity fields and they change nothing.
- The value is bound to a user ID, so it cannot answer for anyone else.
- When nothing is recorded, the store reads the row. A row that no longer exists reads as "not a service account", which gives that caller only what an anonymous link holder already has. A request that did record a service identity stays blocked after its account is deleted, because the membership it needs is gone.
- `isService` on `POST /api/me/tokens` sets `api_tokens.is_service`, the token lifecycle flag. It has no effect on the principal: a human token with that flag remains a human principal, with that human's full authority.

Authorization never relies on the placeholder email, on `api_tokens.is_service`, on client-supplied fields, or on the UI.

## 5. Enforcement mechanisms

The first version of Slice 1 had 28 triggers and duplicate guards at most project write sites. A review removed what it could prove redundant, leaving 14 in migration 080. A later review restored the two `project_members` owner-role triggers in migration 081, because their Go replacement could not cover SQL writes or future write paths. **16 triggers remain.**

### Retained

| Mechanism | Invariant | Role | Also enforced by | If restored or corrupted | Maintenance |
|---|---|---|---|---|---|
| `users_service_identity` CHECK | A service row has role `user`, no bootstrap flag, no password, no 2FA state, and a placeholder email | Primary (durable invariant) | Go guards on `users` credential writes | Travels with the table in a file backup | Must be recreated by any rebuild of `users` |
| `trg_users_is_service_immutable` | Account type never changes | Primary. No application path writes this column | Nothing | Same | Same |
| 10 `*_reject_service_insert` credential triggers | No interactive credential row can be created for a service account | Primary for writes. Replaces a Go guard at 14 insert sites | Read filters make any such row useless | Dropped triggers leave the read filters still failing closed | One trigger per credential table. A new credential table needs one, and the classification test forces that decision |
| Interactive read filters (`AND is_service = 0`) | A session, 2FA, OIDC, OAuth, grant, or recovery row for a service account grants nothing | Primary for reads, inside the existing query | Credential triggers | Hold even if a row exists | Every new interactive lookup must filter |
| Go guards on `users` credential writes | Passwords, 2FA, and roles are not written for a service account | Defense in depth, independent of the CHECK | CHECK | Hold with CHECK enforcement off | Small, in the mutation itself |
| 2 `trg_projects_reject_service_owner_*` triggers | A service account is never a project's owner or creator | Primary for ownership. Covers 9 write sites atomically, including imports and claiming | `rejectServiceActor` on the two reachable policies | n/a | Must be recreated by any rebuild of `projects` |
| 2 `trg_project_members_reject_service_owner_*` triggers | A service account never holds the legacy `owner` member role | Primary. Covers ordinary, direct, and any future write path, including upserts and re-parenting a row | `UpdateProjectMemberRole` already refuses the role for every account | Travels with the table in a file backup | Must be recreated by any rebuild of `project_members`. They do no lookup unless the role is `owner`, which no current path writes for a service account |
| `rejectServiceActor` | Project creation and imports are human operations | Application policy, with a clear error | Project triggers for ownership. Imports also need it because merge and replace touch rows without inserting an owner | n/a | Two callers |
| `rejectServiceLinkAccess` in the project loaders | A service account needs membership on an expiring board | Primary (section 7) | Nothing | n/a | New project loaders must call it |
| `trg_users_service_reject_inherited_references` and ID allocation | A service account is always created with fresh identity references | Allocation is primary, the trigger makes any other insert path fail loudly | Each other | n/a | Section 6 |
| `TestMigration080ProtectionsSurviveChain` and `TestEveryUserReferenceIsClassified` | The above cannot be lost silently | Maintenance control | n/a | n/a | New user-referencing columns must be classified |

### Removed in review

| Removed | Why it was safe |
|---|---|
| 10 `*_reject_service_update` triggers on `user_id` | No code re-parents a credential row (checked by searching every `UPDATE` in `internal` and `cmd`), and the read filters fail closed on one anyway |
| 2 reserved-email triggers | No code changes `users.email`. New human accounts are still refused in Go at all three creation paths. Authorization no longer depends on the domain, so a schema rule protected nothing |
| Go guard for the `owner` role in `AddProjectMember` | Replaced by the restored triggers. The guard also ran before authorization, which told a non-Maintainer the target's account type |
| `rejectServiceActor` at 7 of 8 sites | The project triggers enforce ownership atomically, and one new check at the single import entry covers every import mode. It is kept in `CreateProjectWithWorkflow` |
| Stale-creator SQL filters in list, export, and claim | They guarded a state the triggers make unreachable, and one was on the project list query |
| Go guard in `AssignUnownedDurableProjectsToUser` | The trigger refuses the write, and this removes a query from every login |
| The blanket `@service.invalid` guard in the mail worker | See below |

**Threat-model note:** someone able to drop triggers can also edit `is_service` or `project_members` directly. Project protections do not try to survive that. Credential read filters do, because they are the primary read boundary anyway.

### Notifications

**Implemented.** Every notification send path chooses recipients through `store.GetEmailNotifyPref`, which returns everything disabled for a service account. It is identity-aware and in one place. Password-reset mail goes through `GetUserByEmail`, which excludes service accounts.

The earlier guard in the mail worker dropped mail to any `@service.invalid` address, which would have silently cost an existing human on that domain their password-reset mail. It has been removed.

## 6. Identity-reference integrity

A pre-existing identity-reference integrity issue was identified during this work. A scoped mitigation protects service identities at creation, and the existing regression tests pin that behavior. General remediation changes account behavior beyond service accounts, so it is tracked separately through the project's private security process. It does not block the architectural separation of service accounts described here, but it must be addressed independently.

## 7. Temporary and anonymous boards

**Implemented. Accepted (decision B).** An authenticated service account with no membership gets `404` on temporary and anonymous boards, through the project loaders. An explicit membership opens the board. Signed-out callers and existing human accounts are unchanged.

**What this does and does not do.** It restricts the authority of a *service token*. It does not make a board private. Anyone holding the link, including whoever operates the integration, can still use the board with no credential at all, because link access is how these boards work. Public-view boards are likewise readable by anyone. The value is in attribution and containment: writes made under a service identity stay within delegated projects, and a leaked token plus a guessed slug gains nothing.

## 8. Authentication mechanisms

| Mechanism | Human | Service account | Enforced at |
|---|---|---|---|
| Password login | Allowed | Forbidden | `AuthenticateUser` filter, no hash can exist |
| Browser session | Allowed | Forbidden | Credential trigger, `GetUserBySessionToken` filter |
| Password reset and first-password setup | Allowed | Forbidden | Reset, grant, and CAS filters |
| OIDC login and linking | Allowed | Forbidden | Credential trigger, lookup filters |
| OAuth authorization and tokens | Allowed | Forbidden | Credential triggers, redeem, refresh, and lookup filters |
| 2FA and recovery codes | Allowed | Forbidden | Credential triggers, lookup filters, CHECK |
| Mobile OIDC handoff | Allowed | Forbidden | Credential trigger, exchange filter |
| Bootstrap and `recover-owner` | Allowed | Forbidden | CHECK, recovery guard |
| API token over MCP | Allowed | **Allowed** | `GetUserByAPIToken` returns the identity with its real type |
| API token over REST | Rejected | Rejected | Existing: REST does not accept bearer tokens |

A database holding only service accounts stays closed to bootstrap rather than reopening an anonymous Owner claim.

## 9. Authorization matrix

"Mint" is the one row where the proposed baseline was changed, by decision A. See the reasoning below.

| Operation | Owner | Admin | Human user | Service account | Status |
|---|---|---|---|---|---|
| Create service account | Yes | Yes | No | No | Implemented in store, tool Proposed |
| List service accounts | Yes | Yes | No | No | Proposed. Existing `admin_listUsers` already shows them, without a type flag |
| Delete service account | Yes | No | No | No | Implemented through `DeleteUser`, tool Proposed |
| Mint service token | Yes | **No** | No | No | Accepted (decision A), not built |
| List service tokens (metadata) | Yes | Yes | No | No | Proposed |
| Revoke service token | Yes | Yes | No | No | Proposed |
| Change a human's system role | Yes | No | No | No | Existing |
| Promote a service account | No one | No one | No | No | Implemented |
| Issue a password-reset link | Yes | No | No | No | Existing |
| Read the service-token archive | Yes | No | No | No | Existing |
| Instance-administration MCP tools | Per tool | Per tool | No | **No** | Implemented, role is always `user` |

| Project operation | System Owner or Admin with no membership | Project Maintainer | Contributor or Viewer | Service account |
|---|---|---|---|---|
| Access a project | **No** | Yes | Per role | Only with a membership, at that role |
| Manage members | No | Yes | No | Only if Maintainer |
| Delete the project | No | Yes | No | Only if Maintainer |
| Create a project | Any human | Any human | Any human | **Never** |
| Claim a temporary board | Its human creator only | Same | Same | **Never** |
| Import or export | Per existing rules | Same | Same | Import **never** |

### Minting is an indirect project-access path

The proposed baseline lets an Admin mint any service token. That creates an escalation the maintainer's guardrails rule out: a service account that is a Maintainer on a project gives its authority to whoever holds its token. An Admin who cannot see a project could mint a token for that account and act on the project as a Maintainer, which is exactly "an Admin's system role silently bypassing project permissions".

Owners already have comparable reach, because an Owner can issue a password-reset link for any non-Owner human.

**Accepted (decision A):**

- Only the system **Owner** may mint credentials for a service account in the initial implementation.
- An **Admin** may create and list service accounts but cannot mint their credentials, so credential issuance cannot be used to impersonate an identity with project permissions the Admin lacks.
- Owners and Admins may revoke, because revoking only reduces access.

### A service Maintainer has the whole Maintainer role

**Implemented. Accepted (decision C).** An explicitly assigned service account with Maintainer holds the existing Maintainer permissions, including project administration and deletion where currently permitted. There is no separate service-account role hierarchy. Tests pin this, and also pin that it stops at the project: other projects return `404`, and `admin_*` tools return `403`.

- *Project administration* is what a project's Maintainers delegate, and they can see and undo it.
- *Instance administration* depends on system role, which a service account can never hold.

Special-casing this would start the separate authorization framework this design avoids. It also matches the existing role rules, where only Maintainers can create or move todos (`CanCreateTodo`). The issue's "add it as a plain member" would give a bot that cannot create cards, so most integrations will need Maintainer. Whether a narrower role should exist is a separate question.

## 10. Project membership and ownership

**Implemented.**

- A service account can never be a project's owner or creator, and can never hold the legacy `owner` member role. That role has the same rank as Maintainer, and migration 045 rewrote every stored row. It remains valid for existing human rows, and `ParseProjectRole` reads it as Maintainer. No current store path writes it for a service account, and the schema refuses every other write, whichever way it is issued. `AddProjectMember` reports that refusal as `ErrServiceAccountRestricted`, after checking the requester.
- Creation paths that would make the actor an owner are refused: `projects_create`, anonymous and temporary board creation with an authenticated service actor, Trello import, and every backup import mode.
- `AddProjectMember`, `UpdateProjectMemberRole`, and `RemoveProjectMember` read roles inside their transactions. This also fixes an existing race in which two Maintainers removing each other could leave none.
- `UpdateUserRole` likewise moved its checks into its transaction. This fixes an existing race in which two Owners demoting each other could leave none.
- `AssignUnownedDurableProjectsToUser` and `EnsureMaintainerMembership` grant nothing to a service account. The first is refused by the trigger when there is something to assign, the second is a no-op.

### Deleting a project's sole Maintainer

**Implemented.** `DeleteUser` refuses, with `ErrConflict` (`409 CONFLICT` over both REST and MCP), to delete an account holding the last Maintainer role on a project that would survive. It applies to humans and service accounts alike.

- **What counts as a Maintainer:** `maintainer` or the legacy `owner` role, the same as `countMaintainersTx`. A service account counts (decision C).
- **Which projects survive:** every durable project. `DeleteUser` deletes no project, because no foreign key from `projects` to `users` cascades, so there is no project "being deleted in the same operation". `TestDeleteUser_RemovesNoProjectByCascade` fails if a cascade is ever added.
- **What is excluded:** temporary boards, whose authority is their creator or link rather than a Maintainer row, and which expire on their own. Staged import rows, which are not projects yet and which `AddProjectMember` also treats as absent.
- **What is not done:** no account is promoted and no ownership is transferred.
- **What the error says:** a count of projects only. The system Owner has no access to those projects, so the error names none of them.
- **Atomicity:** the check runs on the deletion's own transaction, before anything is written, and a refusal rolls back. Credentials, memberships, archive records, and audit history are unchanged, and the account's tokens keep working.
- **Concurrency:** production uses one connection, so these transactions cannot overlap. With several connections SQLite gives the same outcome: a check that reads after a competing commit sees it, and one that read before fails with a busy error when it writes. Tests force both interleavings for peer removal, demotion, and deletion, and the opposite order for member mutations.

**Consequence: there is no administrator override.** A system Owner cannot resolve this alone, because system roles grant no project access. A person or integration holding the sole Maintainer role must add a Maintainer or delete the project. Today an Owner can reach a stranded human project only through a password-reset link for that human, and cannot reach a project led only by a service account at all. Decision A provides that path once Slice 2 lets an Owner mint a token. Until then, a compromised service account that is a sole Maintainer cannot be deleted, and the token must be revoked in the database. Slice 2's revocation tool closes this.

**Owned projects:** an account recorded in `projects.owner_user_id` still cannot be deleted while that project exists. Once the sole-Maintainer check passes, `rejectOwnedProjectDeletionTx` refuses with `ErrConflict` (`409 CONFLICT` over both REST and MCP). The error carries only a project count, never names, slugs, IDs, or raw SQLite errors, and the refused deletion rolls back atomically with credentials and history unchanged.

## 11. Token lifecycle (Slice 2 design)

**Proposed.** No minting path exists yet. Tests insert token rows directly, and `CreateUserAPIToken` refuses service accounts.

### Issuance

A new store operation, `CreateServiceAccountToken(ctx, requesterID, serviceUserID, name)`, must in one transaction:

1. Read the requester's role and require the system Owner (decision A).
2. Require the requester to be human.
3. Read the target and **require `is_service = 1`**. Every human target is refused. This is the check the maintainer called out as preventing impersonation.
4. Reuse the existing `sb_` generation and SHA-256 hashing, by extracting them from `CreateUserAPIToken` rather than copying them.
5. Store only the hash and return the plaintext exactly once.

A service account's own credential cannot pass step 1, since its role is always `user`, or step 2. `TestServiceAccount_CannotProvisionCredentials` pins the current behaviour.

### Rotation and revocation

There is no special rotation operation: mint a new token, move the integration, revoke the old one. Revocation reuses `api_tokens.revoked_at`, and must verify the token's owner is a service account so that it cannot revoke a human's tokens. It takes effect on the next lookup, because `GetUserByAPIToken` reads the row on every request.

### Deletion

**Implemented through `DeleteUser`, and tested.** In one transaction it refuses non-Owners, refuses a sole Maintainer (section 10), archives token metadata, and deletes the account. Tokens, memberships, webhooks, and sessions cascade.

- Every token stops working at commit, including several active ones.
- A lookup racing the deletion sees either the account before the commit or nothing after it.
- No credential outlives the account it belongs to.
- History stays. `audit_events` keeps the actor ID, `todos.created_by_user_id` is cleared by its existing foreign key, and the todos themselves remain.
- An external integration still holding a token gets `AUTH_REQUIRED` on its next call.

### What revocation guarantees for a request in flight

Authentication happens once, at the start of a request. Neither revocation nor deletion interrupts a request that has already authenticated. What it can still do depends on when its authorization check runs relative to the commit:

| The request's authorization check runs | After **token revocation** | After **account deletion** |
|---|---|---|
| Next request, at authentication | Refused | Refused |
| Same request, check after the commit | **Still allowed.** Revocation removes no membership, so the request keeps its project authority until it ends | Refused. The membership is gone |
| Same request, check before the commit, on the mutation's own transaction | Allowed | Refused: the write fails against the stale snapshot, or the transactions are serialized and the check runs after the commit |
| Same request, check before the commit, outside the mutation's transaction | Allowed | **The write can land.** One mutation, by a request authorized before the commit |

`TestTokenRevocation_EndsAuthenticationNotAnInFlightRequest` pins the second row. The limit in the last row is real, and it is an existing pattern that applies to humans too.

- **Checked on the mutation's own transaction:** todo create, update, move, delete, archive, and links; member add, remove, and role change; account deletion and role change; service-account creation; project deletion, whose check moved into its transaction in this slice because it is irreversible and decision C grants it to service accounts.
- **Checked by the application layer or a preflight read, before the mutation's transaction:** project rename, image, and settings; sprints, tags, workflow columns, priorities, and custom fields. Several of these store methods take no actor at all. Moving them means a change to the mutation contract across the store, which this slice does not attempt.

The window is one request wide, and a later request is refused at authentication. No API-token path holds a long-lived connection: MCP has no streaming transport, and REST and its event streams do not accept bearer tokens.

### Archive provenance

`DeleteUser` archives only `api_tokens.is_service = 1` rows, so a service account's token minted with the flag off would leave no record.

**Proposed:**

- Mint every service-account token with `api_tokens.is_service = 1`. This reuses the archive and keeps the flag's documented meaning: an automation token whose metadata is kept.
- When Slice 2 adds minting, add `origin_user_is_service` to `archived_service_api_tokens`. Today a reader could only tell a service account's record by the placeholder email domain, and this design does not infer identity from email.
- Add a `created_by_user_id` to `api_tokens`, so a record shows which human minted the token.

The archive never holds a hash, so an archived token can never authenticate. `origin_user_email` holds the placeholder, which is accurate.

## 12. Audit

### Current state

`audit_events` is append-only and instrumented at the store through `insertAuditEventTx`, with 22 call sites. **`project_id` is `NOT NULL`**, so it records project-scoped actions only. Account and token lifecycle events have no project and cannot be stored there today.

### Instance-level events

**Accepted (decision E), not built.** Instance-level events go in a small dedicated stream, rather than rebuilding `audit_events` solely to allow a nullable `project_id`. Project-scoped events stay where they are. No audit infrastructure or redesign is part of this slice.

**Shared conventions**, so both streams read the same way:

- The same event shape as [audit-trail.md](../audit-trail.md): actor, past-tense action, target type, target ID, JSON metadata from a Go map, and a Unix-millisecond timestamp generated in Go.
- Append-only, enforced by triggers.
- The actor and target are not foreign keys, so history outlives the account. Service identities are identified by their stable `public_id`.
- The events to record: account creation and deletion, and token minting and revocation, with actor, target public ID, token ID, and time. A refused deletion changes nothing and records nothing.

**Transactional guarantee:** when the stream is enabled, an event is written on the same transaction as the action it records, through one helper in the style of `insertAuditEventTx`. They commit or roll back together, so a failed audit write fails the action rather than leaving it unrecorded.

**Opt-in behaviour:** the stream may be turned off. With it off, the action still commits and nothing is written. Authorization, revocation, and deletion must never read the stream or depend on it. The existing project-scoped table stays always-on. Whether the instance stream has a configuration flag is a Slice 2 question, and this slice adds none.

**Secret redaction:** metadata is built from an explicit allowlist of keys, never from a whole struct. It never contains a plaintext token, token hash, password or hash, signing secret, session or OAuth credential, or 2FA material. A test must assert this over the serialized row, as the archive's tests already do for token hashes.

Membership changes affecting a service account are already recorded by the existing `member_added`, `member_removed`, and `member_role_changed` events.

## 13. Webhooks (Slice 3 design)

**Accepted (decision F), not built.** Nothing is implemented. Registering a webhook stays session-only.

### Existing behaviour to preserve

- `webhooks.user_id` is the owner. Registration requires Maintainer, checked at the route.
- Delivery re-checks that the owner is still a Maintainer, both when enqueuing and again at send time (`GetWebhookDeliveryTarget`).
- There is a limit of 20 per project.
- URLs are checked at registration, and addresses are refused at dial time by `safehttp`.
- An owner's webhooks cascade away with the account.

### Four separate questions

| Question | Answer |
|---|---|
| Who may register? | An actor who is a Maintainer **of that project**. System Admin gets no bypass |
| Who owns the subscription? | The identity it is registered for, in `webhooks.user_id`. Its lifetime follows that identity |
| Who receives events? | The URL. It is an endpoint, not an identity, and authenticates by the signing secret |
| What permits delivery? | The project, through the owner's current membership |

### The problem

Delivery requires the owner to be a Maintainer, so a bot that only needs events would need the authority to delete the project. The maintainer called out exactly this.

### Accepted model

1. Add `webhooks.registered_by_user_id`, recording the actor separately from the owner.
2. A project Maintainer may register a subscription **owned by a service account that is a member of that project**, at any role.
3. Delivery eligibility becomes: a human owner must be a Maintainer (unchanged), a service-account owner must be a member at Viewer or above. A Viewer can already read what events contain, so this grants nothing new.
4. Both enqueue-time and send-time checks keep applying, in `ListWebhooksByProject` and `GetWebhookDeliveryTarget`.
5. A service Maintainer may register for itself. A service account below Maintainer cannot register anything.
6. Maintainers may delete subscriptions owned by a service account on their project. Today `DeleteWebhook` is owner-only.

SSRF checks, limits, signing, and delivery guarantees do not change. The limit check should move into the insert's transaction, since MCP makes concurrent registration easier.

### When authority is later revoked

Registration authority and delivery authority are separate. Registration is checked once, when it happens. Delivery is checked against the owner on every event.

| What changes | Subscription row | Delivery |
|---|---|---|
| The registering Maintainer loses the role, leaves, or is deleted | Stays. `registered_by_user_id` is cleared on account deletion, and history keeps the event | Continues. The owner's membership authorizes delivery, and other Maintainers can delete the subscription |
| The service owner's role changes, staying at Viewer or above | Stays | Continues |
| The service owner is removed from the project | Stays, inert | Stops at the next enqueue or send check. A request already being dialled may complete. Missed events are not replayed. Re-adding the account resumes delivery, the same as a human owner today |
| The service owner's account is deleted | Cascades away | Stops |
| The service owner's API token is revoked | Stays | Continues. Delivery uses the signing secret and the owner's membership, and never a token |
| The project is deleted | Cascades away | Stops |
| A human owner falls below Maintainer | Stays, inert (existing) | Stops (existing) |

## 14. Migration, compatibility, and rollback

**Forward:** migrations 080 and 081 add a column with a default and create triggers. Migration 080 carries the column, the identity CHECK, and 14 triggers; migration 081 carries the two `project_members` owner-role triggers. Every existing row becomes a human account, and existing data is not rewritten. A database already migrated through 080 receives the 081 protection through ordinary forward migration, and 081 is idempotent, so databases that already have those triggers apply it without error.

**`recover-owner`** deliberately runs without migrating, on any schema from migration 049. `RecoverOwnerPassword` checks whether the column exists and does not reference it on older databases.

**Existing humans on `@service.invalid`** remain ordinary accounts. They can sign in, reset passwords, and receive mail. Only *new* human accounts on that domain are refused, to prevent confusion with real service accounts. It is not a security control.

**Backups:** JSON exports contain no `users` rows, so they cannot create a service account. A file-level backup carries the schema and all of this with it.

**Rollback:** migrations are forward-only.

- An older binary running on a migrated database keeps the CHECK and triggers, so it still cannot give a service account a session, a password, or project ownership.
- It would not apply the link-board check, and could attempt mail to a placeholder.
- SQLite cannot drop `is_service`, because the CHECK refers to it. To truly reverse this, restore a pre-upgrade file backup.
- `recover-owner` on an older binary refuses a schema it does not recognize. This is existing behaviour.

**Migration 080 is immutable.** Its committed definition does not change. The two `project_members` owner-role triggers that review added after the commit ship in migration 081 instead, so no database is rebuilt and migration history stays consistent.

### Behaviour changes for humans

| Change | Why | Effect |
|---|---|---|
| `UpdateUserRole` checks in its transaction | Fixes the zero-Owner race | Same errors. A concurrent writer on another connection can now produce a busy error instead of acting on stale data. Production uses one connection |
| Member mutations check in their transaction | Fixes stale roles and the zero-Maintainer race | Same errors, same condition |
| `SetUserTwoFactor` on a missing user | Needed so the guard can report refusal | Returns `ErrNotFound` instead of silently succeeding. Only tests call it directly |
| New accounts on `@service.invalid` refused | See above | `ErrValidation` |
| OIDC signup asserting a `@service.invalid` address | Same check | Generic sign-in error instead of `link_required` |
| Deleting a human who is a project's sole Maintainer is refused | Enforces boundary 6 | `409 CONFLICT` instead of success. If the human also owns the project, `409` replaces the generic internal foreign-key error |
| `DeleteProject` checks authority on its transaction | Closes a stale-authority window on an irreversible action | Same errors. A concurrent writer on another connection can produce a busy error. Production uses one connection |

## 15. Performance

Measured with Go benchmarks on the store, baseline commit against the working tree, on a busy machine, so treat small differences as noise. Rows marked *median* are the median of 6 alternating runs. Rows marked *min* are the fastest of 3 runs per tree.

| Operation | Baseline | Working tree | Measure | Notes |
|---|---|---|---|---|
| Session lookup | 39.8 µs | 39.9 µs | min | Hot path. No extra query |
| API token lookup | 33 µs | 37 µs | median | Hot path for MCP. One more column, no extra query |
| Load durable project | 15.7 µs | 14.9 µs | min | No extra work |
| Load temporary board, identity recorded | 14.9 µs | 15.3 µs | median | What HTTP and MCP requests do |
| Load temporary board, nothing recorded | 14.9 µs | 22.8 µs | median | Direct and background callers only |
| List projects | 339 µs | 336 µs | min | The stale-creator filters were removed |
| Add and remove a member | 392 µs | 363 µs | median | Moving reads into the transaction did not cost time |
| Create session | 68 µs | 112 µs | median | Noisy. One trigger lookup. Login is dominated by bcrypt |
| Create project | 484 µs | 583 µs | min | One trigger lookup and one policy check |

No trigger fires on routine updates. `sessions.last_seen_at`, token `last_used_at`, and project activity updates do not name a guarded column, and `api_tokens` has no trigger.

The sole-Maintainer check, the restored owner-role triggers, and the transactional `DeleteProject` check were measured separately, commit `2158a138` against the working tree, fastest of 3 alternating runs. The disk-backed database uses `synchronous=FULL`, so absolute numbers are higher than above and are not comparable with them.

| Operation | `2158a138` | Working tree | Notes |
|---|---|---|---|
| Add and remove a member | 1.68 ms | 1.68 ms | The owner-role triggers do no lookup unless the role is `owner` |
| Delete an account holding 20 Maintainer roles | 1.05 ms | 1.11 ms | One indexed query in the deletion transaction. A rare administrative action |
| API token lookup | 144 µs | 140 µs | Unchanged path |
| Project role check | 29 µs | 28 µs | Unchanged path |

## 16. Tradeoffs and open decisions

**Accepted tradeoffs**

- Triggers are less visible than Go code. They are kept only where one rule covers many write sites atomically, and tests fail if any go missing.
- The context identity adds a concept. It is one small value following the existing envelope, and it removes a query from every human request on an expiring board.
- Stale-read tests are deterministic. `statement_hook_test.go` wraps the SQLite driver in test code only, and commits a competing change at an exact statement, with no production hook and no sleep. Stress tests that race real goroutines remain alongside them, and only check invariants that must hold in every interleaving. One grace-period sleep remains in the HTTP mail tests: it guards a negative assertion on an asynchronous worker, so it cannot fail falsely, and only its power to detect a regression depends on timing.

**Remaining risks**

1. A sole Maintainer cannot be deleted without project-level action (section 10). There is no administrator override, by design, until Slice 2 gives an Owner the means to act.
2. Some project mutations authorize before their own transaction (section 11). This is an existing pattern, and limited to one request in flight.
3. Token revocation does not interrupt a request that has already authenticated (section 11).
4. The identity-reference integrity issue (section 6) stays open until the separate remediation lands.

**Future work**

- Slice 2: `admin_createServiceAccount`, `admin_listServiceAccounts`, `admin_deleteServiceAccount`, `admin_createServiceToken`, and `admin_revokeServiceToken`.
- Add `isService` to the admin and member API projections. Today a service account appears as a user with a placeholder email.
- Update the `API.md` text saying a service token is not a service account, which will need to distinguish the two.
- Decide what `me_get` and `projects_create` report to a service token in capabilities.
- Slice 3: `webhooks_create`, `webhooks_list`, and `webhooks_delete`.
