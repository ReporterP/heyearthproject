@echo off
REM Генерация Go из proto. Требуется: protoc, go install protoc-gen-go, protoc-gen-go-grpc
set ROOT=%~dp0..\..
set BACKEND=%ROOT%\backend
set PROTO_DIR=%BACKEND%\proto
set GOPATH=%GOPATH%
if "%GOPATH%"=="" set GOPATH=%USERPROFILE%\go
set PATH=%PATH%;%GOPATH%\bin

mkdir "%BACKEND%\internal\pb" 2>nul
protoc -I="%PROTO_DIR%" ^
  --go_out="%BACKEND%" --go_opt=module=github.com/heyearth/backend ^
  --go-grpc_out="%BACKEND%" --go-grpc_opt=module=github.com/heyearth/backend ^
  "%PROTO_DIR%\heyearth\v1\config.proto"
echo Go code generated in internal\pb
echo For Dart: run from app dir: protoc -I=../backend/proto --dart_out=grpc:lib/generated ../backend/proto/heyearth/v1/config.proto
