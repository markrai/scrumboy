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

    # Prevent PowerShell from unrolling an empty/singleton list into $null/a scalar.
    return ,$failures
}

function New-VoiceCreateGauntletReport($fixturePath, [int]$requestedCases, $results, $stopCondition) {
    $summary = [pscustomobject]@{
        requested = $requestedCases
        cases = $results.Count
        passed = @($results | Where-Object status -eq 'passed').Count
        failed = @($results | Where-Object status -eq 'failed').Count
        ready = @($results | Where-Object { (Get-OptionalProperty $_.result 'outcome') -eq 'ready' }).Count
        correctlyBlocked = @($results | Where-Object classification -eq 'blocked_expected').Count
        plannerFailures = @($results | Where-Object { (Get-OptionalProperty $_.result 'outcome') -eq 'planner_failed' }).Count
        resolutionFailures = @($results | Where-Object { (Get-OptionalProperty $_.result 'outcome') -eq 'resolution_failed' }).Count
        mutations = @($results | Where-Object { $null -ne $_.result -and (Get-OptionalProperty $_.result 'mutationExecuted') -ne $false }).Count
        completed = $results.Count -eq $requestedCases -and $null -eq $stopCondition
    }
    return [pscustomobject]@{
        version = 1
        fixture = $fixturePath
        summary = $summary
        stopCondition = $stopCondition
        cases = $results
    }
}

function Write-VoiceCreateGauntletReport($report, [string]$path) {
    if (-not $path) { return }
    $absoluteReport = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($path)
    $parent = [IO.Path]::GetDirectoryName($absoluteReport)
    if ($parent -and -not [IO.Directory]::Exists($parent)) {
        [IO.Directory]::CreateDirectory($parent) | Out-Null
    }
    $temporary = "$absoluteReport.$([Guid]::NewGuid().ToString('N')).tmp"
    $backup = "$absoluteReport.$([Guid]::NewGuid().ToString('N')).bak"
    try {
        $json = $report | ConvertTo-Json -Depth 30
        [IO.File]::WriteAllText($temporary, $json, [Text.UTF8Encoding]::new($false))
        if ([IO.File]::Exists($absoluteReport)) {
            [IO.File]::Replace($temporary, $absoluteReport, $backup)
        } else {
            [IO.File]::Move($temporary, $absoluteReport)
        }
    } finally {
        if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
        if ([IO.File]::Exists($backup)) { [IO.File]::Delete($backup) }
    }
}

function Invoke-VoiceCreateGauntlet {
    [CmdletBinding()]
    param(
        [string]$Serial,
        [Parameter(Mandatory = $true)][string]$Cases,
        [string]$ReportPath,
        [ValidateRange(1, 120)][int]$TimeoutSeconds = 60,
        [scriptblock]$DryRunInvoker
    )

    if ($null -eq $DryRunInvoker) {
        $DryRunInvoker = {
            param($currentTranscript, $currentSerial, $currentTimeoutSeconds)
            Invoke-VoiceCreateDryRunDevice -Transcript $currentTranscript -Serial $currentSerial -TimeoutSeconds $currentTimeoutSeconds
        }
    }

    $fixturePath = (Resolve-Path -LiteralPath $Cases -ErrorAction Stop).Path
    $fixture = Get-Content -LiteralPath $fixturePath -Raw | ConvertFrom-Json
    $fixtureCases = @($fixture.cases)
    if ($fixture.version -ne 1 -or $fixtureCases.Count -eq 0) {
        throw 'Gauntlet fixture must contain version 1 and a non-empty cases array.'
    }

    $results = [System.Collections.Generic.List[object]]::new()
    $stopCondition = $null
    foreach ($case in $fixtureCases) {
        [Console]::Error.WriteLine("Voice Create dry-run: $($case.id)")
        try {
            $result = & $DryRunInvoker ([string]$case.transcript) $Serial $TimeoutSeconds
            $failures = Test-DryRunExpectation $result $case.expect
            $status = if ($failures.Count -eq 0) { 'passed' } else { 'failed' }
            $resultOutcome = Get-OptionalProperty $result 'outcome'
            $classification = if ($status -eq 'passed' -and $resultOutcome -eq 'blocked') { 'blocked_expected' } elseif ($status -eq 'passed') { $resultOutcome } else { 'expectation_failed' }
            $results.Add([pscustomobject]@{ id = $case.id; status = $status; classification = $classification; failures = $failures; result = $result })

            $planner = Get-OptionalProperty $result 'planner'
            if ((Get-OptionalProperty $planner 'status') -eq 'failed' -and (Get-OptionalProperty $planner 'code') -eq 'planner_timeout') {
                $stopCondition = [pscustomobject]@{
                    type = 'provider_timeout'
                    code = 'planner_timeout'
                    caseId = [string]$case.id
                    transcript = [string]$case.transcript
                    stage = [string](Get-OptionalProperty $planner 'stage')
                    plannerCallCount = Get-OptionalProperty $result 'plannerCallCount'
                }
            }
        } catch {
            $message = $_.Exception.Message
            $results.Add([pscustomobject]@{ id = $case.id; status = 'failed'; classification = 'harness_failed'; failures = @($message); result = $null })
            if ($message -like 'Timed out waiting for Voice Create dry-run request*') {
                $stopCondition = [pscustomobject]@{
                    type = 'harness_result_publication_timeout'
                    code = 'result_publication_timeout'
                    caseId = [string]$case.id
                    transcript = [string]$case.transcript
                    message = $message
                }
            }
        }

        $partialReport = New-VoiceCreateGauntletReport $fixturePath $fixtureCases.Count $results $stopCondition
        Write-VoiceCreateGauntletReport $partialReport $ReportPath
        if ($null -ne $stopCondition) { break }
    }

    $report = New-VoiceCreateGauntletReport $fixturePath $fixtureCases.Count $results $stopCondition
    Write-VoiceCreateGauntletReport $report $ReportPath
    return $report
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        $report = Invoke-VoiceCreateGauntlet -Serial $Serial -Cases $Cases -ReportPath $ReportPath -TimeoutSeconds $TimeoutSeconds
        $report | ConvertTo-Json -Depth 30
        if ($report.summary.mutations -ne 0) { exit 2 }
        if ($report.summary.failed -ne 0 -or $null -ne $report.stopCondition) { exit 1 }
    } catch {
        [Console]::Error.WriteLine($_.Exception.Message)
        exit 1
    }
}
