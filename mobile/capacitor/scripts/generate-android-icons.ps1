# Regenerates Android launcher mipmaps from the PWA icon.
# Source of truth: internal/httpapi/web/icon-512.png
# Canvas/safe-zone padding uses the source artwork's corner gray (not white).
# Also refreshes values/ic_launcher_background.xml to that same gray for the adaptive
# icon / splash icon-disk background only (not the full-screen splash). Does not write local.properties.

$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$sourcePath = Join-Path $repoRoot "internal\httpapi\web\icon-512.png"
$resRoot = Join-Path $repoRoot "mobile\capacitor\android\app\src\main\res"
$backgroundColorPath = Join-Path $resRoot "values\ic_launcher_background.xml"

if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
    throw "PWA icon not found at '$sourcePath'."
}

$densities = @(
    @{ Name = "mdpi";    Launcher = 48;  Foreground = 108 }
    @{ Name = "hdpi";    Launcher = 72;  Foreground = 162 }
    @{ Name = "xhdpi";   Launcher = 96;  Foreground = 216 }
    @{ Name = "xxhdpi";  Launcher = 144; Foreground = 324 }
    @{ Name = "xxxhdpi"; Launcher = 192; Foreground = 432 }
)

function Get-SourceCanvasColor {
    param([System.Drawing.Bitmap]$Source)

    $corner = $Source.GetPixel(0, 0)
    $checks = @(
        @{ X = $Source.Width - 1; Y = 0 }
        @{ X = 0; Y = $Source.Height - 1 }
        @{ X = $Source.Width - 1; Y = $Source.Height - 1 }
    )
    foreach ($check in $checks) {
        $pixel = $Source.GetPixel($check.X, $check.Y)
        if ($pixel.ToArgb() -ne $corner.ToArgb()) {
            throw ("Launcher background color is derived from uniform source-image corners; review the source artwork. Corners differ: (0,0)=({0},{1},{2}) vs ({3},{4})=({5},{6},{7})." -f `
                $corner.R, $corner.G, $corner.B, $check.X, $check.Y, $pixel.R, $pixel.G, $pixel.B)
        }
    }
    return $corner
}

function New-HighQualityBitmap {
    param(
        [System.Drawing.Image]$Source,
        [int]$Size,
        [System.Drawing.Color]$CanvasColor,
        [double]$ContentScale = 1.0
    )

    $dest = New-Object System.Drawing.Bitmap $Size, $Size
    $dest.SetResolution($Source.HorizontalResolution, $Source.VerticalResolution)
    $graphics = [System.Drawing.Graphics]::FromImage($dest)
    try {
        $graphics.Clear($CanvasColor)
        $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
        $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality

        $contentSize = [int][Math]::Round($Size * $ContentScale)
        $offset = [int][Math]::Round(($Size - $contentSize) / 2)
        $destRect = New-Object System.Drawing.Rectangle $offset, $offset, $contentSize, $contentSize
        $srcRect = New-Object System.Drawing.Rectangle 0, 0, $Source.Width, $Source.Height
        $graphics.DrawImage($Source, $destRect, $srcRect, [System.Drawing.GraphicsUnit]::Pixel)
    } finally {
        $graphics.Dispose()
    }
    return $dest
}

function Save-Png {
    param(
        [System.Drawing.Bitmap]$Bitmap,
        [string]$Path
    )

    $directory = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $directory)) {
        throw "Expected mipmap directory missing: '$directory'."
    }
    $Bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
}

function Save-LauncherBackgroundColor {
    param(
        [System.Drawing.Color]$Color,
        [string]$Path
    )

    $hex = "#{0:X2}{1:X2}{2:X2}" -f $Color.R, $Color.G, $Color.B
    $xml = @"
<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="ic_launcher_background">$hex</color>
</resources>
"@
    # Normalize to LF and ensure trailing newline for Android resource XML.
    $normalized = ($xml -replace "`r`n", "`n").TrimEnd() + "`n"
    [System.IO.File]::WriteAllText($Path, $normalized)
    return $hex
}

$sourceImage = [System.Drawing.Image]::FromFile($sourcePath)
try {
    $sourceBitmap = New-Object System.Drawing.Bitmap $sourceImage
    try {
        $canvasColor = Get-SourceCanvasColor -Source $sourceBitmap
        $backgroundHex = Save-LauncherBackgroundColor -Color $canvasColor -Path $backgroundColorPath
        Write-Host ("Sampled source canvas color RGB=({0},{1},{2}) {3}" -f $canvasColor.R, $canvasColor.G, $canvasColor.B, $backgroundHex)
        Write-Host "Updated $backgroundColorPath"

        foreach ($density in $densities) {
            $dir = Join-Path $resRoot ("mipmap-" + $density.Name)

            $launcher = New-HighQualityBitmap -Source $sourceImage -Size $density.Launcher -CanvasColor $canvasColor
            try {
                Save-Png -Bitmap $launcher -Path (Join-Path $dir "ic_launcher.png")
                Save-Png -Bitmap $launcher -Path (Join-Path $dir "ic_launcher_round.png")
            } finally {
                $launcher.Dispose()
            }

            # Adaptive foreground safe zone is the inner 66dp of the 108dp canvas.
            $foreground = New-HighQualityBitmap -Source $sourceImage -Size $density.Foreground -CanvasColor $canvasColor -ContentScale (66.0 / 108.0)
            try {
                Save-Png -Bitmap $foreground -Path (Join-Path $dir "ic_launcher_foreground.png")
            } finally {
                $foreground.Dispose()
            }

            Write-Host ("Wrote mipmap-{0} launcher {1}px and foreground {2}px" -f $density.Name, $density.Launcher, $density.Foreground)
        }
    } finally {
        $sourceBitmap.Dispose()
    }
} finally {
    $sourceImage.Dispose()
}

Write-Host "Android launcher icons regenerated from $sourcePath"
