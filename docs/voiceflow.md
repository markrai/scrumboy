# VoiceFlow

Voice commands are project-scoped. Everything you say applies to the project you are currently viewing.

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
* Each command stands alone (no “move it”)
* **Pronouns are not supported** for targets: phrases like “it”, “that”, “this one” are rejected
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
