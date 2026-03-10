package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) EnsureTelegramUser(ctx context.Context, req *heyearth_v1.EnsureTelegramUserRequest) (*heyearth_v1.UserProfileResponse, error) {
	if req.GetTelegramId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "telegram_id is required")
	}
	user, err := biz.EnsureTelegramUser(req.GetTelegramId(), req.GetPreferredPlatform(), req.GetLocale())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ensure telegram user: %v", err)
	}
	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load user profile: %v", err)
	}
	return profileToProto(profile), nil
}

func (s *server) SetPreferredPlatform(ctx context.Context, req *heyearth_v1.SetPreferredPlatformRequest) (*heyearth_v1.UserProfileResponse, error) {
	if req.GetTelegramId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "telegram_id is required")
	}
	user, err := biz.SetPreferredPlatformByTelegram(req.GetTelegramId(), req.GetPreferredPlatform())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "set preferred platform: %v", err)
	}
	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load user profile: %v", err)
	}
	return profileToProto(profile), nil
}

func (s *server) CreatePairingCode(ctx context.Context, req *heyearth_v1.CreatePairingCodeRequest) (*heyearth_v1.CreatePairingCodeResponse, error) {
	if req.GetTelegramId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "telegram_id is required")
	}
	user, pairing, err := biz.CreatePairingCode(req.GetTelegramId(), req.GetPreferredPlatform(), req.GetLocale(), 10*time.Minute)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create pairing code: %v", err)
	}
	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load user profile: %v", err)
	}
	return &heyearth_v1.CreatePairingCodeResponse{
		Profile:    profileToProto(profile),
		Code:       pairing.Code,
		ClaimToken: pairing.ClaimToken,
		ExpiresAt:  pairing.ExpiresAt.Format(time.RFC3339),
		DeeplinkUrl: fmt.Sprintf(
			"heyearth://claim?token=%s",
			pairing.ClaimToken,
		),
	}, nil
}

func (s *server) GetClientDistribution(ctx context.Context, req *heyearth_v1.GetClientDistributionRequest) (*heyearth_v1.GetClientDistributionResponse, error) {
	resp, err := s.CheckAppUpdate(ctx, &heyearth_v1.CheckAppUpdateRequest{Platform: req.GetPlatform()})
	if err != nil {
		return nil, err
	}
	return &heyearth_v1.GetClientDistributionResponse{
		Version:             resp.GetVersion(),
		AndroidUrl:          resp.GetAndroidUrl(),
		WindowsUrl:          resp.GetWindowsUrl(),
		MacosUrl:            resp.GetMacosUrl(),
		LinuxUrl:            resp.GetLinuxUrl(),
		IosUrl:              resp.GetIosUrl(),
		ReleaseNotes:        resp.GetReleaseNotes(),
		Mandatory:           resp.GetMandatory(),
		MinSupportedVersion: resp.GetMinSupportedVersion(),
	}, nil
}

func (s *server) IssueLegacyAppleConfig(ctx context.Context, req *heyearth_v1.IssueLegacyAppleConfigRequest) (*heyearth_v1.ConnectionBundleResponse, error) {
	if req.GetTelegramId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "telegram_id is required")
	}
	user, err := biz.EnsureTelegramUser(req.GetTelegramId(), "ios", "ru")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ensure telegram user: %v", err)
	}
	resp, err := s.GetConfig(ctx, &heyearth_v1.GetConfigRequest{UserId: user.UserID})
	if err != nil {
		return nil, err
	}
	return &heyearth_v1.ConnectionBundleResponse{
		UserId:              user.UserID,
		SsUri:               resp.GetSsUri(),
		ConnectMode:         "legacy_happ",
		StatusMessage:       "Use Happ to import this ss:// link",
		LegacyHappFallback:  true,
		FallbackInstruction: "Import the ss:// link into Happ and enable global mode with DNS through proxy.",
	}, nil
}

func (s *server) ClaimByCode(ctx context.Context, req *heyearth_v1.ClaimByCodeRequest) (*heyearth_v1.ClaimResponse, error) {
	user, _, err := biz.ClaimPairingCodeByCode(req.GetCode(), deviceFromProto(req.GetDevice(), "pairing_code"))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "claim by code: %v", err)
	}
	return buildClaimResponse(user.UserID)
}

