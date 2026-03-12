// This is a generated file - do not edit.
//
// Generated from heyearth/v1/config.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'config.pb.dart' as $0;
import 'config.pbjson.dart';

export 'config.pb.dart';

abstract class ConfigApiServiceBase extends $pb.GeneratedService {
  $async.Future<$0.GetConfigResponse> getConfig(
      $pb.ServerContext ctx, $0.GetConfigRequest request);
  $async.Future<$0.HealthResponse> health(
      $pb.ServerContext ctx, $0.HealthRequest request);
  $async.Future<$0.CheckAppUpdateResponse> checkAppUpdate(
      $pb.ServerContext ctx, $0.CheckAppUpdateRequest request);
  $async.Future<$0.ApplyPromocodeResponse> applyPromocode(
      $pb.ServerContext ctx, $0.ApplyPromocodeRequest request);
  $async.Future<$0.GetProfileResponse> getProfile(
      $pb.ServerContext ctx, $0.GetProfileRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'GetConfig':
        return $0.GetConfigRequest();
      case 'Health':
        return $0.HealthRequest();
      case 'CheckAppUpdate':
        return $0.CheckAppUpdateRequest();
      case 'ApplyPromocode':
        return $0.ApplyPromocodeRequest();
      case 'GetProfile':
        return $0.GetProfileRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'GetConfig':
        return getConfig(ctx, request as $0.GetConfigRequest);
      case 'Health':
        return health(ctx, request as $0.HealthRequest);
      case 'CheckAppUpdate':
        return checkAppUpdate(ctx, request as $0.CheckAppUpdateRequest);
      case 'ApplyPromocode':
        return applyPromocode(ctx, request as $0.ApplyPromocodeRequest);
      case 'GetProfile':
        return getProfile(ctx, request as $0.GetProfileRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json => ConfigApiServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => ConfigApiServiceBase$messageJson;
}

abstract class BotApiServiceBase extends $pb.GeneratedService {
  $async.Future<$0.UserProfileResponse> ensureTelegramUser(
      $pb.ServerContext ctx, $0.EnsureTelegramUserRequest request);
  $async.Future<$0.UserProfileResponse> setPreferredPlatform(
      $pb.ServerContext ctx, $0.SetPreferredPlatformRequest request);
  $async.Future<$0.CreatePairingCodeResponse> createPairingCode(
      $pb.ServerContext ctx, $0.CreatePairingCodeRequest request);
  $async.Future<$0.GetClientDistributionResponse> getClientDistribution(
      $pb.ServerContext ctx, $0.GetClientDistributionRequest request);
  $async.Future<$0.ConnectionBundleResponse> issueLegacyAppleConfig(
      $pb.ServerContext ctx, $0.IssueLegacyAppleConfigRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'EnsureTelegramUser':
        return $0.EnsureTelegramUserRequest();
      case 'SetPreferredPlatform':
        return $0.SetPreferredPlatformRequest();
      case 'CreatePairingCode':
        return $0.CreatePairingCodeRequest();
      case 'GetClientDistribution':
        return $0.GetClientDistributionRequest();
      case 'IssueLegacyAppleConfig':
        return $0.IssueLegacyAppleConfigRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'EnsureTelegramUser':
        return ensureTelegramUser(ctx, request as $0.EnsureTelegramUserRequest);
      case 'SetPreferredPlatform':
        return setPreferredPlatform(
            ctx, request as $0.SetPreferredPlatformRequest);
      case 'CreatePairingCode':
        return createPairingCode(ctx, request as $0.CreatePairingCodeRequest);
      case 'GetClientDistribution':
        return getClientDistribution(
            ctx, request as $0.GetClientDistributionRequest);
      case 'IssueLegacyAppleConfig':
        return issueLegacyAppleConfig(
            ctx, request as $0.IssueLegacyAppleConfigRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json => BotApiServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => BotApiServiceBase$messageJson;
}

abstract class ClientApiServiceBase extends $pb.GeneratedService {
  $async.Future<$0.ClaimResponse> claimByCode(
      $pb.ServerContext ctx, $0.ClaimByCodeRequest request);
  $async.Future<$0.ClaimResponse> claimByToken(
      $pb.ServerContext ctx, $0.ClaimByTokenRequest request);
  $async.Future<$0.UserProfileResponse> getMe(
      $pb.ServerContext ctx, $0.GetMeRequest request);
  $async.Future<$0.AccessStateResponse> getAccessState(
      $pb.ServerContext ctx, $0.GetAccessStateRequest request);
  $async.Future<$0.ConnectionBundleResponse> getConnectionBundle(
      $pb.ServerContext ctx, $0.GetConnectionBundleRequest request);
  $async.Future<$0.ListSubscriptionsResponse> listSubscriptions(
      $pb.ServerContext ctx, $0.ListSubscriptionsRequest request);
  $async.Future<$0.ListPaymentsResponse> listPayments(
      $pb.ServerContext ctx, $0.ListPaymentsRequest request);
  $async.Future<$0.ApplyPromocodeResponse> applyPromocode(
      $pb.ServerContext ctx, $0.ApplyPromocodeRequest request);
  $async.Future<$0.CheckAppUpdateResponse> checkAppUpdate(
      $pb.ServerContext ctx, $0.CheckAppUpdateRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'ClaimByCode':
        return $0.ClaimByCodeRequest();
      case 'ClaimByToken':
        return $0.ClaimByTokenRequest();
      case 'GetMe':
        return $0.GetMeRequest();
      case 'GetAccessState':
        return $0.GetAccessStateRequest();
      case 'GetConnectionBundle':
        return $0.GetConnectionBundleRequest();
      case 'ListSubscriptions':
        return $0.ListSubscriptionsRequest();
      case 'ListPayments':
        return $0.ListPaymentsRequest();
      case 'ApplyPromocode':
        return $0.ApplyPromocodeRequest();
      case 'CheckAppUpdate':
        return $0.CheckAppUpdateRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'ClaimByCode':
        return claimByCode(ctx, request as $0.ClaimByCodeRequest);
      case 'ClaimByToken':
        return claimByToken(ctx, request as $0.ClaimByTokenRequest);
      case 'GetMe':
        return getMe(ctx, request as $0.GetMeRequest);
      case 'GetAccessState':
        return getAccessState(ctx, request as $0.GetAccessStateRequest);
      case 'GetConnectionBundle':
        return getConnectionBundle(
            ctx, request as $0.GetConnectionBundleRequest);
      case 'ListSubscriptions':
        return listSubscriptions(ctx, request as $0.ListSubscriptionsRequest);
      case 'ListPayments':
        return listPayments(ctx, request as $0.ListPaymentsRequest);
      case 'ApplyPromocode':
        return applyPromocode(ctx, request as $0.ApplyPromocodeRequest);
      case 'CheckAppUpdate':
        return checkAppUpdate(ctx, request as $0.CheckAppUpdateRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json => ClientApiServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => ClientApiServiceBase$messageJson;
}
