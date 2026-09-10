[CmdletBinding()]
param(
    [string]$Serial,
    [string]$Cases = (Join-Path $PSScriptRoot 'fixtures\voice-create-gauntlet.json'),
    [string]$ReportPath,
    [ValidateRange(1, 120)][int]$TimeoutSeconds = 60
)

. (Join-Path $PSScriptRoot 'voice-create-device-common.ps1')

function Get-OptionalProperty($value, [string]$name) {
    if ($null -eq $value) { return $null }
    $property = $value.PSObject.Properties[$name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function Get-DryRunFailureCode($result) {
    $planner = Get-OptionalProperty $result 'planner'
    if ((Get-OptionalProperty $planner 'status') -eq 'failed') { return [string](Get-OptionalProperty $planner 'code') }
    $preparation = Get-OptionalProperty $result 'preparation'
    if ((Get-OptionalProperty $preparation 'status') -eq 'failed') { return [string](Get-OptionalProperty $preparation 'code') }
    return $null
}

function Test-DryRunExpectation($result, $expectation) {
    $failures = [System.Collections.Generic.List[string]]::new()
    $expectedReady = Get-OptionalProperty $expectation 'confirmationReady'
    if ($null -ne $expectedReady -and [bool](Get-OptionalProperty $result 'confirmationReady') -ne [bool]$expectedReady) {
        $failures.Add("confirmationReady expected $expectedReady, got $(Get-OptionalProperty $result 'confirmationReady')")
    }
    $expectedOutcome = Get-OptionalProperty $expectation 'outcome'
    $actualOutcome = Get-OptionalProperty $result 'outcome'
    if ($expectedOutcome -and $actualOutcome -ne $expectedOutcome) {
        $failures.Add("outcome expected $expectedOutcome, got $actualOutcome")
    }
    $expectedErrorCode = Get-OptionalProperty $expectation 'errorCode'
    if ($expectedErrorCode) {
        $actualCode = Get-DryRunFailureCode $result
        if ($actualCode -ne $expectedErrorCode) { $failures.Add("errorCode expected $expectedErrorCode, got $actualCode") }
    }
    $preparation = Get-OptionalProperty $result 'preparation'
    $expectedLane = Get-OptionalProperty $expectation 'lane'
    $actualLane = Get-OptionalProperty (Get-OptionalProperty $preparation 'lane') 'name'
    if ($expectedLane -and $actualLane -ne $expectedLane) {
        $failures.Add("lane expected $expectedLane, got $actualLane")
    }
    $expectedAssignee = Get-OptionalProperty $expectation 'assignee'
    $actualAssignee = Get-OptionalProperty (Get-OptionalProperty $preparation 'assignee') 'name'
    if ($expectedAssignee -and $actualAssignee -ne $expectedAssignee) {
        $failures.Add("assignee expected $expectedAssignee, got $actualAssignee")
    }
    $expectedTagValue = Get-OptionalProperty $expectation 'tags'
    if ($null -ne $expectedTagValue) {
        $expectedTags = @($expectedTagValue) -join '|'
        $actualTags = @((Get-OptionalProperty $preparation 'tags')) -join '|'
        if ($actualTags -ne $expectedTags) { $failures.Add("tags expected $expectedTags, got $actualTags") }
    }
    $mutationExecuted = Get-OptionalProperty $result 'mutationExecuted'
    $plannerCallCount = Get-OptionalProperty $result 'plannerCallCount'
    if ($mutationExecuted -ne $false) { $failures.Add('mutationExecuted was not false') }
    if ($plannerCallCount -gt 1) { $failures.Add("plannerCallCount exceeded one: $plannerCallCount") }
    return @($failures)
}

try {
    $fixturePath = (Resolve-Path -LiteralPath $Cases -ErrorAction Stop).Path
    $fixture = Get-Content -LiteralPath $fixturePath -Raw | ConvertFrom-Json
    if ($fixture.version -ne 1 -or -not $fixture.cases) { throw 'Gauntlet fixture must contain version 1 and a non-empty cases array.' }
    $results = [System.Collections.Generic.List[object]]::new()
    foreach ($case in $fixture.cases) {
        [Console]::Error.WriteLine("Voice Create dry-run: $($case.id)")
        try {
            $result = Invoke-VoiceCreateDryRunDevice -Transcript ([string]$case.transcript) -Serial $Serial -TimeoutSeconds $TimeoutSeconds
            $failures = Test-DryRunExpectation $result $case.expect
            $status = if ($failures.Count -eq 0) { 'passed' } else { 'failed' }
            $resultOutcome = Get-OptionalProperty $result 'outcome'
            $classification = if ($status -eq 'passed' -and $resultOutcome -eq 'blocked') { 'blocked_expected' } elseif ($status -eq 'passed') { $resultOutcome } else { 'expectation_failed' }
            $results.Add([pscustomobject]@{ id = $case.id; status = $status; classification = $classification; failures = $failures; result = $result })
        } catch {
            $results.Add([pscustomobject]@{ id = $case.id; status = 'failed'; classification = 'harness_failed'; failures = @($_.Exception.Message); result = $null })
        }
    }
    $summary = [pscustomobject]@{
        cases = $results.Count
        passed = @($results | Where-Object status -eq 'passed').Count
        failed = @($results | Where-Object status -eq 'failed').Count
        ready = @($results | Where-Object { (Get-OptionalProperty $_.result 'outcome') -eq 'ready' }).Count
        correctlyBlocked = @($results | Where-Object classification -eq 'blocked_expected').Count
        plannerFailures = @($results | Where-Object { (Get-OptionalProperty $_.result 'outcome') -eq 'planner_failed' }).Count
        resolutionFailures = @($results | Where-Object { (Get-OptionalProperty $_.result 'outcome') -eq 'resolution_failed' }).Count
        mutations = @($results | Where-Object { $null -ne $_.result -and (Get-OptionalProperty $_.result 'mutationExecuted') -ne $false }).Count
    }
    $report = [pscustomobject]@{ version = 1; fixture = $fixturePath; summary = $summary; cases = $results }
    $json = $report | ConvertTo-Json -Depth 30
    if ($ReportPath) {
        $absoluteReport = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($ReportPath)
        [IO.File]::WriteAllText($absoluteReport, $json, [Text.UTF8Encoding]::new($false))
    }
    $json
    if ($summary.mutations -ne 0) { exit 2 }
    if ($summary.failed -ne 0) { exit 1 }
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
