[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)][AllowEmptyString()][string]$Transcript,
    [string]$Serial,
    [ValidateRange(1, 120)][int]$TimeoutSeconds = 60
)

. (Join-Path $PSScriptRoot 'voice-create-device-common.ps1')

try {
    $result = Invoke-VoiceCreateDryRunDevice -Transcript $Transcript -Serial $Serial -TimeoutSeconds $TimeoutSeconds
    $result | ConvertTo-Json -Depth 20 -Compress
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
