param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $CommandArguments
)

$sourceExecutable = $CommandArguments[0]
$runExecutable = "$sourceExecutable.c4run.exe"
Copy-Item -LiteralPath $sourceExecutable -Destination $runExecutable -Force

$testArguments = if ($CommandArguments.Count -gt 1) {
    $CommandArguments[1..($CommandArguments.Count - 1)]
} else {
    @()
}

& $runExecutable @testArguments
$testExitCode = $LASTEXITCODE

for ($attempt = 0; $attempt -lt 20; $attempt++) {
    try {
        Remove-Item -LiteralPath $runExecutable -Force -ErrorAction Stop
        break
    } catch {
        Start-Sleep -Milliseconds 250
    }
}

exit $testExitCode
