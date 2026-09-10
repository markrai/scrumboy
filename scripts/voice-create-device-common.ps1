Set-StrictMode -Version Latest

$script:VoiceCreatePackage = 'com.markrai.scrumboy'
$script:VoiceCreateAction = 'com.markrai.scrumboy.action.VOICE_CREATE_DRY_RUN'

function Get-VoiceCreateAdbPath {
    $command = Get-Command adb.exe -ErrorAction SilentlyContinue
    if (-not $command) { $command = Get-Command adb -ErrorAction SilentlyContinue }
    if (-not $command) { throw 'adb was not found on PATH.' }
    return $command.Source
}

function Resolve-VoiceCreateAndroidSerial {
    param(
        [string]$Serial,
        [string]$AdbPath = (Get-VoiceCreateAdbPath)
    )

    $deviceOutput = & $AdbPath devices 2>&1
    if ($LASTEXITCODE -ne 0) { throw "adb devices failed: $($deviceOutput -join ' ')" }
    $devices = @(
        $deviceOutput | ForEach-Object {
            if ($_ -match '^\s*(\S+)\s+device\s*$') { $Matches[1] }
        }
    )
    $requested = if ($Serial) { $Serial.Trim() } elseif ($env:ANDROID_SERIAL) { $env:ANDROID_SERIAL.Trim() } else { '' }
    if ($requested) {
        if ($devices -notcontains $requested) { throw "Android device '$requested' is not connected and ready." }
        return $requested
    }
    if ($devices.Count -eq 0) { throw 'No ready Android device is connected.' }
    if ($devices.Count -gt 1) { throw "Multiple Android devices are connected. Pass -Serial or set ANDROID_SERIAL. Devices: $($devices -join ', ')" }
    return $devices[0]
}

function Assert-VoiceCreateDebugInstall {
    param(
        [Parameter(Mandatory = $true)][string]$AdbPath,
        [Parameter(Mandatory = $true)][string]$Serial
    )
    $packagePath = & $AdbPath -s $Serial shell pm path $script:VoiceCreatePackage 2>&1
    if ($LASTEXITCODE -ne 0 -or -not ($packagePath -match '^package:')) {
        throw "Scrumboy is not installed on Android device '$Serial'."
    }
    $runAs = & $AdbPath -s $Serial shell run-as $script:VoiceCreatePackage pwd 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "The installed Scrumboy app is not a debuggable build; the dry-run bridge is unavailable. $($runAs -join ' ')"
    }
}

function ConvertTo-VoiceCreateSafePreview {
    param(
        [AllowNull()][string]$Value,
        [ValidateRange(1, 1000)][int]$Limit = 300
    )

    if ($null -eq $Value) { return '' }
    $preview = New-Object Text.StringBuilder
    $characterCount = [Math]::Min($Value.Length, $Limit)
    for ($index = 0; $index -lt $characterCount; $index++) {
        $character = $Value[$index]
        switch ([int]$character) {
            9 { [void]$preview.Append('\t'); continue }
            10 { [void]$preview.Append('\n'); continue }
            13 { [void]$preview.Append('\r'); continue }
        }
        if ([Char]::IsControl($character)) {
            [void]$preview.Append(('\u{0:X4}' -f [int]$character))
        } else {
            [void]$preview.Append($character)
        }
    }
    if ($Value.Length -gt $Limit) { [void]$preview.Append('...') }
    return $preview.ToString()
}

function New-VoiceCreateTransportException {
    param(
        [Parameter(Mandatory = $true)][string]$RequestId,
        [Parameter(Mandatory = $true)][string]$Stage,
        [int]$EncodedLength = -1,
        [int]$DecodedByteCount = -1,
        [int]$DecodedCharacterCount = -1,
        [AllowNull()][string]$Preview,
        [AllowNull()][string]$Reason
    )

    $encodedDisplay = if ($EncodedLength -ge 0) { [string]$EncodedLength } else { 'n/a' }
    $byteDisplay = if ($DecodedByteCount -ge 0) { [string]$DecodedByteCount } else { 'n/a' }
    $characterDisplay = if ($DecodedCharacterCount -ge 0) { [string]$DecodedCharacterCount } else { 'n/a' }
    $safePreview = ConvertTo-VoiceCreateSafePreview -Value $Preview
    $safeReason = ConvertTo-VoiceCreateSafePreview -Value $Reason -Limit 200
    $message = "Voice Create dry-run transport failed: requestId=$RequestId stage=$Stage encodedLength=$encodedDisplay decodedByteCount=$byteDisplay decodedCharacterCount=$characterDisplay preview='$safePreview'"
    if ($safeReason) { $message += " reason='$safeReason'" }
    return New-Object System.FormatException($message)
}

