package main

import (
	"context"
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

			// FP matrix — happ-desktop sees different uTLS ClientHello JA3s for
			// each entry. Russian ISP DPI applies different rules per operator;
			// chrome/safari/ios/firefox give us 4 distinct JA3 signatures so at
			// least one passes through any given operator's DPI whitelist.
			// uTLS FP is purely client-side — server (Xray) doesn't validate it,
			// so all 4 entries share the same vless-reality inbound on :443.
			//
			// PathEscape (not QueryEscape): RFC 3986 fragment encoding emits %20
			// for spaces; QueryEscape's `+` is rendered literally by happ-desktop.
			//
			// 🌳 Берёза 1..4 — TCP Reality :443, 4 uTLS variants.
			realityFPs := []string{"chrome", "safari", "ios", "firefox"}
			for i, fp := range realityFPs {
				label := fmt.Sprintf("🌳 Берёза %d", i+1)
				vlessURI := fmt.Sprintf(
					"vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
					userVlessUUID, mainServerHost, realitySNI, fp, realityPubKey, realityShortID, flowParam,
					url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 vlessURI,
					Protocol:            "vless",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
					Priority:            int32(i), // 0..3
				})
			}

			// 🌲 Дуб 1..2 — XHTTP Reality :2443 (configurable port via app_settings.xhttp_port),
			// 2 uTLS variants. XHTTP framing looks like an HTTP/1.1 long-poll
			// instead of pure-Reality timing pattern, slipping past DPI that
			// learned to fingerprint TCP+Vision. Server inbound `vless-xhttp`
			// strips the `flow` field from clients (see xray_sync.go::vlessClientsXHTTP)
			// because XTLS Vision is incompatible with xhttp transport.
			xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")
			xhttpFPs := []string{"chrome", "safari"}
			for i, fp := range xhttpFPs {
				label := fmt.Sprintf("🌲 Дуб %d", i+1)
				xhttpURI := fmt.Sprintf(
					"vless://%s@%s:%s?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=xhttp&path=%%2F&host=%s&mode=auto&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
					userVlessUUID, mainServerHost, xhttpPort, realitySNI, fp, realityPubKey, realityShortID,
					url.QueryEscape(mainServerHost),
					url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 xhttpURI,
					Protocol:            "vless",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
					Priority:            int32(4 + i), // 4..5
				})
			}
		}
	}

	// 2. Hysteria2 «Бамбук» — QUIC/UDP, Wi-Fi only (mobile carriers QoS UDP).
	// Two entries differ only in congestion control:
	//   Бамбук 1 — no bandwidth in URI → Hysteria2 client picks BBR (adaptive)
	//   Бамбук 2 — explicit upmbps/downmbps → Hysteria2 client picks Brutal CC
	//              (fixed bandwidth, ignores loss-induced backoff — beats operator
	//              QoS that throttles UDP via packet loss injection)
	// Both labels contain "only WiFi" — happ natively hides them on cellular.
	if mainServerHost != "" {
		hy2Password, hy2Err := biz.GetUserHysteria2Password(userID)
		obfsPassword := biz.GetHysteria2ObfsPassword()
		if hy2Err == nil && hy2Password != "" && obfsPassword != "" {
			// Бамбук 1 (BBR adaptive)
			bambooBBRLabel := "🎋 Бамбук 1 only WiFi"
			bambooBBRURI := fmt.Sprintf(
				"hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=50000-50100#%s",
				url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
				url.PathEscape(bambooBBRLabel))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 bambooBBRURI,
				Protocol:            "hysteria2",
				Label:               bambooBBRLabel,
				RecommendedNetworks: []string{"wifi", "ethernet"},
				Priority:            6,
			})

			// Бамбук 2 (Brutal CC, 20↑/80↓ Mbps fixed)
			bambooBrutalLabel := "🎋 Бамбук 2 only WiFi"
			bambooBrutalURI := fmt.Sprintf(
				"hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=50000-50100&upmbps=20&downmbps=80#%s",
				url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
				url.PathEscape(bambooBrutalLabel))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 bambooBrutalURI,
				Protocol:            "hysteria2",
				Label:               bambooBrutalLabel,
				RecommendedNetworks: []string{"wifi", "ethernet"},
				Priority:            7,
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
		// GroupId/GroupName were replaced by plan_id/plan_name in the data model,
		// but the proto field tags are still named group_* for wire compatibility.
		response.Items = append(response.Items, &heyearth_v1.SubscriptionItem{
			GroupId:    int32(item.PlanID),
			GroupName:  item.PlanName,
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
