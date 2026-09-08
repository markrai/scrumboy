# VoiceFlow

Voice commands are project-scoped. Everything you say applies to the project you are currently viewing.

## On-device AI VoiceFlow

### Experimental Create v2

On English, enhanced-capable devices, the VoiceFlow mode selector offers **Create v2 (experimental)**. It persists the explicit local selection in `scrumboy_voice_create_v2` (`1` enables it); **All commands** selects the existing agent. Switching mode closes and invalidates the current interaction before listening again. No classifier model runs before the create planner, and non-create output does not automatically replay through the old agent. Select All commands for moves, updates, deletes and other existing skills.

The `voice-create-plan-v1` prompt makes one request through the existing Nano generation bridge. It extracts one create with optional lane, singular assignee, existing tags and literal notes. Unspecified fields stay omitted in model output. Scrumboy supplies the current project, canonical leftmost lane, unassigned, no tags and empty notes. An explicit unresolved field never falls back to a default. Missing title or material unsupported content blocks the entire create. Duplicate people produce deterministic choices; selecting a choice does not call Nano.

The final ASR transcript remains visible through preparation and review. Review text comes from the resolved command and includes the lane and supplied assignee/tags/notes, without listing empty defaults. Planning does not open todos or call mutation APIs. Confirmation refreshes and re-resolves the reviewed fields; a changed leftmost default, resolved identity or context invalidates consent. One enriched `todos_create` carries all fields in the existing server create transaction. No follow-up assign/tag/notes requests run.

Whole-utterance `yes`, `yep`, `confirm`, `go ahead`, `do it`, and `yes please` confirm only a pending review. `no`, `cancel`, `never mind` and `stop` cancel. Mixed replies such as “yes but assign Sarah instead” invalidate the pending review and ask for a complete restatement; natural revision is not implemented. Confirmation adds no Nano generation. A lost execution response is reported as failed or unconfirmed and is not automatically retried.

The existing 256-output-token generation budget is unchanged. This experiment bounds input to 2,000 code units, notes to 1,000 and tags to five; malformed/oversized output fails closed with no repair loop. These are parser limits, not a guarantee that every maximal request fits the token budget. Full extraction accuracy and output truncation still require physical Nano testing. The focused synthetic evaluation cases are in `internal/httpapi/web/scripts/voice-create-evaluation.json`; mocked tests prove orchestration, not physical-model accuracy. Diagnostic traces add `planner_start` and `plan` metadata without raw model output or duplicated notes.

### Existing agent

With All commands selected, AI VoiceFlow uses the existing domain-conditioned skill loop. Nano chooses one bounded Scrumboy skill at a time and can sequence several actions from one request. Todo, story, card, task and item identify the same entity; software-like titles such as **Bird's Eye View**, **Settings**, and **Search** remain literal todo titles.

The available skills are `todos.resolve`, `todos.open`, `todos.inspect`, `todos.create`, `todos.move`, `todos.rename`, `todos.append_notes`, `todos.replace_notes`, `todos.assign`, `todos.unassign`, `todos.add_tag`, `todos.remove_tag`, `todos.delete`, and `analytics.count_completed` (this week). Opening and bounded reads can run immediately. Every mutation prepares a proposal. A complete task receives one combined confirmation, including when the user adds another action during confirmation. Natural replies such as “yeah, go ahead” and “no thanks” are interpreted locally.

Scrumboy resolves resources, enforces permission and validation, and issues task-scoped opaque handles. The model sees bounded skill results, never the board or project member/tag catalogs. All proposals are freshly checked before presenting confirmation and again before the first mutation. Execution follows proposal order. A failure stops the batch and reports succeeded, failed or unconfirmed, and unattempted operations; separate server mutations are not a transaction and are not rolled back. Overlapping writes to the same todo field and create-then-assign dependencies require separate tasks in this version.

Each task allows at most 8 model invocations (including protocol repairs), 6 skill calls, 4 mutation proposals, 5 choices per result, and 16 in-memory trace entries. Invalid output permits one local repair per step and never falls back to command parsing. Completion and invalidation clear task state. Project/account/server changes and closing VoiceFlow invalidate handles and pending proposals.

**Keep Listening**, off by default, controls the next command window. Both settings allow the current task's clarification and confirmation replies. When enabled, a spoken terminal result opens exactly one additional bounded listening window and retains only an active todo reference. Speech output finishes before speech input starts. No always-on microphone, cloud AI, persisted conversation, or second model call for factual wording is used. The stored boolean preference is unchanged.

The browser/basic path remains unchanged. The grammar, modes, and confirmation policy below describe that path.

## Locale boundary

* The surrounding Scrumboy UI follows the app locale, including the `Settings -> Customization -> VoiceFlow` toggle and nearby board chrome.
* VoiceFlow command parsing, built-in status aliases, spoken confirmations, and spoken disambiguation words remain English-centric today.
* In **Hands-Free** mode, confirmation words are still **`yes`** and **`no`**, and ambiguous-target choices are still **`one`**, **`two`**, and **`three`**.
* If you use Scrumboy in a non-English UI, **Safe-Mode** is the safer option because the command grammar does not switch with the app locale yet.

## Basic Rules

