#!/bin/bash
# Генерация Go и Dart из proto. Требуется: protoc, protoc-gen-go, protoc-gen-go-grpc (Go),
# для Dart: dart pub global activate protoc_plugin, затем protoc с --dart_out=grpc.
set -e
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BACKEND="$ROOT/backend"
PROTO_DIR="$BACKEND/proto"
GO_OUT="$BACKEND/internal/pb"
APP_PROTO_DST="$ROOT/app/proto"

echo "Backend: $BACKEND"
echo "Proto:   $PROTO_DIR"

# Go
export PATH="$PATH:$(go env GOPATH)/bin"
mkdir -p "$GO_OUT"
protoc -I="$PROTO_DIR" \
  --go_out="$BACKEND" --go_opt=module=github.com/heyearth/backend \
  --go-grpc_out="$BACKEND" --go-grpc_opt=module=github.com/heyearth/backend \
  "$PROTO_DIR"/heyearth/v1/config.proto
echo "Go code generated in $GO_OUT"

# Dart (Flutter app)
if [ -d "$ROOT/app" ]; then
  mkdir -p "$APP_PROTO_DST"
  protoc -I="$PROTO_DIR" \
    --dart_out=grpc:"$APP_PROTO_DST" \
    "$PROTO_DIR"/heyearth/v1/config.proto
  echo "Dart code generated in $APP_PROTO_DST"
fi
