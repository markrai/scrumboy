# Regenerates Android launcher mipmaps from the PWA icon.
# Source of truth: internal/httpapi/web/icon-512.png
# Does not touch splash drawables or write local.properties.

$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$sourcePath = Join-Path $repoRoot "internal\httpapi\web\icon-512.png"
$resRoot = Join-Path $repoRoot "mobile\capacitor\android\app\src\main\res"

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

function New-HighQualityBitmap {
    param(
        [System.Drawing.Image]$Source,
        [int]$Size,
        [double]$ContentScale = 1.0
    )

    $dest = New-Object System.Drawing.Bitmap $Size, $Size
    $dest.SetResolution($Source.HorizontalResolution, $Source.VerticalResolution)
    $graphics = [System.Drawing.Graphics]::FromImage($dest)
    try {
        $graphics.Clear([System.Drawing.Color]::White)
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

$source = [System.Drawing.Image]::FromFile($sourcePath)
try {
    foreach ($density in $densities) {
        $dir = Join-Path $resRoot ("mipmap-" + $density.Name)

        $launcher = New-HighQualityBitmap -Source $source -Size $density.Launcher
        try {
            Save-Png -Bitmap $launcher -Path (Join-Path $dir "ic_launcher.png")
            Save-Png -Bitmap $launcher -Path (Join-Path $dir "ic_launcher_round.png")
        } finally {
            $launcher.Dispose()
        }

        # Adaptive foreground safe zone is the inner 66dp of the 108dp canvas.
        $foreground = New-HighQualityBitmap -Source $source -Size $density.Foreground -ContentScale (66.0 / 108.0)
        try {
            Save-Png -Bitmap $foreground -Path (Join-Path $dir "ic_launcher_foreground.png")
        } finally {
            $foreground.Dispose()
        }

        Write-Host ("Wrote mipmap-{0} launcher {1}px and foreground {2}px" -f $density.Name, $density.Launcher, $density.Foreground)
    }
} finally {
    $source.Dispose()
}

Write-Host "Android launcher icons regenerated from $sourcePath"
