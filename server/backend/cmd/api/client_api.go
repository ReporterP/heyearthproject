package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─────────────────────── Auth RPCs ───────────────────────

func (s *server) Register(ctx context.Context, req *heyearth_v1.RegisterRequest) (*heyearth_v1.AuthResponse, error) {
	device := deviceFromProto(req.GetDevice(), "register")

	user, err := biz.RegisterAnonymousUser(device)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "register user: %v", err)
	}

	token, err := issueUserJWT(user.UserID, user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue token: %v", err)
	}

	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get profile: %v", err)
	}

	return &heyearth_v1.AuthResponse{
		UserId:  user.UserID,
		Token:   token,
		Profile: profileToProto(profile),
	}, nil
}

func (s *server) Login(ctx context.Context, req *heyearth_v1.LoginRequest) (*heyearth_v1.AuthResponse, error) {
	idOrUsername := strings.TrimSpace(req.GetIdOrUsername())
	if idOrUsername == "" {
		return nil, status.Error(codes.InvalidArgument, "id_or_username is required")
	}

	user, err := biz.LoginByIDOrUsername(idOrUsername, req.GetPassword())
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "%v", err)
	}

	if req.GetDevice() != nil {
		_ = biz.RegisterUserDevice(user.UserID, deviceFromProto(req.GetDevice(), "login"))
	}

	token, err := issueUserJWT(user.UserID, user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue token: %v", err)
	}

	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get profile: %v", err)
	}

	return &heyearth_v1.AuthResponse{
		UserId:  user.UserID,
		Token:   token,
		Profile: profileToProto(profile),
	}, nil
}

