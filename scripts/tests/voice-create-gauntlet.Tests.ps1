$scriptRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $scriptRoot 'voice-create-gauntlet.ps1')

function New-TestGauntletResult {
    param(
        [bool]$ConfirmationReady = $true,
        [string]$Outcome = 'ready',
        [int]$PlannerCallCount = 1,
        [bool]$MutationExecuted = $false,
        [string]$PlannerCode,
        [string]$PlannerStage = 'planner_invocation'
    )
    $planner = if ($PlannerCode) {
        [pscustomobject]@{ status = 'failed'; stage = $PlannerStage; code = $PlannerCode }
    } else {
        [pscustomobject]@{ status = 'ok' }
    }
    return [pscustomobject]@{
        version = 1
        outcome = $Outcome
        planner = $planner
        confirmationReady = $ConfirmationReady
        plannerCallCount = $PlannerCallCount
        mutationExecuted = $MutationExecuted
    }
}

function Write-TestGauntletFixture {
    param([Parameter(Mandatory = $true)][string]$Path)
    [pscustomobject]@{
        version = 1
        cases = @(
            [pscustomobject]@{ id = 'one'; transcript = 'one'; expect = [pscustomobject]@{ confirmationReady = $true } }
            [pscustomobject]@{ id = 'two'; transcript = 'two'; expect = [pscustomobject]@{ confirmationReady = $true } }
            [pscustomobject]@{ id = 'three'; transcript = 'three'; expect = [pscustomobject]@{ confirmationReady = $true } }
        )
    } | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $Path -Encoding UTF8
}

Describe 'Voice Create gauntlet expectation collections' {
    It 'returns a real empty collection for zero failures' {
        $failures = Test-DryRunExpectation (New-TestGauntletResult) ([pscustomobject]@{ confirmationReady = $true })
        $null -eq $failures | Should Be $false
        $failures.Count | Should Be 0
    }

    It 'returns a real singleton collection for one failure' {
        $failures = Test-DryRunExpectation (New-TestGauntletResult -ConfirmationReady $false) ([pscustomobject]@{ confirmationReady = $true })
        $failures.Count | Should Be 1
        $failures[0] | Should Match 'confirmationReady expected'
    }

    It 'retains multiple failures and enforces planner/mutation invariants' {
        $result = New-TestGauntletResult -ConfirmationReady $false -PlannerCallCount 2 -MutationExecuted $true
        $failures = Test-DryRunExpectation $result ([pscustomobject]@{ confirmationReady = $true })
        $failures.Count | Should Be 3
        ($failures -join '|') | Should Match 'mutationExecuted was not false'
        ($failures -join '|') | Should Match 'plannerCallCount exceeded one'
    }
}

Describe 'Voice Create gauntlet stop and publication behavior' {
    BeforeEach {
        $script:fixturePath = Join-Path $TestDrive 'cases.json'
        $script:reportPath = Join-Path $TestDrive 'report.json'
        Write-TestGauntletFixture $script:fixturePath
        $script:launches = [System.Collections.Generic.List[string]]::new()
    }

    It 'runs every case when there are no failures and persists the final report' {
        $report = Invoke-VoiceCreateGauntlet -Cases $script:fixturePath -ReportPath $script:reportPath -DryRunInvoker {
            param($transcript, $serial, $timeoutSeconds)
            $script:launches.Add($transcript)
            New-TestGauntletResult
        }
        $report.summary.cases | Should Be 3
        $report.summary.failed | Should Be 0
        $report.summary.completed | Should Be $true
        $script:launches.Count | Should Be 3
        (Get-Content -LiteralPath $script:reportPath -Raw | ConvertFrom-Json).summary.cases | Should Be 3
    }

    It 'records a structured planner timeout, persists the partial report, and launches no later cases' {
        $report = Invoke-VoiceCreateGauntlet -Cases $script:fixturePath -ReportPath $script:reportPath -DryRunInvoker {
            param($transcript, $serial, $timeoutSeconds)
            $script:launches.Add($transcript)
            New-TestGauntletResult -ConfirmationReady $false -Outcome 'planner_failed' -PlannerCode 'planner_timeout'
        }
        $report.summary.cases | Should Be 1
        $report.summary.completed | Should Be $false
        $report.stopCondition.type | Should Be 'provider_timeout'
        $report.stopCondition.code | Should Be 'planner_timeout'
        $script:launches.Count | Should Be 1
        $persisted = Get-Content -LiteralPath $script:reportPath -Raw | ConvertFrom-Json
        $persisted.summary.cases | Should Be 1
        $persisted.stopCondition.type | Should Be 'provider_timeout'
    }

    It 'distinguishes an outer result publication timeout and launches no later cases' {
        $report = Invoke-VoiceCreateGauntlet -Cases $script:fixturePath -ReportPath $script:reportPath -DryRunInvoker {
            param($transcript, $serial, $timeoutSeconds)
            $script:launches.Add($transcript)
            throw "Timed out waiting for Voice Create dry-run request 'request-one'."
        }
        $report.summary.cases | Should Be 1
        $report.summary.failed | Should Be 1
        $report.stopCondition.type | Should Be 'harness_result_publication_timeout'
        $report.stopCondition.code | Should Be 'result_publication_timeout'
        $report.cases[0].classification | Should Be 'harness_failed'
        $script:launches.Count | Should Be 1
        (Get-Content -LiteralPath $script:reportPath -Raw | ConvertFrom-Json).stopCondition.type | Should Be 'harness_result_publication_timeout'
    }
}
