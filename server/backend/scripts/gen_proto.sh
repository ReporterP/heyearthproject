#!/bin/bash
set -euo pipefail

BACKEND="$(cd "$(dirname "$0")/.." && pwd)"
PROTO_DIR="$BACKEND/proto"

export PATH="$PATH:$(go env GOPATH)/bin"

mkdir -p "$BACKEND/internal/pb/heyearth_v1" "$BACKEND/internal/pb/node_agent_v1"

rm -rf "$BACKEND/internal/pb/heyearth_v1/heyearth" "$BACKEND/internal/pb/node_agent_v1/node_agent"

protoc -I="$PROTO_DIR" \
  --go_out="$BACKEND/internal/pb/heyearth_v1" --go_opt=paths=source_relative \
  --go-grpc_out="$BACKEND/internal/pb/heyearth_v1" --go-grpc_opt=paths=source_relative \
  "$PROTO_DIR/heyearth/v1/config.proto"

mv "$BACKEND/internal/pb/heyearth_v1/heyearth/v1/config.pb.go" "$BACKEND/internal/pb/heyearth_v1/config.pb.go"
mv "$BACKEND/internal/pb/heyearth_v1/heyearth/v1/config_grpc.pb.go" "$BACKEND/internal/pb/heyearth_v1/config_grpc.pb.go"
rm -rf "$BACKEND/internal/pb/heyearth_v1/heyearth"

protoc -I="$PROTO_DIR" \
  --go_out="$BACKEND/internal/pb/node_agent_v1" --go_opt=paths=source_relative \
  --go-grpc_out="$BACKEND/internal/pb/node_agent_v1" --go-grpc_opt=paths=source_relative \
  "$PROTO_DIR/node_agent/v1/node_agent.proto"

mv "$BACKEND/internal/pb/node_agent_v1/node_agent/v1/node_agent.pb.go" "$BACKEND/internal/pb/node_agent_v1/node_agent.pb.go"
mv "$BACKEND/internal/pb/node_agent_v1/node_agent/v1/node_agent_grpc.pb.go" "$BACKEND/internal/pb/node_agent_v1/node_agent_grpc.pb.go"
rm -rf "$BACKEND/internal/pb/node_agent_v1/node_agent"

echo "Go proto stubs generated successfully."
