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

			// Tree-themed names (🌳 Берёза / 🌲 Дуб / 🎋 Бамбук) with inline
			// short explainers (• основной / - запасной / • Wi-Fi). The earlier
			// approach of base64-encoded `serverDescription` in URL fragments
			// (per happ docs) is not rendered on current happ builds — we bake
			// the explainer into the visible label instead. The user's actual
			// exit is selected by the smart load-balancer at connect time, so
			// we don't expose country geography here.

			// Single unified VLESS Reality entry — «🌳 Берёза».
			// Previously we shipped 3 variants (chrome/safari/edge fingerprints
			// branded as Домашний/Мобильный/Строгий) so happ could auto-pick the
			// fastest. The diversity confused users — same exit nodes, same
			// performance, just different TLS fingerprints. Population-level FP
			// diversity is preserved by auto-tuner's 4h rotation of
			// app_settings.reality_fingerprint across the user base.
			//
			// chrome is kept as the default fingerprint (most reliable in
			// production based on tunnel-monitor logs).
			//
			// tcpKeepAliveInterval=15 / tcpKeepAliveIdle=100: shipped via the URI
			// so happ configures sockopt without manual editing. Keepalive pings
			// prevent client-side policy.connIdle from killing long-lived streams
			// (AI streaming, WebSocket, SSE) AND keep carrier-NAT mappings alive
			// on mobile / hotel wifi networks.
			// Compact inline label — happ's serverDescription base64 hint inside
			// the URL fragment isn't being rendered on every happ build, so we
			// bake the explainer directly into the visible name. Short phrase
			// fits the bold first-line layout without truncation.
			// PathEscape encodes spaces as %20 (RFC 3986 fragment); QueryEscape
			// would emit `+` which happ-desktop renders literally as «+» between
			// words. Verified visually 2026-05-26.
			//
			// URI param order follows the v2ray-spec canonical order:
			// encryption → flow → security → sni → fp → pbk → sid → type.
			// happ-desktop's embedded Xray-core URI parser (verified 2026-05-26
			// via e2e probe — server-side connection works perfectly, but
			// happ-desktop shows n/a) appears sensitive to param ordering and
			// to non-standard tcpKeepAlive* params. Dropped tcpKeepAlive*
			// — they're TCP socket tuning, not protocol-critical.
			// URI exactly mirrors the working multi-FP variant from before the
			// consolidation (Домашний 225ms / Мобильный 222ms / Строгий 215ms).
			// Param order:
			//   encryption -> security -> sni -> fp -> pbk -> sid -> type
			//   -> tcpKeepAliveInterval -> tcpKeepAliveIdle -> flow
			// happ-desktop embedded parser appears strict about this exact
			// ordering for VLESS+Reality+Vision. Reverting parameter order or
			// dropping tcpKeepAlive* breaks ping AND connection — Hysteria
			// (no flow param) is unaffected.
			vlessURI := fmt.Sprintf(
				"vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
				userVlessUUID, mainServerHost, realitySNI, realityPubKey, realityShortID, flowParam,
				url.PathEscape("🌳 Берёза - основной"))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 vlessURI,
				Protocol:            "vless",
				Label:               "🌳 Берёза - основной",
				RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
				Priority:            0,
			})
		}

		// 1b. VLESS Reality + XHTTP (anti-behavioral-DPI) — «🌲 Дуб».
		// Fallback profile for ISPs running aggressive behavioral DPI against
		// pure-TCP Reality. SplitHTTP framing makes the connection look like
		// HTTP/1.1 long-poll rather than direct Reality handshake — slips past
		// some Russian DPI deployments that learned to fingerprint TCP+Reality
		// timing patterns.
		//
		// Port: configurable via app_settings.xhttp_port (default 2443). If
		// provider firewall blocks :2443 (verified 2026-05-26 on vdsina —
		// external TCP times out, internal succeeds), the setting can be
		// changed to a port the provider allows without redeploy.
		if realityPubKey != "" && canVLESS {
			xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")
			// type=xhttp matches the Xray inbound's `network: xhttp` (renamed
			// from splithttp in Xray v1.8.21+). XTLS flow MUST be absent on
			// xhttp transport — server-side clients list excludes it. See
			// xray_sync.go::vlessClientsXHTTP.
			//
			// path=/ + host=<server> are required so the xhttp HTTP request
			// matches what the server's xhttpSettings.host expects (Reality
			// validates Host header against this list, not realitySettings.
			// serverNames which contains the SNI camouflage domain yandex.ru).
			// Mirror Berëza param structure (incl. tcpKeepAlive*). No flow on
			// xhttp transport — XTLS Vision only works with type=tcp. Plus
			// xhttp-specific params: path + host + mode.
			xhttpURI := fmt.Sprintf(
				"vless://%s@%s:%s?encryption=none&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&type=xhttp&path=%%2F&host=%s&mode=auto&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
				userVlessUUID, mainServerHost, xhttpPort, realitySNI, realityPubKey, realityShortID,
				url.QueryEscape(mainServerHost),
				url.PathEscape("🌲 Дуб - запасной"))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 xhttpURI,
				Protocol:            "vless",
				Label:               "🌲 Дуб - запасной",
				RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
				Priority:            1, // below Берёза — fallback choice
			})
		}

	}

	// 2. Hysteria2 «Express» — QUIC/UDP low-latency путь.
	// Сейчас включён для ВСЕХ пользователей (не привязан к плану) — это «технологический preview»
	// чтобы happ показывал Express в списке профилей и мы видели реальное поведение.
	// Авторизация через per-user пароль (hysteria2_password), сервер проверяет через HTTP-бэкенд.
	if mainServerHost != "" {
		hy2Password, hy2Err := biz.GetUserHysteria2Password(userID)
		obfsPassword := biz.GetHysteria2ObfsPassword()
		if hy2Err == nil && hy2Password != "" && obfsPassword != "" {
			// Port-hopping: клиент берёт случайный порт из 50000-50100, iptables на Хабе REDIRECT
			// все эти UDP-порты на :50443 где слушает hysteria2-server. Защищает от QoS,
			// который таргетирует один UDP-порт.
			// Hysteria2 URI: hysteria2://<password>@<host>:<port>/?obfs=salamander&obfs-password=<obfs>&sni=<sni>&insecure=0#<name>
			// Поле `mport` — Happ-расширение для port-hopping ranges.
			//
			// «🎋 Бамбук» — premium speed profile. RecommendedNetworks limited to
			// wifi/ethernet because UDP-based QUIC works best on stable lossless
			// links. The subtitle explicitly warns about the Wi-Fi limitation so
			// users don't pick it on cellular and complain about reconnects.
			hy2URI := fmt.Sprintf(
				"hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=50000-50100#%s",
				url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
				url.PathEscape("🎋 Бамбук - скорость, только Wi-Fi"))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 hy2URI,
				Protocol:            "hysteria2",
				Label:               "🎋 Бамбук - скорость, только Wi-Fi",
				RecommendedNetworks: []string{"wifi", "ethernet"}, // Wi-Fi only hint to happ
				Priority:            2,                            // below Дуб — premium pick, not default
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