func (s *server) UpdateProfile(ctx context.Context, req *heyearth_v1.UpdateProfileRequest) (*heyearth_v1.UserProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Set username if provided
	if username := strings.TrimSpace(req.GetUsername()); username != "" {
		if err := biz.SetUsername(userID, username); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	// Set password if provided
	if newPassword := req.GetNewPassword(); newPassword != "" {
		if err := biz.SetCloudPassword(userID, newPassword, req.GetCurrentPassword()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	profile, err := biz.GetUserProfile(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get profile: %v", err)
	}
	return profileToProto(profile), nil
}

// ─────────────────────── User Info RPCs ───────────────────────

func (s *server) GetMe(ctx context.Context, req *heyearth_v1.GetMeRequest) (*heyearth_v1.UserProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.GetDevice() != nil {
		_ = biz.RegisterUserDevice(userID, deviceFromProto(req.GetDevice(), "app_open"))
	}
	profile, err := biz.GetUserProfile(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get user profile: %v", err)
	}
	return profileToProto(profile), nil
}

func (s *server) GetAccessState(ctx context.Context, req *heyearth_v1.GetAccessStateRequest) (*heyearth_v1.AccessStateResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	access, err := biz.GetUserAccessState(userID, req.GetPlatform())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get access state: %v", err)
	}
	return accessToProto(access), nil
}

// ─────────────────────── Connection Bundle ───────────────────────

func (s *server) GetConnectionBundle(ctx context.Context, req *heyearth_v1.GetConnectionBundleRequest) (*heyearth_v1.ConnectionBundleResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	access, err := biz.GetUserAccessState(userID, req.GetPlatform())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get access state: %v", err)
	}
	if access.IsBlocked {
		return nil, status.Error(codes.PermissionDenied, "user is blocked")
	}
	if !access.CanConnect {
		return nil, status.Errorf(codes.PermissionDenied, "cannot connect: %s", access.Reason)
	}

	// Hub-and-spoke: return main server connection info (per-user UUID)
	options := buildHubConnectionOptions(userID)

	return &heyearth_v1.ConnectionBundleResponse{
		UserId:                userID,
		ConnectionOptions:     options,
		StatusMessage:         "Connection bundle is ready.",
		TrafficMaskingEnabled: access.TrafficMaskingAvailable,
	}, nil
}

// buildHubConnectionOptions returns connection options pointing to the main server.
// In hub-and-spoke model, all users connect to the main server only.
// Каждый пользователь получает уникальные подключения (per-user VLESS UUID).
// Only VLESS is exposed to users now. SOCKS/HTTP user-facing inbounds removed (see spec 2026-05-22-socks5-removal-mtproto-perk-design.md).
func buildHubConnectionOptions(userID string) []*heyearth_v1.ConnectionOption {
	mainServerHost := biz.GetSettingOrDefault("main_server_host", "")

	// Per-user UUID используется как пароль для прокси-авторизации
	userVlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "")
	}

	// Check user's plan for protocol restrictions
	canVLESS := biz.IsProtocolAllowed(userID, "vless")

	var opts []*heyearth_v1.ConnectionOption

	if mainServerHost != "" && userVlessUUID != "" {
		// 1. VLESS Reality (максимальная защита от DPI) — only if plan allows vless
		realityPubKey := biz.GetSettingOrDefault("reality_public_key", "")
		realityShortID := biz.GetSettingOrDefault("reality_short_id", "")
		realitySNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
		if realityPubKey != "" && canVLESS {
			flow := biz.GetSettingOrDefault("reality_flow", "xtls-rprx-vision")
			flowParam := ""
			if flow != "" {
				flowParam = "&flow=" + flow
			}

			// Profile tag enrichment — per Happ docs (emoji.md / examples-of-links):
			//   • #Title?serverDescription=<base64> shows custom subtitle
			//     (replaces technical "VLESS" line under the profile name)
			//
			// We use a neutral ⚡ emoji prefix instead of a country flag because
			// the user's actual exit is selected by the smart load-balancer at
			// connection time — could be any of N countries. A specific country
			// flag would mislead. The ⚡ also conveys "speed" which matches
			// the Reality TCP performance profile.
			const profileEmoji = "⚡"
			subDescB64 := func(s string) string {
				return base64.StdEncoding.EncodeToString([]byte(s))
			}

			// Multi-profile: 3 VLESS URIs optimized for different ISP types.
			// Client (Happ/v2rayNG) tests all and auto-picks the fastest working one.
			profiles := []struct {
				fp       string
				label    string
				tag      string // displayed under the flag
				subDesc  string // custom 2nd line (base64 inside URI)
				networks []string
				priority int32
			}{
				{"chrome", "Домашний интернет", "Домашний", "Reality • Chrome • TCP", []string{"wifi", "ethernet", "any"}, 0},
				{"safari", "Мобильный интернет", "Мобильный", "Reality • Safari • TCP", []string{"cellular", "wifi"}, 1},
				{"edge", "Строгий режим", "Строгий", "Reality • Edge • TCP", []string{"cellular", "wifi", "ethernet", "any"}, 2},
			}

			for _, p := range profiles {
				fragment := fmt.Sprintf("%s %s?serverDescription=%s", profileEmoji, p.tag, subDescB64(p.subDesc))
				// tcpKeepAliveInterval=15 / tcpKeepAliveIdle=100: shipped via the URI so Happ /
				// v2rayNG configure their outbound sockopt without manual editing. Keepalive
				// pings prevent the client's policy.connIdle (which we cannot push via the
				// URI — it's a client-side policy field) from killing long-lived streams
				// (AI streaming, WebSocket, SSE), AND keep carrier-NAT mappings alive on
				// mobile / hotel wifi networks.
				vlessURI := fmt.Sprintf(
					"vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
					userVlessUUID, mainServerHost, realitySNI, p.fp, realityPubKey, realityShortID, flowParam,
					url.QueryEscape(fragment))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 vlessURI,
					Protocol:            "vless",
					Label:               p.label,
					RecommendedNetworks: p.networks,
					Priority:            p.priority,
				})
			}
		}

		// 1b. VLESS Reality + XHTTP (anti-behavioral-DPI, port 2443)
		if realityPubKey != "" && canVLESS {
			// Neutral ⚡ prefix — exit country chosen by smart load-balancer at connect.
			subDescB64 := base64.StdEncoding.EncodeToString([]byte("Reality • Chrome • XHTTP (anti-DPI)"))
			fragment := fmt.Sprintf("⚡ XHTTP?serverDescription=%s", subDescB64)
			// NOTE: splithttp transport does NOT use flow=xtls-rprx-vision (flow is only for tcp+Reality)
			xhttpURI := fmt.Sprintf(
				"vless://%s@%s:2443?encryption=none&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=splithttp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
				userVlessUUID, mainServerHost, realitySNI, realityPubKey, realityShortID,
				url.QueryEscape(fragment))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 xhttpURI,
				Protocol:            "vless",
				Label:               "XHTTP Reality (максимальная защита)",
				RecommendedNetworks: []string{"cellular", "wifi", "ethernet", "any"},
				Priority:            -1, // highest priority — most DPI-resistant
			})
		}

	}

	return opts
}

