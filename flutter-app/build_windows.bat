@echo off
REM Сборка HeYearth VPN для Windows.
REM Требуется: Flutter SDK в PATH (flutter doctor -d).
setlocal
cd /d "%~dp0"
flutter pub get
flutter build windows
if %ERRORLEVEL% equ 0 (
  echo.
  echo Сборка завершена. EXE и данные в: build\windows\x64\runner\Release\
)
endlocal
