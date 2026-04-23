$ErrorActionPreference = "Stop"

$Repo = if ($env:REPO) { $env:REPO } else { "lamht09/claude-account-switcher" }
$Version = if ($env:VERSION) { $env:VERSION } else { "latest" }
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { "$HOME\.local\bin" }

if ($Version -eq "latest") {
  $apiUrl = "https://api.github.com/repos/$Repo/releases/latest"
  $apiHeaders = @{}
  if ($env:GITHUB_TOKEN) {
    $apiHeaders["Authorization"] = "Bearer $($env:GITHUB_TOKEN)"
  }
  $release = Invoke-RestMethod -Uri $apiUrl -Headers $apiHeaders
  $Version = $release.tag_name
}

# Prefer WOW64 hint when 32-bit PowerShell runs on 64-bit Windows.
$procArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($procArch.ToLower()) {
  "amd64" { "amd64" }
  "x86_64" { "amd64" }
  "arm64" { "arm64" }
  default { "amd64" }
}

$asset = "ca_windows_$arch.zip"
# Zip layout matches Makefile release-local (binary named ca_windows_<arch>.exe).
$exeInArchive = "ca_windows_$arch.exe"
$base = "https://github.com/$Repo/releases/download/$Version"
$tmp = New-Item -ItemType Directory -Path ([System.IO.Path]::GetTempPath()) -Name ("ca-install-" + [guid]::NewGuid())

Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile "$tmp\SHA256SUMS"
Invoke-WebRequest -Uri "$base/$asset" -OutFile "$tmp\$asset"

$expected = (Get-Content "$tmp\SHA256SUMS" | Where-Object { $_ -match $asset }).Split(" ")[0]
$actual = (Get-FileHash "$tmp\$asset" -Algorithm SHA256).Hash.ToLower()
if ($actual -ne $expected.ToLower()) {
  throw "Checksum mismatch for $asset"
}

Expand-Archive -Path "$tmp\$asset" -DestinationPath $tmp -Force
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item "$tmp\$exeInArchive" "$InstallDir\ca.exe" -Force
Write-Host "Installed ca.exe to $InstallDir\ca.exe"

function Test-PathListContainsDir {
  param(
    [string]$PathList,
    [string]$DirFull
  )
  if ([string]::IsNullOrWhiteSpace($PathList)) { return $false }
  $want = $DirFull.TrimEnd('\')
  foreach ($piece in ($PathList -split ';', [StringSplitOptions]::RemoveEmptyEntries)) {
    $t = $piece.Trim().Trim('"')
    if (-not $t) { continue }
    try {
      $norm = [System.IO.Path]::GetFullPath($t).TrimEnd('\')
      if ($norm.Equals($want, [StringComparison]::OrdinalIgnoreCase)) { return $true }
    } catch { }
  }
  return $false
}

$skipPath = $env:SKIP_PATH
if ($skipPath -eq "1" -or $skipPath -eq "true" -or $skipPath -eq "yes") {
  Write-Host "Skipping user PATH update (SKIP_PATH set)."
} else {
  $binDir = [System.IO.Path]::GetFullPath($InstallDir).TrimEnd('\')
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (-not (Test-PathListContainsDir -PathList $userPath -DirFull $binDir)) {
    $newUserPath = if ([string]::IsNullOrWhiteSpace($userPath)) { $binDir } else { $userPath.TrimEnd(';') + ";" + $binDir }
    [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    Write-Host "Added install directory to user PATH: $binDir"
  } else {
    Write-Host "Install directory already on user PATH: $binDir"
  }

  $procPath = if ($null -eq $env:Path) { "" } else { $env:Path }
  if (-not (Test-PathListContainsDir -PathList $procPath -DirFull $binDir)) {
    $env:Path = $procPath.TrimEnd(';') + ";" + $binDir
    Write-Host "Updated PATH for this PowerShell session."
  }
  Write-Host "Open a new terminal (or restart Cursor/VS Code) if another app should pick up PATH."
}
