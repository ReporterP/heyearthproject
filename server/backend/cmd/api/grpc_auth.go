package main

// gRPC ClientApi authorization.
//
// nginx publishes /heyearth.v1.ClientApi/ on the site, and every user RPC
// carries a user_id. Before this check the server trusted that user_id, so
// anyone who knew a user's UUID could fetch their connection keys or set a
// password on a password-less account. Now every RPC except the session
// creators and the public probes needs `authorization: Bearer <user JWT>`
// (the token Login/Register return, validated against user_sessions), and
// the request's user_id must be the token's user.
//
// Nobody called ClientApi through nginx in the 14 days before this change
// (Happ uses the REST subscription), so enforcing at once breaks no one.

import (
	"context"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// grpcPublicMethods need no user token: they create a session or carry no user data.
var grpcPublicMethods = map[string]bool{
	heyearth_v1.ClientApi_Register_FullMethodName:       true,
	heyearth_v1.ClientApi_Login_FullMethodName:          true,
	heyearth_v1.ClientApi_Health_FullMethodName:         true,
	heyearth_v1.ClientApi_CheckAppUpdate_FullMethodName: true,
}

// grpcUserMethods need a token whose user is the request's user_id.
var grpcUserMethods = map[string]bool{
	heyearth_v1.ClientApi_UpdateProfile_FullMethodName:       true,
	heyearth_v1.ClientApi_GetMe_FullMethodName:               true,
	heyearth_v1.ClientApi_GetAccessState_FullMethodName:      true,
	heyearth_v1.ClientApi_GetConnectionBundle_FullMethodName: true,
	heyearth_v1.ClientApi_ListSubscriptions_FullMethodName:   true,
	heyearth_v1.ClientApi_ListPayments_FullMethodName:        true,
	heyearth_v1.ClientApi_ApplyPromocode_FullMethodName:      true,
}

// authorizeClientCall decides one RPC. validate returns the token's user_id.
// A method in neither list is refused, so a new RPC is closed until listed.
func authorizeClientCall(method string, md metadata.MD, req any, validate func(token string) (string, error)) error {
	if grpcPublicMethods[method] {
		return nil
	}
	if !grpcUserMethods[method] {
		return status.Error(codes.PermissionDenied, "method is not open to clients")
	}
	token := ""
	for _, v := range md.Get("authorization") {
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			token = strings.TrimSpace(v[7:])
			break
		}
	}
	if token == "" {
		return status.Error(codes.Unauthenticated, "authorization: Bearer <token> is required")
	}
	userID, err := validate(token)
	if err != nil || userID == "" {
		return status.Error(codes.Unauthenticated, "invalid or expired token")
	}
	r, ok := req.(interface{ GetUserId() string })
	if !ok || r.GetUserId() != userID {
		return status.Error(codes.PermissionDenied, "user_id does not match the token")
	}
	return nil
}

// grpcAuthLimiter caps Login/Register. Behind nginx every caller is
// 127.0.0.1, so the key is the method: a global budget, like a per-IP one
// would be for a single address.
var grpcAuthLimiter = middleware.NewRateLimiter(20, time.Minute)

func clientAuthInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	// grpc-go does not recover handler panics: one would kill the whole API.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[grpc] panic в %s: %v\n%s", info.FullMethod, r, debug.Stack())
			resp, err = nil, status.Error(codes.Internal, "internal error")
		}
	}()
	switch info.FullMethod {
	case heyearth_v1.ClientApi_Login_FullMethodName, heyearth_v1.ClientApi_Register_FullMethodName:
		if !grpcAuthLimiter.Allow(info.FullMethod) {
			return nil, status.Error(codes.ResourceExhausted, "too many attempts, try again in a minute")
		}
	}
	md, _ := metadata.FromIncomingContext(ctx)
	err = authorizeClientCall(info.FullMethod, md, req, func(token string) (string, error) {
		claims, err := biz.ValidateUserToken(token)
		if err != nil {
			return "", err
		}
		return claims.UserID, nil
	})
	if err != nil {
		return nil, err
	}
	return handler(ctx, req)
}