function Test-VoiceCreateDryRunContract {
    param([Parameter(Mandatory = $true)][psobject]$Result)

    $properties = @($Result.PSObject.Properties.Name)
    return $properties -contains 'version' `
        -and $Result.version -eq 1 `
        -and $properties -contains 'mutationExecuted' `
        -and $Result.mutationExecuted -is [bool] `
        -and $Result.mutationExecuted -eq $false `
        -and $properties -contains 'confirmationReady' `
        -and $Result.confirmationReady -is [bool] `
        -and $properties -contains 'plannerCallCount' `
        -and $Result.plannerCallCount -ge 0 `
        -and $Result.plannerCallCount -le 1
}

function ConvertFrom-VoiceCreateDryRunTransport {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$EncodedOutput,
        [Parameter(Mandatory = $true)][string]$RequestId
    )

    $encoded = (($EncodedOutput | ForEach-Object { [string]$_ }) -join '').Trim()
    $encodedLength = $encoded.Length
    if (-not $encoded) {
        throw (New-VoiceCreateTransportException -RequestId $RequestId -Stage 'base64_capture' -EncodedLength $encodedLength -Preview $encoded -Reason 'The device returned an empty Base64 payload.')
    }

    try {
        $bytes = [Convert]::FromBase64String($encoded)
    } catch {
        throw (New-VoiceCreateTransportException -RequestId $RequestId -Stage 'base64_decode' -EncodedLength $encodedLength -Preview $encoded -Reason $_.Exception.Message)
    }

    $utf8 = New-Object Text.UTF8Encoding($false, $true)
    try {
        $resultJson = $utf8.GetString($bytes)
    } catch {
        throw (New-VoiceCreateTransportException -RequestId $RequestId -Stage 'utf8_decode' -EncodedLength $encodedLength -DecodedByteCount $bytes.Length -Preview '' -Reason $_.Exception.Message)
    }

    try {
        $result = $resultJson | ConvertFrom-Json
    } catch {
        throw (New-VoiceCreateTransportException -RequestId $RequestId -Stage 'json_parse' -EncodedLength $encodedLength -DecodedByteCount $bytes.Length -DecodedCharacterCount $resultJson.Length -Preview $resultJson -Reason $_.Exception.Message)
    }

    if (-not (Test-VoiceCreateDryRunContract -Result $result)) {
        throw (New-VoiceCreateTransportException -RequestId $RequestId -Stage 'contract_validation' -EncodedLength $encodedLength -DecodedByteCount $bytes.Length -DecodedCharacterCount $resultJson.Length -Preview $resultJson -Reason 'The decoded JSON does not satisfy the Voice Create dry-run v1 contract.')
    }
    return $result
}

function Invoke-VoiceCreateDryRunDevice {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Transcript,
        [string]$Serial,
        [ValidateRange(1, 120)][int]$TimeoutSeconds = 60
    )

    $adbPath = Get-VoiceCreateAdbPath
    $resolvedSerial = Resolve-VoiceCreateAndroidSerial -Serial $Serial -AdbPath $adbPath
    Assert-VoiceCreateDebugInstall -AdbPath $adbPath -Serial $resolvedSerial

    $requestId = 'vf_' + [Guid]::NewGuid().ToString('N')
    $resultPath = "files/voice-create-dry-run/$requestId.json"
    $transcriptBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Transcript))
    $modelTimeoutMs = [Math]::Min($TimeoutSeconds * 1000, 120000)
    $launchOutput = & $adbPath -s $resolvedSerial shell am start `
        -n "$script:VoiceCreatePackage/.MainActivity" `
        -a $script:VoiceCreateAction `
        --es requestId $requestId `
        --es transcriptBase64 $transcriptBase64 `
        --ei timeoutMs $modelTimeoutMs 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Could not launch the dry-run request: $($launchOutput -join ' ')" }

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds + 15)
    $readyMarker = "__VOICE_CREATE_DRY_RUN_READY__$requestId"
    $remoteReadCommand = "if [ -f $resultPath ]; then echo $readyMarker; base64 $resultPath; fi"
    $removeResultFile = $true
    try {
        while ([DateTime]::UtcNow -lt $deadline) {
            $transportOutput = @(& $adbPath -s $resolvedSerial exec-out run-as $script:VoiceCreatePackage sh -c $remoteReadCommand 2>$null)
            if ($transportOutput.Count -gt 0) {
                try {
                    if ([string]$transportOutput[0] -ne $readyMarker) {
                        $unexpectedOutput = ($transportOutput -join '')
                        throw (New-VoiceCreateTransportException -RequestId $requestId -Stage 'base64_capture' -EncodedLength $unexpectedOutput.Length -Preview $unexpectedOutput -Reason 'The device result-ready marker was missing.')
                    }
                    return ConvertFrom-VoiceCreateDryRunTransport -EncodedOutput @($transportOutput | Select-Object -Skip 1) -RequestId $requestId
                } catch {
                    $removeResultFile = $false
                    $preservedPath = "/data/user/0/$script:VoiceCreatePackage/$resultPath"
                    throw "$($_.Exception.Message) Device result preserved at '$preservedPath'."
                }
            }
            Start-Sleep -Milliseconds 250
        }
        throw "Timed out waiting for Voice Create dry-run request '$requestId'."
    } finally {
        if ($removeResultFile) {
            & $adbPath -s $resolvedSerial shell run-as $script:VoiceCreatePackage rm -f $resultPath 2>$null | Out-Null
        }
    }
}