func (s *server) ClaimByToken(ctx context.Context, req *heyearth_v1.ClaimByTokenRequest) (*heyearth_v1.ClaimResponse, error) {
	user, _, err := biz.ClaimPairingCodeByToken(req.GetClaimToken(), deviceFromProto(req.GetDevice(), "pairing_token"))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "claim by token: %v", err)
	}
	return buildClaimResponse(user.UserID)
}

func (s *server) GetMe(ctx context.Context, req *heyearth_v1.GetMeRequest) (*heyearth_v1.UserProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.GetDevice() != nil {
		if err := biz.RegisterUserDevice(userID, deviceFromProto(req.GetDevice(), "app_open")); err != nil {
			return nil, status.Errorf(codes.Internal, "register device: %v", err)
		}
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
	if access.LegacyHappFallback {
		return &heyearth_v1.ConnectionBundleResponse{
			UserId:              userID,
			ConnectMode:         "legacy_happ",
			StatusMessage:       "Use the Telegram bot to get an ss:// link for Happ.",
			LegacyHappFallback:  true,
			FallbackInstruction: "Open the bot, choose your Apple platform and import the generated ss:// link into Happ.",
		}, nil
	}

	resp, err := s.GetConfig(ctx, &heyearth_v1.GetConfigRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	return &heyearth_v1.ConnectionBundleResponse{
		UserId:             resp.GetUserId(),
		SsUri:              resp.GetSsUri(),
		ConnectMode:        "native_app",
		StatusMessage:      "Connection bundle is ready.",
		LegacyHappFallback: false,
	}, nil
}

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

func buildClaimResponse(userID string) (*heyearth_v1.ClaimResponse, error) {
	profile, err := biz.GetUserProfile(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get user profile: %v", err)
	}
	access, err := biz.GetUserAccessState(userID, profile.PreferredPlatform)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get access state: %v", err)
	}
	return &heyearth_v1.ClaimResponse{
		Profile:     profileToProto(profile),
		AccessState: accessToProto(access),
	}, nil
}

func profileToProto(profile *biz.UserProfile) *heyearth_v1.UserProfileResponse {
	if profile == nil {
		return &heyearth_v1.UserProfileResponse{}
	}
	return &heyearth_v1.UserProfileResponse{
		UserId:             profile.UserID,
		TelegramId:         profile.TelegramID,
		TelegramLinked:     profile.TelegramLinked,
		PreferredPlatform:  profile.PreferredPlatform,
		Locale:             profile.Locale,
		IsBlocked:          profile.IsBlocked,
		CurrentPlan:        profile.CurrentPlan,
		ValidUntil:         profile.ValidUntil,
		LegacyHappFallback: profile.LegacyHappFallback,
		AccessStatus:       profile.AccessStatus,
		StatusReason:       profile.StatusReason,
	}
}

func accessToProto(access *biz.UserAccessState) *heyearth_v1.AccessStateResponse {
	if access == nil {
		return &heyearth_v1.AccessStateResponse{}
	}
	return &heyearth_v1.AccessStateResponse{
		UserId:             access.UserID,
		CurrentPlan:        access.CurrentPlan,
		ValidUntil:         access.ValidUntil,
		PreferredPlatform:  access.PreferredPlatform,
		IsBlocked:          access.IsBlocked,
		CanConnect:         access.CanConnect,
		LegacyHappFallback: access.LegacyHappFallback,
		Reason:             access.Reason,
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

func (s *server) issueNodeConfig(ctx context.Context, userID string) (*heyearth_v1.GetConfigResponse, error) {
	user, err := biz.GetOrCreateUser(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get user: %v", err)
	}
	if user.IsBlocked {
		return nil, status.Error(codes.PermissionDenied, "user is blocked")
	}

	var groupID int
	sub, err := biz.GetUserSubscription(user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get subscription: %v", err)
	}
	if sub != nil {
		groupID = sub.GroupID
	}

	bestServer, err := biz.GetBestServerForGroup(groupID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "no available VPN nodes: %v", err)
	}

	agentClient, conn, err := connectToNodeAgent(ctx, bestServer.IPAddress)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "connect node-agent: %v", err)
	}
	defer conn.Close()

	resp, err := agentClient.CreateUser(ctx, &pbnode.CreateUserRequest{UserId: userID})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "create config on node-agent: %v", err)
	}

	return &heyearth_v1.GetConfigResponse{
		UserId: userID,
		SsUri:  resp.GetSsLink(),
	}, nil
}
