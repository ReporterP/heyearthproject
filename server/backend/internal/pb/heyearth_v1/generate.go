// Package heyearth_v1 contains generated gRPC code. Generate with:
//
//	go generate ./internal/pb/heyearth_v1
//
// Requires: protoc, protoc-gen-go, protoc-gen-go-grpc (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest)
package heyearth_v1

//go:generate sh -c "protoc -I../../proto --go_out=../.. --go_opt=module=github.com/heyearth/backend --go-grpc_out=../.. --go-grpc_opt=module=github.com/heyearth/backend ../../proto/heyearth/v1/config.proto"
// Windows: run from backend: scripts\gen_proto.bat
