# VoiceFlow diagnostic tracing

Implemented September 7, 2026. Instrumentation only; no commit was created.

## Architecture and correlation

The current board entry is `views/board.ts → voice/entry.ts → voice/agent.ts → local-agent-controller.ts → agent-loop.ts`. The model returns skill envelopes; `agent-skills.ts` resolves them into validated commands, and `agent-proposals.ts` retains, revalidates, and executes mutation batches. The older `agent-controller.ts` still contains the local interpreter/semantic resolver pipeline and is also instrumented.

`trace.ts` allocates an ID from a module-session timestamp plus a monotonically increasing counter. It is independent of native ASR operation IDs and AbortController ownership leases. The active loop retains the trace on the logical task; the semantic controller retains it across its ownership counter changes. Spoken replies, typed replies, clarification, confirmation, preflight, and execution share that ID. A finished task closes its trace exactly once; late events on that trace are suppressed. Continuation listening starts a new trace. Pending interactions deliberately keep their trace when stopping a microphone or receiving no speech leaves the task available for later confirmation.

No trace information is passed to the model or added to the product-facing command contract. Provider metadata is copied from the owned native listening event after the existing strict native transcript validation. Provider never affects resolution or execution.

## Enabling and reading

Enable the existing opt-in gate in the application's WebView storage:

```js
localStorage.setItem('scrumboy_debug_voiceflow', '1');
```

Then use:

```sh
adb logcat -s ScrumboyVoiceFlow
```

`voiceFlowDiagnostic` remains the single gated producer. Trace events are serialized into a DOM event for the separately bundled Capacitor shell. The new native plugin writes them under the existing ASR tag only in debuggable builds. Sink failures cannot interrupt commands. Native ASR lifecycle logging remains unchanged. Long Unicode lines are split into correlated `op=... part=N/total` chunks to avoid Logcat byte truncation; joining chunk payloads reconstructs the original line.

Disable with `localStorage.removeItem('scrumboy_debug_voiceflow')`. Nothing appears in normal product UI.

## Stages

| Stage | Meaning |
| --- | --- |
| `asr_final` | Owned final transcript, trimmed text, length, voice modality, optional provider. |
| `transcript_input` | Typed transcript, trimmed text, length, typed modality. |
| `interpret` | Actual interpreter kind or model envelope/skill, bounded relevant selectors/arguments, model step; failures expose stable codes or application-owned protocol reasons. The semantic controller also records its trimmed interpreter input and pending kind. |
| `resolve` | Command intent, project/todo identifiers and applied destination/title/tag/assignee; or question, choice count, information, failure status/code. Merged note bodies and complete board/tag data are omitted. |
| `safety` | Command intent, danger, exact reason, resolution phase. |
| `confirmation` | Initial required confirmation or accepted revalidation, summary, intent(s), danger/reason, batch count where applicable. |
| `execute` | Started, success, or failure; batch entries include proposal index and command intent. |
| `cancel` | User cancellation, stopped microphone, superseded acquisition, invalidation, or close; indicates retained interaction where relevant. |
| `failure` | Speech, semantic resolution, revalidation, execution or other controller failure; does not dump arbitrary exception payloads. |
| `terminal` | Exactly one final outcome for a completed/abandoned trace. |

Resolution/safety phases are `initial`, `confirmation_preflight` (active skill loop only), and `confirm_revalidation`. Terminal outcomes include success, information, cancelled_by_user/declined, microphone_stopped, speech_cancelled/speech_failure, controller_invalidated/controller_closed, stale_context, interpretation/resolution failures, confirmation_failed, execution_failure/execution_exception, refresh_failure, and dialogue-turn exhaustion. The semantic controller also preserves existing message keys for specific blocked outcomes, including `voice.status.commandChanged`.

## Exact safety and confirmation rules

| Existing rule | Danger | Diagnostic reason |
| --- | --- | --- |
| Command IR intent is `todos.delete` | true | `destructive_delete` |
| Any other supported IR intent | false | `non_delete_command` |
| Batch contains at least one dangerous command | true | `destructive_delete` (individual safety events identify the delete) |
| Batch contains no dangerous command | false | `non_delete_command` |

The non-delete group includes create, move, assign/unassign, title update, append/replace notes, add/remove tag, and open. The reason deliberately says non-delete: replacing notes can overwrite content, but the existing policy does not mark it dangerous.

There is no independent danger rule for bulk mutations, privileged operations, or cross-project moves. Mutation permissions, project scope, schema validation, stale resources, conflicting proposals, preconditions, and task limits block operations separately. They were not weakened or reclassified.

Both mutation paths still require confirmation for safe mutations too. Opening a todo runs directly. Read/information skills do not enter mutation confirmation. Batch danger remains the existing `some(command.danger)` rule. Command booleans and reasons originate from the same classification helper; batch reasons use classifications of the constituent commands.

