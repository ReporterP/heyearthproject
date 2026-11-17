package main

import (
	"errors"
	"testing"

	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func fakeValidate(token string) (string, error) {
	if token == "good-u1" {
		return "u1", nil
	}
	return "", errors.New("invalid token")
}

func codeOf(err error) codes.Code { return status.Code(err) }

func TestAuthorizeClientCall(t *testing.T) {
	bearer := func(tok string) metadata.MD { return metadata.Pairs("authorization", "Bearer "+tok) }
	cases := []struct {
		name   string
		method string
		md     metadata.MD
		req    any
		want   codes.Code
	}{
		{"login needs no token", heyearth_v1.ClientApi_Login_FullMethodName, nil, &heyearth_v1.LoginRequest{}, codes.OK},
		{"register needs no token", heyearth_v1.ClientApi_Register_FullMethodName, nil, &heyearth_v1.RegisterRequest{}, codes.OK},
		{"health needs no token", heyearth_v1.ClientApi_Health_FullMethodName, nil, &heyearth_v1.HealthRequest{}, codes.OK},
		{"bundle without token", heyearth_v1.ClientApi_GetConnectionBundle_FullMethodName, nil,
			&heyearth_v1.GetConnectionBundleRequest{UserId: "u1"}, codes.Unauthenticated},
		{"bundle with a bad token", heyearth_v1.ClientApi_GetConnectionBundle_FullMethodName, bearer("forged"),
			&heyearth_v1.GetConnectionBundleRequest{UserId: "u1"}, codes.Unauthenticated},
		{"someone else's user_id", heyearth_v1.ClientApi_GetConnectionBundle_FullMethodName, bearer("good-u1"),
			&heyearth_v1.GetConnectionBundleRequest{UserId: "u2"}, codes.PermissionDenied},
		{"empty user_id", heyearth_v1.ClientApi_UpdateProfile_FullMethodName, bearer("good-u1"),
			&heyearth_v1.UpdateProfileRequest{}, codes.PermissionDenied},
		{"own user_id", heyearth_v1.ClientApi_UpdateProfile_FullMethodName, bearer("good-u1"),
			&heyearth_v1.UpdateProfileRequest{UserId: "u1"}, codes.OK},
		{"lowercase bearer", heyearth_v1.ClientApi_GetMe_FullMethodName, metadata.Pairs("authorization", "bearer good-u1"),
			&heyearth_v1.GetMeRequest{UserId: "u1"}, codes.OK},
		{"unknown method is closed", "/heyearth.v1.ClientApi/Future", bearer("good-u1"), &heyearth_v1.GetMeRequest{UserId: "u1"}, codes.PermissionDenied},
	}
	for _, c := range cases {
		if got := codeOf(authorizeClientCall(c.method, c.md, c.req, fakeValidate)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
