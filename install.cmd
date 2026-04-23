@echo off
setlocal ENABLEDELAYEDEXPANSION

if "%REPO%"=="" set REPO=lamht09/claude-account-switcher
if "%VERSION%"=="" set VERSION=latest
if "%INSTALL_DIR%"=="" set INSTALL_DIR=%USERPROFILE%\.local\bin
if not defined SKIP_PATH set "SKIP_PATH="

set "TMP_PS1=%TEMP%\ca-install-%RANDOM%-%RANDOM%.ps1"
curl -fsSL -o "%TMP_PS1%" "https://raw.githubusercontent.com/%REPO%/main/install.ps1"
if errorlevel 1 (
  echo Failed to download install.ps1
  exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$env:REPO='%REPO%'; $env:VERSION='%VERSION%'; $env:INSTALL_DIR='%INSTALL_DIR%'; $env:SKIP_PATH='%SKIP_PATH%'; & '%TMP_PS1%'"

if errorlevel 1 (
  del /q "%TMP_PS1%" >nul 2>&1
  echo Installation failed.
  exit /b 1
)

del /q "%TMP_PS1%" >nul 2>&1
echo Installation complete.
