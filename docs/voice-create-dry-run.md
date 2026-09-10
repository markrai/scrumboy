# Voice Create dry-run v1

This debug harness feeds text into the real experimental Create v2 semantic path on an installed Android debug build. It bypasses microphone/ASR acquisition, uses the current authenticated board and the installed Gemini Nano provider, and stops at confirmation readiness.

It cannot execute a command: the shared evaluator exposes only planner, board refresh, context, and an explicitly injected member reader. It has no generic MCP or execution capability. Its versioned result always contains `mutationExecuted: false`. The native intent and result writer reject non-debuggable builds.

## Prerequisites

- Install and launch a fresh debug APK.
- Sign in and leave the intended project board open.
- Put `adb` on `PATH`.
- With multiple devices, pass `-Serial` or set `ANDROID_SERIAL`.

## One transcript

```powershell
Set-Location C:\dev\project\scrumboy
.\scripts\voice-create-dry-run.ps1 `
  -Transcript "Create Fred and tag it Architecture"
```

Specify a device when needed:

```powershell
.\scripts\voice-create-dry-run.ps1 `
  -Serial 192.168.1.200:5555 `
  -Transcript "Create Fred and tag it U.X."
```

The script starts or reuses the app, sends a Base64-encoded transcript through an explicit debug intent, and reads the atomically published app-private result through `adb run-as` as Base64. PowerShell joins the ASCII transport lines, decodes the bytes explicitly as strict UTF-8, and then parses JSON. It does not use microphone permission or scrape Logcat.

Successful reads delete their app-private result file. A Base64, UTF-8, JSON, or contract failure preserves that file and reports bounded diagnostics containing the request ID, transport stage, payload sizes, and a safely printable preview.

## Starter gauntlet

```powershell
.\scripts\voice-create-gauntlet.ps1
```

Or select the device and retain a report:

```powershell
.\scripts\voice-create-gauntlet.ps1 `
  -Serial 192.168.1.200:5555 `
  -Cases .\scripts\fixtures\voice-create-gauntlet.json `
  -ReportPath .\voice-create-report.json
```

Cases run serially. The version 1 fixture supports only `confirmationReady`, `outcome`, `errorCode`, `lane`, `assignee`, and exact `tags` expectations. Add a case by supplying a unique `id`, a `transcript`, and an `expect` object.

## Result interpretation

The result contains the exact parsed `planner.plan` after successful parsing, then either a ready preparation or a structured failure. The main stages are:

- `input_validation`, `provider_readiness`, `planner_invocation`, `planner_parsing`, `planner_contract_validation`
- `unsupported_request_guard`, `context_authorization`
- `lane_resolution`, `member_resolution`, `tag_resolution`
- `command_ir_validation`, `preparation`, `confirmation_construction`

High-level outcomes are `ready`, `blocked`, `planner_failed`, `resolution_failed`, `validation_failed`, `context_failed`, and `unexpected_failure`. Resolver failures distinguish unknown and ambiguous lanes, members, and tags. Provider failures never fall back to cloud. Each case has a bounded timeout and reports at most one planner call.

The device evaluator races the complete semantic evaluation against its deadline. If the deadline wins before planning completes, it publishes `planner_timeout` and poisons that in-process dry-run provider session because the losing Nano promise may still be active. Later requests return `provider_session_poisoned` without starting Nano; restart the app before further evaluation. This preserves the one-active-generation invariant.

For parser-stage failures only, the debug dry-run result may contain `details.outputPreview`. It is the generated text truncated to 384 UTF-16 code units and exists only in the explicitly opted-in device dry-run planner. Normal production VoiceFlow planning and successful dry-run results do not expose generated output.
