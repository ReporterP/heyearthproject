package main

import (
	"context"
	"encoding/base64"
	"fmt"
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
func buildHubConnectionOptions(userID string) []*heyearth_v1.ConnectionOption {
	mainServerHost := biz.GetSettingOrDefault("main_server_host", "")
	vlessPort := biz.GetSettingOrDefault("vless_port", "443")
	socks5Port := biz.GetSettingOrDefault("socks5_port", "1080")
	httpProxyPort := biz.GetSettingOrDefault("http_proxy_port", "8080")
	ssPort := biz.GetSettingOrDefault("ss_port", "8388")
	ssMethod := biz.GetSettingOrDefault("ss_method", "2022-blake3-aes-128-gcm")
	ssPassword := biz.GetSettingOrDefault("ss_password", "")
	realityPublicKey := biz.GetSettingOrDefault("reality_public_key", "")
	realityShortID := biz.GetSettingOrDefault("reality_short_id", "")
	realitySNI := biz.GetSettingOrDefault("reality_sni", mainServerHost)

	// Получаем per-user UUID для VLESS и прокси-авторизации
	userVlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		// Фоллбэк на серверный UUID если не удалось получить пользовательский
		userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "")
	}

	var opts []*heyearth_v1.ConnectionOption

	if mainServerHost != "" {
		// 1. VPN mode (VLESS Reality) — полный туннель, per-user UUID
		if userVlessUUID != "" && realityPublicKey != "" {
			vlessURI := fmt.Sprintf("vless://%s@%s:%s?encryption=none&flow=xtls-rprx-vision&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=tcp#HeyEarth%%20VPN",
				userVlessUUID, mainServerHost, vlessPort, realitySNI, realityPublicKey, realityShortID)
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 vlessURI,
				Protocol:            "vless",
				Label:               "VPN (полный туннель)",
				RecommendedNetworks: []string{"cellular", "ethernet", "any"},
				Priority:            1,
			})
		}

		// 2. Shadowsocks — зашифрованный прокси (общий пароль — ограничение протокола)
		if ssPassword != "" {
			// SIP002 формат: ss://base64(method:password)@host:port#label
			userInfo := base64.URLEncoding.EncodeToString([]byte(ssMethod + ":" + ssPassword))
			ssURI := fmt.Sprintf("ss://%s@%s:%s#HeyEarth%%20SS", userInfo, mainServerHost, ssPort)
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 ssURI,
				Protocol:            "shadowsocks",
				Label:               "Shadowsocks (зашифрованный прокси)",
				RecommendedNetworks: []string{"cellular", "wifi", "any"},
				Priority:            2,
			})
		}

		// 3. SOCKS5 proxy (per-user авторизация: user=<userID>, pass=<vless_uuid>)
		if userVlessUUID != "" {
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 fmt.Sprintf("socks5://%s:%s@%s:%s", userID, userVlessUUID, mainServerHost, socks5Port),
				Protocol:            "socks5",
				Label:               "SOCKS5 прокси",
				RecommendedNetworks: []string{"wifi", "ethernet", "any"},
				Priority:            3,
			})
		}

		// 4. HTTP proxy (per-user авторизация: user=<userID>, pass=<vless_uuid>)
		if userVlessUUID != "" {
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 fmt.Sprintf("http://%s:%s@%s:%s", userID, userVlessUUID, mainServerHost, httpProxyPort),
				Protocol:            "http",
				Label:               "HTTP прокси",
				RecommendedNetworks: []string{"wifi", "ethernet", "any"},
				Priority:            4,
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