## Confirm-time re-resolution

The active skill loop refreshes the board and re-resolves all proposals before initial confirmation, then does so again after confirmation and before the first mutation. Fingerprint equality and permissions/context checks are unchanged. Traces distinguish `confirmation_preflight` and `confirm_revalidation`; changed proposals report the existing application-owned rejection reason and end without execution. Partial batch outcomes report succeeded/unattempted counts and execution failures.

The older semantic controller re-resolves the retained semantic intent/selection or canonical candidate transcript after confirmation. It compares the fresh IR hash to the reviewed IR hash. Both resolution and safety are traced before the comparison, so a changed command remains visible and blocked.

## Example Logcat excerpts

Illustrative abbreviated excerpts; IDs and summaries depend on the session and locale. Intermediate model `finish` events and unchanged preflight fields are elided here, not from actual logging.

Safe open:

```text
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"asr_final","modality":"voice","provider":"mlkit_genai_advanced","transcript":"open Happy Birthday","transcriptLength":19}
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"interpret","step":1,"interpretationKind":"skill_call","skill":"todos.open","reference":"Happy Birthday"}
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"resolve","phase":"initial","result":"command","commandIntent":"open_todo","localId":355,"projectId":1}
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"safety","phase":"initial","commandIntent":"open_todo","danger":false,"reason":"non_delete_command"}
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"execute","result":"started","commandIntent":"open_todo"}
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"execute","result":"success","commandIntent":"open_todo"}
D/ScrumboyVoiceFlow: VF {"op":"session-1","stage":"terminal","outcome":"success","modelSteps":2,"skillCalls":1,"mutationsExecuted":0}
```

Dangerous delete, retained until confirmation:

```text
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"transcript_input","modality":"typed","transcript":"delete Happy Birthday","transcriptLength":21}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"interpret","step":1,"interpretationKind":"skill_call","skill":"todos.delete","reference":"Happy Birthday"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"resolve","phase":"initial","result":"command","commandIntent":"todos.delete","projectId":1,"localId":355}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"safety","phase":"initial","commandIntent":"todos.delete","danger":true,"reason":"destructive_delete"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"safety","phase":"confirmation_preflight","commandIntent":"todos.delete","danger":true,"reason":"destructive_delete"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"confirmation","phase":"initial","required":true,"proposalCount":1,"commandIntents":["todos.delete"],"danger":true,"reason":"destructive_delete","summary":"Delete Happy Birthday"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"resolve","phase":"confirm_revalidation","result":"command","commandIntent":"todos.delete","projectId":1,"localId":355}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"safety","phase":"confirm_revalidation","commandIntent":"todos.delete","danger":true,"reason":"destructive_delete"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"confirmation","phase":"confirm_revalidation","result":"accepted","proposalCount":1,"commandIntents":["todos.delete"],"danger":true,"reason":"destructive_delete","summary":"Delete Happy Birthday"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"execute","result":"started","proposal":1,"commandIntent":"todos.delete"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"execute","result":"success","proposal":1,"commandIntent":"todos.delete"}
D/ScrumboyVoiceFlow: VF {"op":"session-2","stage":"terminal","outcome":"success","succeededCount":1,"unattemptedCount":0}
```

Stopped microphone, no later accepted transcript or execution:

```text
D/ScrumboyVoiceFlow: VF {"op":"session-3","stage":"cancel","reason":"microphone_stopped","interactionRetained":false}
D/ScrumboyVoiceFlow: VF {"op":"session-3","stage":"terminal","outcome":"microphone_stopped"}
```

## Tests and verification

Added 12 classification tests (all supported intents and mixed batches), 12 semantic-controller trace cases, 9 active-controller trace cases, 2 trace-helper tests, 2 native provider propagation cases, 1 diagnostic bridge/isolation test, and 2 Android bridge tests. Existing ownership, cancellation, confirmation, resolver, interpreter, and batch tests remain intact.

Commands and exact final results:

- Web `npm test`: **1603 passed, 1 failed; 146 files passed, 1 failed**. Vendor/i18n/landing prechecks passed. The sole failure is the pre-existing `modules/voice/dormant-loading.test.ts:25`, expecting a direct `../voice/flow.js` import. Both the working tree and HEAD board file import `../voice/entry.js`. Neither this test nor the board entry was changed; the failure was not hidden or patched.
- Web `npx vitest run modules/voice modules/platform/speech-input-capacitor.test.ts modules/platform/voiceflow-diagnostics.test.ts`: **541 passed, 1 pre-existing dormant-loading failure** at that checkpoint. Subsequent additions are included in the full run above.
- Final affected-path check `npx vitest run modules/voice/local-agent-controller.test.ts modules/voice/agent-loop.test.ts modules/voice/agent-proposals.test.ts modules/voice/command-safety.test.ts`: **79 passed, 4 files passed**.
- Web `npx tsc --noEmit` / root `npx tsc --noEmit -p internal/httpapi/web/tsconfig.json`: **exit 0**.
- Capacitor `npm run typecheck:shell`: **exit 0**.
- Android, with `ANDROID_HOME=C:\Users\okayt\AppData\Local\Android\Sdk`: `./gradlew.bat :app:testDebugUnitTest :app:assembleDebug --console=plain`: **BUILD SUCCESSFUL**, **134 tests, 0 failures/errors/skips**, 137 actionable tasks (10 executed, 127 up-to-date). Initial attempts hit a sandbox JDK read restriction and an unset SDK location; granting build access and selecting the installed SDK resolved both.
- `git diff --check`: **exit 0**.

