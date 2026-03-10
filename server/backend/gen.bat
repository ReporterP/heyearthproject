@echo off
cd /d "%~dp0"
go run github.com/bufbuild/buf/cmd/buf@v1.28.1 generate
echo Exit code: %ERRORLEVEL%
