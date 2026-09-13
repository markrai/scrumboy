# Enhanced VoiceFlow (AI VoiceFlow)

AI VoiceFlow is the enhanced VoiceFlow experience on supported Android devices.
It combines on-device speech recognition, on-device AI planning through Gemini Nano
/ ML Kit Prompt infrastructure, and Scrumboy server actions that run only after
you confirm mutations.

This page describes the shipped AI VoiceFlow surface. For Basic VoiceFlow
(deterministic command grammar, Safe-Mode / Hands-Free), see
[voiceflow.md](voiceflow.md).

## What it is

On capable English Android installs, opening VoiceFlow can present **AI VoiceFlow**
instead of the Basic VoiceFlow dialog.

AI VoiceFlow:

* listens with native Android / ML Kit on-device speech recognition
* plans create and other board requests on device
* shows a review when a mutation is prepared
* sends confirmed create/update/delete work to your configured Scrumboy server

AI VoiceFlow does not use a Scrumboy cloud AI service for planning. Browser and
PWA sessions stay on Basic VoiceFlow.

## Availability

AI VoiceFlow is available when all of the following are true:

* you are running the Capacitor Android app (not browser/PWA)
* the app locale is English (the enhanced VoiceFlow path is English-only today)
* the device exposes both **local text generation** and **speech input** capabilities
* both capabilities report **ready**

Otherwise:

* missing or unsupported capabilities → Scrumboy opens **Basic VoiceFlow**
* capabilities exist but are still preparing, busy, or temporarily unavailable →
  Scrumboy shows the AI VoiceFlow not-ready surface with **Use basic commands**

Browser and PWA runtimes do not advertise the enhanced capability path, so they
always use Basic VoiceFlow. Exact device models vary by Android, AICore, and ML Kit
readiness; Scrumboy gates on capability status rather than a hard-coded model list.

The board microphone also requires VoiceFlow to be enabled in
**Settings → Customization → VoiceFlow**, a durable project, and a project role
that can use VoiceFlow.

## Device support

AI VoiceFlow does not use a hard-coded phone allowlist.

Scrumboy checks the capabilities of the device at runtime. AI VoiceFlow opens only when both on-device text generation and speech input report ready. If those capabilities are unsupported, Scrumboy uses Basic VoiceFlow instead; if they are supported but still preparing, Scrumboy shows the AI VoiceFlow not-ready surface.

For on-device planning, Scrumboy uses the ML Kit **Prompt API** backed by Gemini Nano. Google maintains the current list of devices supported by the ML Kit **Prompt API**, currently grouped by Gemini Nano generation. A device appearing on that list means Google currently lists it as supported for the Prompt API, but it does not guarantee that AI VoiceFlow will be ready at a particular moment. AICore configuration, model availability, Android software state, and other device conditions can affect runtime readiness.

