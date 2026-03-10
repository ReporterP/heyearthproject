@echo off
setlocal

set BACKEND=%~dp0..
for %%I in ("%BACKEND%") do set BACKEND=%%~fI
set PROTO_DIR=%BACKEND%\proto

if "%GOPATH%"=="" set GOPATH=%USERPROFILE%\go
set PATH=%PATH%;%GOPATH%\bin

mkdir "%BACKEND%\internal\pb\heyearth_v1" 2>nul
mkdir "%BACKEND%\internal\pb\node_agent_v1" 2>nul
rmdir /S /Q "%BACKEND%\internal\pb\heyearth_v1\heyearth" 2>nul
rmdir /S /Q "%BACKEND%\internal\pb\node_agent_v1\node_agent" 2>nul

protoc -I="%PROTO_DIR%" ^
  --go_out="%BACKEND%\internal\pb\heyearth_v1" --go_opt=paths=source_relative ^
  --go-grpc_out="%BACKEND%\internal\pb\heyearth_v1" --go-grpc_opt=paths=source_relative ^
  "%PROTO_DIR%\heyearth\v1\config.proto"

move /Y "%BACKEND%\internal\pb\heyearth_v1\heyearth\v1\config.pb.go" "%BACKEND%\internal\pb\heyearth_v1\config.pb.go" >nul
move /Y "%BACKEND%\internal\pb\heyearth_v1\heyearth\v1\config_grpc.pb.go" "%BACKEND%\internal\pb\heyearth_v1\config_grpc.pb.go" >nul
rmdir /S /Q "%BACKEND%\internal\pb\heyearth_v1\heyearth"

protoc -I="%PROTO_DIR%" ^
  --go_out="%BACKEND%\internal\pb\node_agent_v1" --go_opt=paths=source_relative ^
  --go-grpc_out="%BACKEND%\internal\pb\node_agent_v1" --go-grpc_opt=paths=source_relative ^
  "%PROTO_DIR%\node_agent\v1\node_agent.proto"

move /Y "%BACKEND%\internal\pb\node_agent_v1\node_agent\v1\node_agent.pb.go" "%BACKEND%\internal\pb\node_agent_v1\node_agent.pb.go" >nul
move /Y "%BACKEND%\internal\pb\node_agent_v1\node_agent\v1\node_agent_grpc.pb.go" "%BACKEND%\internal\pb\node_agent_v1\node_agent_grpc.pb.go" >nul
rmdir /S /Q "%BACKEND%\internal\pb\node_agent_v1\node_agent"

echo Go proto stubs generated successfully.
