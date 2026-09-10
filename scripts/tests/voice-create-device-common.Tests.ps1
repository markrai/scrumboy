$scriptRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $scriptRoot 'voice-create-device-common.ps1')

function New-TestDryRunJson {
    param([Parameter(Mandatory = $true)][string]$Transcript)

    return [ordered]@{
        version = 1
        input = $Transcript
        outcome = 'ready'
        planner = [ordered]@{ status = 'ok' }
        confirmationReady = $true
        plannerCallCount = 1
        mutationExecuted = $false
    } | ConvertTo-Json -Depth 10 -Compress
}

function ConvertTo-TestBase64Lines {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [int]$Width = 0
    )

    $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Value))
    if ($Width -le 0) { return @($encoded) }
    $lines = @()
    for ($offset = 0; $offset -lt $encoded.Length; $offset += $Width) {
        $lines += $encoded.Substring($offset, [Math]::Min($Width, $encoded.Length - $offset))
    }
    return $lines
}

Describe 'Voice Create dry-run Base64 transport' {
    It 'parses ASCII JSON' {
        $json = New-TestDryRunJson -Transcript 'Create Fred'
        $result = ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput (ConvertTo-TestBase64Lines $json) -RequestId 'ascii'
        $result.input | Should Be 'Create Fred'
    }

    It 'explicitly decodes Unicode JSON as UTF-8' {
        $json = New-TestDryRunJson -Transcript 'Create Café 東京'
        $result = ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput (ConvertTo-TestBase64Lines $json) -RequestId 'unicode'
        $result.input | Should Be 'Create Café 東京'
    }

    It 'preserves JSON punctuation' {
        $json = New-TestDryRunJson -Transcript 'Create "Fred": yes, please!'
        $result = ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput (ConvertTo-TestBase64Lines $json) -RequestId 'punctuation'
        $result.input | Should Be 'Create "Fred": yes, please!'
    }

    It 'preserves a middle dot and other non-ASCII display characters' {
        $json = New-TestDryRunJson -Transcript 'Architecture · UX — ready'
        $result = ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput (ConvertTo-TestBase64Lines $json) -RequestId 'display'
        $result.input | Should Be 'Architecture · UX — ready'
    }

    It 'joins wrapped Base64 output before decoding' {
        $json = New-TestDryRunJson -Transcript 'Create Fred and tag it Architecture'
        $lines = ConvertTo-TestBase64Lines -Value $json -Width 11
        $lines.Count | Should BeGreaterThan 1
        $result = ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput $lines -RequestId 'wrapped'
        $result.input | Should Be 'Create Fred and tag it Architecture'
    }

    It 'reports bounded diagnostics for malformed Base64' {
        try {
            ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput @('%%%not-base64%%%') -RequestId 'bad_base64'
            throw 'Expected malformed Base64 to fail.'
        } catch {
            $_.Exception.Message | Should Match 'requestId=bad_base64'
            $_.Exception.Message | Should Match 'stage=base64_decode'
            $_.Exception.Message | Should Match 'encodedLength=16'
        }
    }

    It 'reports bounded diagnostics for valid Base64 containing malformed JSON' {
        $encoded = ConvertTo-TestBase64Lines -Value '{"version":1,'
        try {
            ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput $encoded -RequestId 'bad_json'
            throw 'Expected malformed JSON to fail.'
        } catch {
            $_.Exception.Message | Should Match 'requestId=bad_json'
            $_.Exception.Message | Should Match 'stage=json_parse'
            $_.Exception.Message | Should Match 'decodedByteCount=13'
            $_.Exception.Message | Should Match 'decodedCharacterCount=13'
            $_.Exception.Message.Length | Should BeLessThan 1000
        }
    }

    It 'validates the dry-run contract after explicit UTF-8 decoding' {
        $json = New-TestDryRunJson -Transcript 'Create Fred'
        $result = ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput (ConvertTo-TestBase64Lines $json) -RequestId 'contract'
        $result.version | Should Be 1
        $result.confirmationReady | Should Be $true
        $result.plannerCallCount | Should Be 1
        $result.mutationExecuted | Should Be $false
    }
}