// ─────────────────────── Subscriptions & Payments ───────────────────────

func (s *server) ListSubscriptions(ctx context.Context, req *heyearth_v1.ListSubscriptionsRequest) (*heyearth_v1.ListSubscriptionsResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	items, err := biz.ListUserSubscriptions(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list subscriptions: %v", err)
	}
	response := &heyearth_v1.ListSubscriptionsResponse{Items: make([]*heyearth_v1.SubscriptionItem, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, &heyearth_v1.SubscriptionItem{
			GroupId:    int32(item.GroupID),
			GroupName:  item.GroupName,
			ValidUntil: item.ValidUntil,
			AutoRenew:  item.AutoRenew,
			Active:     item.Active,
			Plan:       item.Plan,
			PriceRub:   int32(item.PriceRub),
		})
	}
	return response, nil
}

func (s *server) ListPayments(ctx context.Context, req *heyearth_v1.ListPaymentsRequest) (*heyearth_v1.ListPaymentsResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	items, err := biz.ListUserPayments(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list payments: %v", err)
	}
	response := &heyearth_v1.ListPaymentsResponse{Items: make([]*heyearth_v1.PaymentItem, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, &heyearth_v1.PaymentItem{
			Id:          int32(item.ID),
			Amount:      item.Amount,
			Currency:    item.Currency,
			Status:      item.Status,
			Provider:    item.Provider,
			ExternalId:  item.ExternalID,
			Notes:       item.Notes,
			CreatedAt:   item.CreatedAt,
			ConfirmedAt: item.ConfirmedAt,
		})
	}
	return response, nil
}

// ─────────────────────── Converters ───────────────────────

func profileToProto(profile *biz.UserProfile) *heyearth_v1.UserProfileResponse {
	if profile == nil {
		return &heyearth_v1.UserProfileResponse{}
	}
	return &heyearth_v1.UserProfileResponse{
		UserId:            profile.UserID,
		Username:          profile.Username,
		HasPassword:       profile.HasPassword,
		PreferredPlatform: profile.PreferredPlatform,
		Locale:            profile.Locale,
		IsBlocked:         profile.IsBlocked,
		CurrentPlan:       profile.CurrentPlan,
		ValidUntil:        profile.ValidUntil,
		AccessStatus:      profile.AccessStatus,
		StatusReason:      profile.StatusReason,
	}
}

func accessToProto(access *biz.UserAccessState) *heyearth_v1.AccessStateResponse {
	if access == nil {
		return &heyearth_v1.AccessStateResponse{}
	}
	return &heyearth_v1.AccessStateResponse{
		UserId:                  access.UserID,
		CurrentPlan:             access.CurrentPlan,
		ValidUntil:              access.ValidUntil,
		PreferredPlatform:       access.PreferredPlatform,
		IsBlocked:               access.IsBlocked,
		CanConnect:              access.CanConnect,
		Reason:                  access.Reason,
		IsPremium:               access.IsPremium,
		TrafficMaskingAvailable: access.TrafficMaskingAvailable,
	}
}

func deviceFromProto(device *heyearth_v1.DeviceContext, linkedVia string) biz.DeviceRegistration {
	if device == nil {
		return biz.DeviceRegistration{LinkedVia: linkedVia}
	}
	return biz.DeviceRegistration{
		DeviceID:    device.GetDeviceId(),
		Platform:    device.GetPlatform(),
		DeviceLabel: device.GetDeviceLabel(),
		Locale:      device.GetLocale(),
		LinkedVia:   firstNonEmpty(device.GetLinkedVia(), linkedVia),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ─────────────────────── JWT ───────────────────────

func issueUserJWT(userID string, userDBID int) (string, error) {
	token, err := biz.GenerateUserSessionToken(userID, userDBID)
	if err != nil {
		return "", err
	}
	return token, nil
}