## Remaining visibility limits

- The basic deterministic dialog in `flow.ts` retains its existing diagnostics; the new end-to-end traces cover the current enhanced UI and the older semantic agent controller. Its shared resolver does use the centralized safety policy.
- Provider is absent if no owned native provider event supplied it. No provider is inferred.
- A pending question/confirmation has no terminal until it finishes or is abandoned. Stopping only its microphone does not falsely declare the retained task cancelled.
- Process death or logging being disabled mid-operation can prevent a terminal line. Traces are ephemeral, not persisted telemetry.
- No physical-device Logcat session was available for verification; the bridge was unit-tested and compiled into a successful debug APK.
- Native trace forwarding rejects lines over 16,000 code units. Valid bounded commands fit; exceptionally oversized rejected input is not guaranteed complete native logging.

## Exact files changed

- [docs/voiceflow-tracing.md](C:/dev/project/scrumboy/docs/voiceflow-tracing.md)
- [internal/httpapi/web/modules/platform/speech-input-capacitor.test.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/platform/speech-input-capacitor.test.ts)
- [internal/httpapi/web/modules/platform/speech-input.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/platform/speech-input.ts)
- [internal/httpapi/web/modules/platform/voiceflow-diagnostics.test.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/platform/voiceflow-diagnostics.test.ts)
- [internal/httpapi/web/modules/platform/voiceflow-diagnostics.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/platform/voiceflow-diagnostics.ts)
- [internal/httpapi/web/modules/voice/agent-controller.test.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/agent-controller.test.ts)
- [internal/httpapi/web/modules/voice/agent-controller.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/agent-controller.ts)
- [internal/httpapi/web/modules/voice/agent-loop.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/agent-loop.ts)
- [internal/httpapi/web/modules/voice/agent-proposals.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/agent-proposals.ts)
- [internal/httpapi/web/modules/voice/agent-skills.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/agent-skills.ts)
- [internal/httpapi/web/modules/voice/command-safety.test.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/command-safety.test.ts)
- [internal/httpapi/web/modules/voice/command-safety.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/command-safety.ts)
- [internal/httpapi/web/modules/voice/local-agent-controller.test.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/local-agent-controller.test.ts)
- [internal/httpapi/web/modules/voice/local-agent-controller.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/local-agent-controller.ts)
- [internal/httpapi/web/modules/voice/resolve.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/resolve.ts)
- [internal/httpapi/web/modules/voice/semantic-resolver.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/semantic-resolver.ts)
- [internal/httpapi/web/modules/voice/trace.test.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/trace.test.ts)
- [internal/httpapi/web/modules/voice/trace.ts](C:/dev/project/scrumboy/internal/httpapi/web/modules/voice/trace.ts)
- [mobile/capacitor/android/app/src/main/java/com/markrai/scrumboy/MainActivity.java](C:/dev/project/scrumboy/mobile/capacitor/android/app/src/main/java/com/markrai/scrumboy/MainActivity.java)
- [mobile/capacitor/android/app/src/main/java/com/markrai/scrumboy/ScrumboyVoiceFlowPlugin.java](C:/dev/project/scrumboy/mobile/capacitor/android/app/src/main/java/com/markrai/scrumboy/ScrumboyVoiceFlowPlugin.java)
- [mobile/capacitor/android/app/src/test/java/com/markrai/scrumboy/ScrumboyVoiceFlowPluginTest.java](C:/dev/project/scrumboy/mobile/capacitor/android/app/src/test/java/com/markrai/scrumboy/ScrumboyVoiceFlowPluginTest.java)
- [mobile/capacitor/shell/bootstrap.ts](C:/dev/project/scrumboy/mobile/capacitor/shell/bootstrap.ts)
- [mobile/capacitor/shell/speech-input-capability.ts](C:/dev/project/scrumboy/mobile/capacitor/shell/speech-input-capability.ts)
- [mobile/capacitor/shell/voiceflow-diagnostics.ts](C:/dev/project/scrumboy/mobile/capacitor/shell/voiceflow-diagnostics.ts)
