$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$Version = if ($env:VERSION) { $env:VERSION } else { "0.7.0" }
$LdFlags = "-s -w -X wol/internal/version.Version=$Version"

$EmbeddedKey = $env:WOL_EMBEDDED_KEY
if (-not $EmbeddedKey -and (Test-Path ".wol-embedded-key")) {
    $EmbeddedKey = (Get-Content ".wol-embedded-key" -Raw).Trim()
}
if ($EmbeddedKey) {
    if ($EmbeddedKey -notmatch '^[0-9A-Fa-f]{64}$') {
        throw "WOL_EMBEDDED_KEY must be 64 hex characters"
    }
    $LdFlags += " -X wol/internal/secrets.EmbeddedKey=$EmbeddedKey"
    Write-Host "==> embedded secrets key: enabled"
} else {
    Write-Warning "no embedded secrets key; using key file fallback"
}

Write-Host "==> building host binary (version $Version)"
New-Item -ItemType Directory -Force -Path bin, dist | Out-Null
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags $LdFlags -o "bin/wol.exe" ./cmd/wol

$targets = @(
  @("windows", "amd64"),
  @("windows", "arm64"),
  @("linux", "amd64"),
  @("linux", "arm64"),
  @("linux", "arm"),
  @("linux", "mipsle"),
  @("darwin", "amd64"),
  @("darwin", "arm64")
)

foreach ($target in $targets) {
  $os = $target[0]
  $arch = $target[1]
  $ext = if ($os -eq "windows") { ".exe" } else { "" }
  $output = "dist/wol-$os-$arch$ext"
  Write-Host "==> $os/$arch -> $output"
  $env:GOOS = $os
  $env:GOARCH = $arch
  $env:GOARM = "7"
  $env:GOMIPS = "softfloat"
  go build -trimpath -ldflags $LdFlags -o $output ./cmd/wol
}

Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:GOARM -ErrorAction SilentlyContinue
Remove-Item Env:GOMIPS -ErrorAction SilentlyContinue
Write-Host "==> done"
Get-ChildItem bin, dist | Format-Table Name, Length
