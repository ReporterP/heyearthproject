---
name: proto
description: Regenerate protobuf Go and Dart code from .proto files. Use when user says "protobuf", "proto", "перегенерировать proto", etc.
disable-model-invocation: true
---

# Regenerate Protobuf

Proto files are in `server/backend/proto/`:
- `heyearth/v1/config.proto` — main API config (ClientApi service)
- `node_agent/v1/node_agent.proto` — node-agent gRPC service

## Tools

- **protoc**: `C:/Users/alex/Documents/heyearthproject/.tools/protoc/bin/protoc.exe` (v29.3)
- **protoc-gen-go** + **protoc-gen-go-grpc**: must be in PATH (`go install`)
- **protoc-gen-dart**: must be in PATH (`dart pub global activate protoc_plugin`)

## Steps

### 1. Generate Go code

```bash
PROTOC="C:/Users/alex/Documents/heyearthproject/.tools/protoc/bin/protoc.exe"
PROTO_DIR="C:/Users/alex/Documents/heyearthproject/server/backend/proto"
GO_OUT="C:/Users/alex/Documents/heyearthproject/server/backend/internal/pb"

# For each proto file:
"$PROTOC" \
  --proto_path="$PROTO_DIR" \
  --go_out="$GO_OUT" --go_opt=paths=source_relative \
  --go-grpc_out="$GO_OUT" --go-grpc_opt=paths=source_relative \
  heyearth/v1/config.proto
```

**Important**: protoc generates to `$GO_OUT/heyearth/v1/` but Go imports expect `heyearth_v1/`. Move files:

```bash
mkdir -p "$GO_OUT/heyearth_v1"
cp "$GO_OUT/heyearth/v1/"*.go "$GO_OUT/heyearth_v1/"
rm -rf "$GO_OUT/heyearth"
```

### 2. Generate Dart code

```bash
DART_OUT="C:/Users/alex/Documents/heyearthproject/flutter-app/lib/generated"
mkdir -p "$DART_OUT"

"$PROTOC" \
  --proto_path="$PROTO_DIR" \
  --dart_out=grpc:"$DART_OUT" \
  heyearth/v1/config.proto
```

### 3. Verify

```bash
cd server/backend && go build ./...
cd flutter-app && fvm flutter pub get
```

### 4. Run `go mod tidy`

```bash
cd server/backend && go mod tidy
```
