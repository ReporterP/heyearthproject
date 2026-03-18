@echo off
REM HeYearth VPN — Windows Build & Installer Script
REM Requirements:
REM   - Flutter SDK in PATH
REM   - Inno Setup 6.x installed (iscc.exe in PATH or in default location)
REM   - xray.exe in windows\installer\xray\ directory

setlocal enabledelayedexpansion
cd /d "%~dp0"

echo ============================================
echo   HeYearth VPN — Windows Build
echo ============================================
echo.

REM Step 1: Get dependencies
echo [1/4] Getting Flutter dependencies...
flutter pub get
if %ERRORLEVEL% neq 0 (
    echo ERROR: flutter pub get failed
    exit /b 1
)

REM Step 2: Build Flutter app
echo [2/4] Building Flutter Windows app...
flutter build windows --release
if %ERRORLEVEL% neq 0 (
    echo ERROR: flutter build windows failed
    exit /b 1
)

echo.
echo Flutter build complete: build\windows\x64\runner\Release\
echo.

REM Step 3: Check for Xray
echo [3/4] Checking Xray binary...
if not exist "windows\installer\xray\xray.exe" (
    echo WARNING: xray.exe not found in windows\installer\xray\
    echo Download from: https://github.com/XTLS/Xray-core/releases
    echo Place xray.exe, geoip.dat, geosite.dat in windows\installer\xray\
    echo.
    echo Skipping installer creation. You can still use the built EXE directly.
    echo.
    goto :skip_installer
)

REM Step 4: Build installer with Inno Setup
echo [4/4] Building installer with Inno Setup...

REM Try common Inno Setup locations
set "ISCC="
if exist "%ProgramFiles(x86)%\Inno Setup 6\ISCC.exe" (
    set "ISCC=%ProgramFiles(x86)%\Inno Setup 6\ISCC.exe"
)
if exist "%ProgramFiles%\Inno Setup 6\ISCC.exe" (
    set "ISCC=%ProgramFiles%\Inno Setup 6\ISCC.exe"
)

REM Try PATH
where iscc >nul 2>nul
if %ERRORLEVEL% equ 0 set "ISCC=iscc"

if "!ISCC!"=="" (
    echo WARNING: Inno Setup not found. Install from: https://jrsoftware.org/isinfo.php
    echo Skipping installer creation.
    goto :skip_installer
)

"!ISCC!" windows\installer\heyearth_setup.iss
if %ERRORLEVEL% equ 0 (
    echo.
    echo ============================================
    echo   Installer created successfully!
    echo   Output: releases\HeyearthSetup-*.exe
    echo ============================================
) else (
    echo ERROR: Inno Setup compilation failed
)

goto :end

:skip_installer
echo.
echo Build output (without installer): build\windows\x64\runner\Release\
echo To create an installer, install Inno Setup and place xray.exe in windows\installer\xray\

:end
echo.
echo Done.
endlocal
