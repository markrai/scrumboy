# VoiceFlow

Voice commands are project-scoped. Everything you say applies to the project you are currently viewing.

## Basic Rules

* “story” and “todo” mean the same thing
* You can use the number directly (e.g. “open 12”)
* Commands must be clear and complete (no guessing)
* Each command stands alone (no “move it”)

## Create

* create story "login page"
* create todo "fix bug"

## Move / Update Status

* move story 12 to in progress
* move todo 8 to done
* story 5 is in progress
* todo 3 is done

## Open / Edit

* open story 12
* edit todo 7
* open 12
* edit 7

## Delete

* delete story 13
* delete 13

## Assign

* assign story 10 to john
* assign todo 4 to sarah

## Status Words

* to do
* in progress
* done

## Modes

### Safe-Mode (default)

* Shows what was understood
* You confirm actions in the UI
* Deletes always require confirmation

### Hands-Free

* Starts listening immediately when mic is pressed
* Uses voice confirmations instead of UI
* You will hear: “Delete story 13. Confirm?”
* Respond with:

  * yes
  * no

Only “yes” or “no” will be accepted during confirmation.

## Tips

* Be explicit: “move 12 to done” works, “move 12” does not
* Use numbers clearly: “twelve” or “one two” both work
* If unclear, the command will be rejected instead of guessed
* All actions stay within the current project