* “story” and “todo” mean the same thing
* You can target a todo by **local ID** (number) or by a **title phrase** (when the match is strong enough—see below)
* You can use the number directly (e.g. “open 12”)
* Commands must be clear and complete (no guessing)
* Explicit todo commands use an ID or title. The bounded conversational forms described below may refer to the active todo as “it”, “this todo”, or “this card”.
* **Project switching in speech is not supported** (e.g. “in project foo …”); stay on the current board

## Referencing a todo: ID vs title

### By ID

* Digits work: “12”, “#12”
* Spoken numbers work: “twelve”, “twenty three”
* Leading noise like “number” / “id” before the value is stripped when parsing an ID
* **Ambiguous digit runs** (e.g. separate digits that could mean different IDs) may be flagged; prefer unambiguous forms like “twelve” or “one two” only when you mean the digit string—when in doubt, use digits or a **title** phrase

### By title phrase

For **move**, **delete**, **open** / **edit**, **assign**, and forms like **“todo 5 is done”** / **“story login is in progress”**, anything that is **not** parsed as a numeric ID is treated as a **title search phrase**.

* Titles are **normalized** (case, punctuation, quotes, `#`, hyphens/underscores) so minor speech variants still match
* A trailing spoken **“number …”** / **“no …”** / **“num …”** plus a number in a title phrase is folded into digits (e.g. a todo literally titled “item number one” aligns with how that is normalized)
* Matching uses the **current board** plus a **project todo search** (when available) to build candidates, then **scores** them (exact title, prefix of title, ordered token overlap, etc.)
* If **one** todo is a clear winner (exact match, or a strong score gap over the runner-up), that todo is used
* If several todos tie or the match is weak, the command is **rejected as ambiguous** and you can **pick one** (see below)—nothing is executed on a guess

## Create

* create story "login page"
* create todo "fix bug"

Create v2 speech acquisition keeps one ML Kit recognition stream open across authoritative
segment finals. Each final is retained in order; a 4-second post-final grace window ends the
VoiceFlow turn after no further meaningful partial activity. The 45-second absolute ceiling
still applies, and a ceiling timeout fails closed rather than promoting unfinished partial text.
Create v2 then sends the joined authoritative segments to the planner once. All Commands keeps
its existing first-final behavior and 10-second acquisition window.

## Move / Update Status

* move story 12 to in progress
* move todo "login page" to done
* story 5 is in progress
* todo "fix billing" is done

## Open / Edit

* open story 12
* edit todo 7
* open 12
* edit 7
* open story "qa checklist"

## Delete

* delete story 13
* delete 13
* delete todo "spike old api"

## Assign

* assign story 10 to john
* assign todo 4 to sarah
* assign story "login page" to sarah

## Status Words

Built-in phrases are mapped to your board’s lanes where possible, including:

* to do
* backlog / not started
* in progress / doing
* testing
* done

Custom lane **names** and **keys** are also accepted when they resolve to a single lane.

## Modes

### Continue conversation

The **Continue conversation** toggle is off by default. When enabled, a successful turn clears the transcript and review UI while retaining only the in-memory active todo reference needed for the next bounded turn. Turning it off, closing VoiceFlow, changing project context, signing out, or restarting the process clears that conversation state.

After a todo has been opened or otherwise resolved successfully, on-device interpretation can understand **“Open it”**, **“Change the title”**, and **“Change its title to …”**. A missing title produces the question **“What would you like to change the title to?”**; the next answer is bound to the concrete pending todo, freshly revalidated, explicitly confirmed, and executed through the normal todo-update path. The pending question remains open long enough to answer even when continuation is off.

No chat transcript, todo title, project data, or pending answer is persisted. Only the boolean toggle preference may be stored.

### Safe-Mode (default)

* Shows what was understood
* You confirm actions in the UI
* Deletes always require confirmation
* When a **title match is ambiguous**, the UI lists up to **three** candidates (#id + title); pick one to continue

### Hands-Free

* Starts listening immediately when mic is pressed
* Uses spoken confirmations instead of the Safe-Mode Review/Execute UI when the confirmation policy requires it
* **Confirmation policy** (toggle under the transcript; stored as `scrumboy.voiceFlowHandsFreeConfirmation` / preference `voiceFlowHandsFreeConfirmation`):
  * **Confirm only deletes** (`deletes`, default) — spoken confirm for **delete** only; create, move, assign, and open run without a confirm prompt
  * **Confirm every action before execution** (`mutations`) — spoken confirm for **create, move, delete, and assign**; **open / edit** still runs without spoken confirm
* When confirmation is required you will hear a prompt such as: “Delete story 13. Confirm?”
* Respond with:
  * yes
  * no

Only “yes” or “no” will be accepted during confirmation.

* **Disambiguation** (pick among ambiguous title matches) is separate from action confirmation. After an ambiguous title, you can say e.g. **“one”**, **“number one”**, **“option one”** (and similarly for options 2–3), matching the spoken option list. That choice happens before any yes/no confirmation when the policy requires it.

## Tips

* Be explicit: “move 12 to done” works, “move 12” does not
* Use numbers clearly: “twelve” or “one two” both work for IDs; if the app treats digits as ambiguous, switch to a **title** or speak the ID more clearly
* For **titles**, use a phrase that is **distinctive** on the board; very short or generic words may match multiple todos
* If unclear, the command will be rejected instead of guessed
* All actions stay within the current project
