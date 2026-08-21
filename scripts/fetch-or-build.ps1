# herdr [[build]] step (Windows). Download the prebuilt herdr-gh-checks.exe matching this
# manifest's version for the host arch (verify SHA-256) into ./herdr-gh-checks.exe. On any miss
# fall back to `go build` — mirror of scripts/fetch-or-build.sh.
$ErrorActionPreference = "Stop"
$bin = "herdr-gh-checks"
$repo = "itisbryan/herdr-gh-checks"
$version = (Select-String -Path herdr-plugin.toml -Pattern '^version = "(.*)"').Matches[0].Groups[1].Value
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

function Build-FromSource {
  if (Get-Command go -ErrorAction SilentlyContinue) {
    Write-Host "herdr-gh-checks: building from source with go"
    go build -trimpath -ldflags "-s -w" -o "$bin.exe" .
    exit $LASTEXITCODE
  }
  Write-Error "herdr-gh-checks: no prebuilt binary for windows/$arch and Go is not installed. Install Go 1.25+ or use WSL."
  exit 1
}

if (-not $version) { Build-FromSource }
$asset = "$bin-windows-$arch.exe"
$base = "https://github.com/$repo/releases/download/v$version"
$tmp = New-TemporaryFile
try {
  Invoke-WebRequest -Uri "$base/$asset" -OutFile $tmp -UseBasicParsing
  $want = (Invoke-WebRequest -Uri "$base/$asset.sha256" -UseBasicParsing).Content.Trim()
  $got = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLower()
  if ($want -eq $got) {
    Move-Item -Force $tmp "$bin.exe"
    Write-Host "herdr-gh-checks: installed prebuilt $asset v$version"
    exit 0
  }
  Write-Warning "herdr-gh-checks: checksum mismatch for $asset; falling back to source"
} catch {
  Write-Warning "herdr-gh-checks: download failed ($_); falling back to source"
}
Build-FromSource