See Google’s current [ML Kit GenAI device support](https://developers.google.com/ml-kit/genai) for the authoritative Prompt API device list.

### Speech support

AI VoiceFlow can use two levels of on-device speech recognition:

* **Standard on-device speech:** available on many Android devices running API level 31 or later. This path normally completes the turn on the first finalized result.
* **Advanced GenAI speech:** available only on a smaller set of devices identified by Google. When available, Scrumboy can combine multiple finalized speech segments and use the configurable **Wait after I stop speaking** grace period.

Scrumboy detects the available speech path automatically. There is no provider selector.

Because Google expands and updates these device lists over time, Scrumboy treats runtime capability detection as authoritative rather than maintaining its own static phone list.

> **Important:** Google documents ML Kit GenAI APIs as unsupported on devices with an unlocked bootloader. A phone may therefore appear on Google's hardware compatibility list while its on-device GenAI capability is unavailable because of its current system configuration.

## How it differs from Basic VoiceFlow

| | **Basic VoiceFlow** | **AI VoiceFlow** |
| --- | --- | --- |
| Planning | Deterministic English command grammar | On-device Create planner or skill agent |
| Speech input | Web Speech path | Native Android / ML Kit speech |
| Modes | Safe-Mode / Hands-Free | Review and confirmation before mutations |
| Capture | First finalized result ends the turn (shorter window) | Up to 45 seconds; advanced recognition can keep listening across finalized segments |
| Continuation toggle | **Continue conversation** | **Keep Listening** |

Basic VoiceFlow details remain in [voiceflow.md](voiceflow.md).

## Opening AI VoiceFlow

On a supported ready device, the board **VoiceFlow** microphone opens the floating
**AI VoiceFlow** panel.

From that panel you can:

* **Listen** / **Stop**
* confirm or cancel a prepared review
* enable **Keep Listening**
* choose **Use basic commands** to leave AI VoiceFlow and open Basic VoiceFlow

### When AI VoiceFlow is not ready

If on-device speech or interpretation is preparing or needs attention, Scrumboy
still opens an AI VoiceFlow shell, but listening stays unavailable until readiness
recovers. Use **Use basic commands** to continue with Basic VoiceFlow.

## Speaking to Scrumboy

Speak naturally about the project you currently have open. VoiceFlow stays
project-scoped; it does not switch projects from speech.

While AI VoiceFlow prepares and reviews a request, the captured transcript remains
visible so you can see what was heard.

### Listening window

Each enhanced capture has an absolute limit of **45 seconds**.

On devices where advanced ML Kit speech recognition is active:

* Scrumboy can combine multiple finalized speech segments into one request
* meaningful speech after a final can extend the turn until the absolute limit

If recognition falls back to the platform on-device recognizer, the first finalized
result ends the turn.

If the 45-second ceiling expires without an accepted completed utterance, Scrumboy
fails closed. Partial speech is not promoted into a completed request.

### Wait after I stop speaking

**Settings → Customization → VoiceFlow → Wait after I stop speaking** controls how
long Scrumboy waits after a finalized segment before treating the turn as done
(when advanced multi-segment recognition is in use).

Options:

* **Fast — 2 seconds**
* **Normal — 4 seconds** (default)
* **Patient — 7 seconds**

The preference is device-local, applies to AI VoiceFlow, and is sampled for each
new speech acquisition. Meaningful speech after a final can reset the grace period
within the 45-second absolute limit.

## What you can ask

### Creating a todo

Scrumboy automatically recognizes likely new-todo requests (for example clear
“create …” / “add a story …” shapes) and routes them through a dedicated Create
planner. There is no separate create-mode switch in the UI.

Supported create details:

* **title** (required)
* optional **lane**
* optional **assignee** (one person)
* up to **5** existing project **tags**
* **notes** up to **1000** characters

Defaults when a field is omitted:

* lane → the leftmost lane on the current board
* assignee → unassigned
* tags / notes → none / empty

If you name a lane, person, or tag that cannot be resolved cleanly, Scrumboy does
**not** silently substitute a default. Incomplete or unsupported create requests
fail closed instead of guessing. Planning never mutates the board; the create runs
only after confirmation, as one enriched create action.

### Other actions

Requests that are not clear create candidates use the AI VoiceFlow skill agent.
At a product level, that includes opening or inspecting todos, moving and renaming,
updating notes, assigning or unassigning people, adding or removing tags, deleting,
creating when the agent path prepares it, and counting completed work for this week.

The agent can sequence more than one prepared mutation from a single request, then
ask for one combined confirmation before any write runs. Opening and bounded
read-style actions may complete immediately without a mutation confirmation.

## Confirmation and safety

Mutations require confirmation. Scrumboy owns that confirmation UI and spoken
yes/no handling; the on-device model does not replace Scrumboy’s safety checks.

Before a confirmed write runs, Scrumboy refreshes and revalidates the reviewed
work. If the board or resolved identities changed enough to invalidate consent,
confirmation is rejected and you start again.

For multi-action agent batches:

* execution follows the prepared order
* a failure stops the remaining actions
* earlier completed actions are not rolled back automatically

Destructive deletes are visually distinguished (confirm label **Delete** when the
only dangerous action is a delete).

### Spoken confirmation

On the dedicated Create path, clear whole-utterance yes / no / cancel replies
confirm or cancel the pending review. Mixed or qualified replies (for example
“yes, but assign someone else”) do not rewrite the pending create; Scrumboy asks
for a direct yes/no clarification, or you cancel and restate the full request.

On the general skill-agent path, a qualified reply that adds more work can be
treated as a revised request and produce a **new** full confirmation instead of
executing the old batch as-is.

You can also tap **Confirm** / **Delete** or **Cancel** in the panel.

### Ambiguous people and tags

AI VoiceFlow may ask for clarification when:

* more than one person matches an assignee phrase
* a spoken tag needs confirmation or a close-match suggestion

Choosing among people is a local clarification step; selecting a choice does not
by itself call the on-device planner again. Explicit unresolved people or tags are
not silently replaced with a guess.

## Keep Listening

**Keep Listening** is off by default. When enabled, a successful spoken terminal
result can open **one** additional bounded listening turn for the next request.

Clarification and confirmation listening can still start after Scrumboy speaks,
even when Keep Listening is off. Closing AI VoiceFlow, changing project context,
or signing out clears in-progress conversation state.

Do not confuse **Keep Listening** with Basic VoiceFlow’s **Continue conversation**
toggle. They are different UI labels on different surfaces; see
[voiceflow.md](voiceflow.md) for the Basic control.

## Speech settings

Open **Settings → Customization → VoiceFlow**.

### Speech speed

**Speech speed** controls how fast Scrumboy speaks during VoiceFlow:

* **1.0x** (default)
* **1.25x**
* **1.50x**
* **1.75x**
* **2.0x**

AI VoiceFlow samples the speech-rate preference for each new spoken utterance.
The preference is stored on this device only.

### Wait after I stop speaking

See [Wait after I stop speaking](#wait-after-i-stop-speaking) above. The control
appears on Capacitor Android when VoiceFlow is enabled.

## Privacy and processing

Implementation-backed boundaries for AI VoiceFlow:

* enhanced speech recognition uses on-device Android / ML Kit speech capabilities
* AI planning runs on-device through Gemini Nano / ML Kit Prompt / AICore
  infrastructure
* enhanced spoken replies use local Android text-to-speech voices (network-required
  voices are not selected)
* todo mutations are sent to the configured Scrumboy server after confirmation
* Scrumboy does not persist the VoiceFlow conversation or transcript as chat history
* some VoiceFlow preferences sync with your account; **Speech speed** and
  **Wait after I stop speaking** are device-local

## Troubleshooting

* AI VoiceFlow unavailable on this environment → Scrumboy uses **Basic VoiceFlow**.
* AI VoiceFlow is preparing or temporarily unavailable → use **Use basic commands**,
  or wait and try again when on-device speech/interpretation is ready.
* Some devices need Android / AICore / ML Kit components to finish downloading or
  updating before local generation or advanced speech becomes ready.
* Speech recognition or local TTS availability can differ by device and language
  pack; if speaking fails, tap Confirm/Cancel in the panel when a review is shown.
* “I didn’t hear a response” / timeout means no completed utterance was accepted
  within the capture window; try again, speak sooner, or choose a longer
  **Wait after I stop speaking** preset when using advanced multi-segment capture.

## For maintainers

### Experience selection

`selectVoiceFlowExperience` order:

1. non-English locale → Basic (`legacy-deterministic`)
2. required capabilities absent or `unsupported` → Basic
3. both local text generation and speech input `ready` → Enhanced (`enhanced-agent`)
4. supported but preparing / busy / status error → Enhanced not-ready shell
   (`enhanced-not-ready`) with **Use basic commands**

Speech output is optional for opening Enhanced; missing TTS skips speak but does
not force Basic by itself. Capabilities are registered from the Capacitor shell
bootstrap; browser/PWA runtimes do not register them.

### Speech providers

Automatic selection only; no user-facing provider picker.

* ML Kit GenAI speech when supported/ready
* Android on-device `SpeechRecognizer` fallback otherwise

Enhanced capture does not fall back to Web Speech ASR. Provider IDs appear in
diagnostics only.

### Create planner and skill agent

Enhanced VoiceFlow currently has two planning engines behind one surface:

* deterministic create-candidate routing (`isVoiceCreateCandidate`) →
  `voice-create-plan-v1`
* other requests → `voice-agent-v15`

There is no separate classifier model before either engine. The removed
`scrumboy_voice_create_v2` preference is ignored and must not be documented as a
product mode.

### Capture behavior

Enhanced listens use a 45-second ceiling and sample
`getEnhancedSpeechWaitMs()` per acquisition. The listen options may still pass
internal aggregation mode `create_v2`. That string is an implementation
identifier only and is **not** a current user-selectable mode.

### Preferences and persistence

| Concern | Scope | Notes |
| --- | --- | --- |
| VoiceFlow enabled | Synced | `scrumboy.voiceFlowEnabled` / `voiceFlowEnabled` |
| Keep Listening / Continue conversation | Shared synced boolean | Same preference backs both UI labels (`scrumboy.voiceFlowContinueConversation` / `voiceFlowContinueConversation`) |
| Speech speed | Device-local | `scrumboy.voiceSpeechRate` |
| Wait after I stop speaking | Device-local | `scrumboy.voiceEnhancedSpeechWait` (`fast` / `normal` / `patient`) |

Safe-Mode / Hands-Free prefs apply only to Basic VoiceFlow.

### Tests and tracing

Useful areas: `experience-selection`, `enhanced-voice-session`,
`local-agent-controller`, `voice-create-*`, `agent-loop` / `agent-proposals`,
platform speech I/O Capacitor tests, and Android speech / local-AI unit tests under
`mobile/capacitor`.

For opt-in Logcat diagnostics, see [voiceflow-tracing.md](voiceflow-tracing.md).
Do not treat that guide as user documentation.

## Related documentation

* [voiceflow.md](voiceflow.md) — Basic VoiceFlow grammar, Safe-Mode / Hands-Free, Continue conversation
* [voiceflow-tracing.md](voiceflow-tracing.md) — maintainer diagnostics
* [diagrams/scrumboy_voiceflow.md](diagrams/scrumboy_voiceflow.md) — pipeline overview (may lag Enhanced Create routing)
* [mobile/capacitor/README.md](../mobile/capacitor/README.md) — Capacitor Android shell
