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

import 'package:grpc/service_api.dart' as $grpc;
import 'package:protobuf/protobuf.dart' as $pb;

import 'config.pb.dart' as $0;

export 'config.pb.dart';

/// ConfigApi — legacy service kept for backward compatibility.
@$pb.GrpcServiceName('heyearth.v1.ConfigApi')
class ConfigApiClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  ConfigApiClient(super.channel, {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.GetConfigResponse> getConfig(
    $0.GetConfigRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getConfig, request, options: options);
  }

  $grpc.ResponseFuture<$0.HealthResponse> health(
    $0.HealthRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$health, request, options: options);
  }

  $grpc.ResponseFuture<$0.CheckAppUpdateResponse> checkAppUpdate(
    $0.CheckAppUpdateRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$checkAppUpdate, request, options: options);
  }

  $grpc.ResponseFuture<$0.ApplyPromocodeResponse> applyPromocode(
    $0.ApplyPromocodeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$applyPromocode, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetProfileResponse> getProfile(
    $0.GetProfileRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getProfile, request, options: options);
  }

  // method descriptors

  static final _$getConfig =
      $grpc.ClientMethod<$0.GetConfigRequest, $0.GetConfigResponse>(
          '/heyearth.v1.ConfigApi/GetConfig',
          ($0.GetConfigRequest value) => value.writeToBuffer(),
          $0.GetConfigResponse.fromBuffer);
  static final _$health =
      $grpc.ClientMethod<$0.HealthRequest, $0.HealthResponse>(
          '/heyearth.v1.ConfigApi/Health',
          ($0.HealthRequest value) => value.writeToBuffer(),
          $0.HealthResponse.fromBuffer);
  static final _$checkAppUpdate =
      $grpc.ClientMethod<$0.CheckAppUpdateRequest, $0.CheckAppUpdateResponse>(
          '/heyearth.v1.ConfigApi/CheckAppUpdate',
          ($0.CheckAppUpdateRequest value) => value.writeToBuffer(),
          $0.CheckAppUpdateResponse.fromBuffer);
  static final _$applyPromocode =
      $grpc.ClientMethod<$0.ApplyPromocodeRequest, $0.ApplyPromocodeResponse>(
          '/heyearth.v1.ConfigApi/ApplyPromocode',
          ($0.ApplyPromocodeRequest value) => value.writeToBuffer(),
          $0.ApplyPromocodeResponse.fromBuffer);
  static final _$getProfile =
      $grpc.ClientMethod<$0.GetProfileRequest, $0.GetProfileResponse>(
          '/heyearth.v1.ConfigApi/GetProfile',
          ($0.GetProfileRequest value) => value.writeToBuffer(),
          $0.GetProfileResponse.fromBuffer);
}

@$pb.GrpcServiceName('heyearth.v1.ConfigApi')
abstract class ConfigApiServiceBase extends $grpc.Service {
  $core.String get $name => 'heyearth.v1.ConfigApi';

  ConfigApiServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.GetConfigRequest, $0.GetConfigResponse>(
        'GetConfig',
        getConfig_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetConfigRequest.fromBuffer(value),
        ($0.GetConfigResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.HealthRequest, $0.HealthResponse>(
        'Health',
        health_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.HealthRequest.fromBuffer(value),
        ($0.HealthResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.CheckAppUpdateRequest,
            $0.CheckAppUpdateResponse>(
        'CheckAppUpdate',
        checkAppUpdate_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.CheckAppUpdateRequest.fromBuffer(value),
        ($0.CheckAppUpdateResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ApplyPromocodeRequest,
            $0.ApplyPromocodeResponse>(
        'ApplyPromocode',
        applyPromocode_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ApplyPromocodeRequest.fromBuffer(value),
        ($0.ApplyPromocodeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetProfileRequest, $0.GetProfileResponse>(
        'GetProfile',
        getProfile_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetProfileRequest.fromBuffer(value),
        ($0.GetProfileResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.GetConfigResponse> getConfig_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetConfigRequest> $request) async {
    return getConfig($call, await $request);
  }

  $async.Future<$0.GetConfigResponse> getConfig(
      $grpc.ServiceCall call, $0.GetConfigRequest request);

  $async.Future<$0.HealthResponse> health_Pre(
      $grpc.ServiceCall $call, $async.Future<$0.HealthRequest> $request) async {
    return health($call, await $request);
  }

  $async.Future<$0.HealthResponse> health(
      $grpc.ServiceCall call, $0.HealthRequest request);

  $async.Future<$0.CheckAppUpdateResponse> checkAppUpdate_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.CheckAppUpdateRequest> $request) async {
    return checkAppUpdate($call, await $request);
  }

  $async.Future<$0.CheckAppUpdateResponse> checkAppUpdate(
      $grpc.ServiceCall call, $0.CheckAppUpdateRequest request);

  $async.Future<$0.ApplyPromocodeResponse> applyPromocode_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ApplyPromocodeRequest> $request) async {
    return applyPromocode($call, await $request);
  }

  $async.Future<$0.ApplyPromocodeResponse> applyPromocode(
      $grpc.ServiceCall call, $0.ApplyPromocodeRequest request);

  $async.Future<$0.GetProfileResponse> getProfile_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetProfileRequest> $request) async {
    return getProfile($call, await $request);
  }

  $async.Future<$0.GetProfileResponse> getProfile(
      $grpc.ServiceCall call, $0.GetProfileRequest request);
}

@$pb.GrpcServiceName('heyearth.v1.BotApi')
class BotApiClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  BotApiClient(super.channel, {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.UserProfileResponse> ensureTelegramUser(
    $0.EnsureTelegramUserRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$ensureTelegramUser, request, options: options);
  }

  $grpc.ResponseFuture<$0.UserProfileResponse> setPreferredPlatform(
    $0.SetPreferredPlatformRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$setPreferredPlatform, request, options: options);
  }

  $grpc.ResponseFuture<$0.CreatePairingCodeResponse> createPairingCode(
    $0.CreatePairingCodeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$createPairingCode, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetClientDistributionResponse> getClientDistribution(
    $0.GetClientDistributionRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getClientDistribution, request, options: options);
  }

  $grpc.ResponseFuture<$0.ConnectionBundleResponse> issueLegacyAppleConfig(
    $0.IssueLegacyAppleConfigRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$issueLegacyAppleConfig, request,
        options: options);
  }

  // method descriptors

  static final _$ensureTelegramUser =
      $grpc.ClientMethod<$0.EnsureTelegramUserRequest, $0.UserProfileResponse>(
          '/heyearth.v1.BotApi/EnsureTelegramUser',
          ($0.EnsureTelegramUserRequest value) => value.writeToBuffer(),
          $0.UserProfileResponse.fromBuffer);
  static final _$setPreferredPlatform = $grpc.ClientMethod<
          $0.SetPreferredPlatformRequest, $0.UserProfileResponse>(
      '/heyearth.v1.BotApi/SetPreferredPlatform',
      ($0.SetPreferredPlatformRequest value) => value.writeToBuffer(),
      $0.UserProfileResponse.fromBuffer);
  static final _$createPairingCode = $grpc.ClientMethod<
          $0.CreatePairingCodeRequest, $0.CreatePairingCodeResponse>(
      '/heyearth.v1.BotApi/CreatePairingCode',
      ($0.CreatePairingCodeRequest value) => value.writeToBuffer(),
      $0.CreatePairingCodeResponse.fromBuffer);
  static final _$getClientDistribution = $grpc.ClientMethod<
          $0.GetClientDistributionRequest, $0.GetClientDistributionResponse>(
      '/heyearth.v1.BotApi/GetClientDistribution',
      ($0.GetClientDistributionRequest value) => value.writeToBuffer(),
      $0.GetClientDistributionResponse.fromBuffer);
  static final _$issueLegacyAppleConfig = $grpc.ClientMethod<
          $0.IssueLegacyAppleConfigRequest, $0.ConnectionBundleResponse>(
      '/heyearth.v1.BotApi/IssueLegacyAppleConfig',
      ($0.IssueLegacyAppleConfigRequest value) => value.writeToBuffer(),
      $0.ConnectionBundleResponse.fromBuffer);
}

@$pb.GrpcServiceName('heyearth.v1.BotApi')
abstract class BotApiServiceBase extends $grpc.Service {
  $core.String get $name => 'heyearth.v1.BotApi';

  BotApiServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.EnsureTelegramUserRequest,
            $0.UserProfileResponse>(
        'EnsureTelegramUser',
        ensureTelegramUser_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.EnsureTelegramUserRequest.fromBuffer(value),
        ($0.UserProfileResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.SetPreferredPlatformRequest,
            $0.UserProfileResponse>(
        'SetPreferredPlatform',
        setPreferredPlatform_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.SetPreferredPlatformRequest.fromBuffer(value),
        ($0.UserProfileResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.CreatePairingCodeRequest,
            $0.CreatePairingCodeResponse>(
        'CreatePairingCode',
        createPairingCode_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.CreatePairingCodeRequest.fromBuffer(value),
        ($0.CreatePairingCodeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetClientDistributionRequest,
            $0.GetClientDistributionResponse>(
        'GetClientDistribution',
        getClientDistribution_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetClientDistributionRequest.fromBuffer(value),
        ($0.GetClientDistributionResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.IssueLegacyAppleConfigRequest,
            $0.ConnectionBundleResponse>(
        'IssueLegacyAppleConfig',
        issueLegacyAppleConfig_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.IssueLegacyAppleConfigRequest.fromBuffer(value),
        ($0.ConnectionBundleResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.UserProfileResponse> ensureTelegramUser_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.EnsureTelegramUserRequest> $request) async {
    return ensureTelegramUser($call, await $request);
  }

  $async.Future<$0.UserProfileResponse> ensureTelegramUser(
      $grpc.ServiceCall call, $0.EnsureTelegramUserRequest request);

  $async.Future<$0.UserProfileResponse> setPreferredPlatform_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.SetPreferredPlatformRequest> $request) async {
    return setPreferredPlatform($call, await $request);
  }

  $async.Future<$0.UserProfileResponse> setPreferredPlatform(
      $grpc.ServiceCall call, $0.SetPreferredPlatformRequest request);

  $async.Future<$0.CreatePairingCodeResponse> createPairingCode_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.CreatePairingCodeRequest> $request) async {
    return createPairingCode($call, await $request);
  }

  $async.Future<$0.CreatePairingCodeResponse> createPairingCode(
      $grpc.ServiceCall call, $0.CreatePairingCodeRequest request);

  $async.Future<$0.GetClientDistributionResponse> getClientDistribution_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetClientDistributionRequest> $request) async {
    return getClientDistribution($call, await $request);
  }

  $async.Future<$0.GetClientDistributionResponse> getClientDistribution(
      $grpc.ServiceCall call, $0.GetClientDistributionRequest request);

  $async.Future<$0.ConnectionBundleResponse> issueLegacyAppleConfig_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.IssueLegacyAppleConfigRequest> $request) async {
    return issueLegacyAppleConfig($call, await $request);
  }

  $async.Future<$0.ConnectionBundleResponse> issueLegacyAppleConfig(
      $grpc.ServiceCall call, $0.IssueLegacyAppleConfigRequest request);
}

@$pb.GrpcServiceName('heyearth.v1.ClientApi')
class ClientApiClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  ClientApiClient(super.channel, {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.ClaimResponse> claimByCode(
    $0.ClaimByCodeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$claimByCode, request, options: options);
  }

  $grpc.ResponseFuture<$0.ClaimResponse> claimByToken(
    $0.ClaimByTokenRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$claimByToken, request, options: options);
  }

  $grpc.ResponseFuture<$0.UserProfileResponse> getMe(
    $0.GetMeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getMe, request, options: options);
  }

  $grpc.ResponseFuture<$0.AccessStateResponse> getAccessState(
    $0.GetAccessStateRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getAccessState, request, options: options);
  }

  $grpc.ResponseFuture<$0.ConnectionBundleResponse> getConnectionBundle(
    $0.GetConnectionBundleRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getConnectionBundle, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListSubscriptionsResponse> listSubscriptions(
    $0.ListSubscriptionsRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listSubscriptions, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListPaymentsResponse> listPayments(
    $0.ListPaymentsRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listPayments, request, options: options);
  }

  $grpc.ResponseFuture<$0.ApplyPromocodeResponse> applyPromocode(
    $0.ApplyPromocodeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$applyPromocode, request, options: options);
  }

  $grpc.ResponseFuture<$0.CheckAppUpdateResponse> checkAppUpdate(
    $0.CheckAppUpdateRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$checkAppUpdate, request, options: options);
  }

  // method descriptors

  static final _$claimByCode =
      $grpc.ClientMethod<$0.ClaimByCodeRequest, $0.ClaimResponse>(
          '/heyearth.v1.ClientApi/ClaimByCode',
          ($0.ClaimByCodeRequest value) => value.writeToBuffer(),
          $0.ClaimResponse.fromBuffer);
  static final _$claimByToken =
      $grpc.ClientMethod<$0.ClaimByTokenRequest, $0.ClaimResponse>(
          '/heyearth.v1.ClientApi/ClaimByToken',
          ($0.ClaimByTokenRequest value) => value.writeToBuffer(),
          $0.ClaimResponse.fromBuffer);
  static final _$getMe =
      $grpc.ClientMethod<$0.GetMeRequest, $0.UserProfileResponse>(
          '/heyearth.v1.ClientApi/GetMe',
          ($0.GetMeRequest value) => value.writeToBuffer(),
          $0.UserProfileResponse.fromBuffer);
  static final _$getAccessState =
      $grpc.ClientMethod<$0.GetAccessStateRequest, $0.AccessStateResponse>(
          '/heyearth.v1.ClientApi/GetAccessState',
          ($0.GetAccessStateRequest value) => value.writeToBuffer(),
          $0.AccessStateResponse.fromBuffer);
  static final _$getConnectionBundle = $grpc.ClientMethod<
          $0.GetConnectionBundleRequest, $0.ConnectionBundleResponse>(
      '/heyearth.v1.ClientApi/GetConnectionBundle',
      ($0.GetConnectionBundleRequest value) => value.writeToBuffer(),
      $0.ConnectionBundleResponse.fromBuffer);
  static final _$listSubscriptions = $grpc.ClientMethod<
          $0.ListSubscriptionsRequest, $0.ListSubscriptionsResponse>(
      '/heyearth.v1.ClientApi/ListSubscriptions',
      ($0.ListSubscriptionsRequest value) => value.writeToBuffer(),
      $0.ListSubscriptionsResponse.fromBuffer);
  static final _$listPayments =
      $grpc.ClientMethod<$0.ListPaymentsRequest, $0.ListPaymentsResponse>(
          '/heyearth.v1.ClientApi/ListPayments',
          ($0.ListPaymentsRequest value) => value.writeToBuffer(),
          $0.ListPaymentsResponse.fromBuffer);
  static final _$applyPromocode =
      $grpc.ClientMethod<$0.ApplyPromocodeRequest, $0.ApplyPromocodeResponse>(
          '/heyearth.v1.ClientApi/ApplyPromocode',
          ($0.ApplyPromocodeRequest value) => value.writeToBuffer(),
          $0.ApplyPromocodeResponse.fromBuffer);
  static final _$checkAppUpdate =
      $grpc.ClientMethod<$0.CheckAppUpdateRequest, $0.CheckAppUpdateResponse>(
          '/heyearth.v1.ClientApi/CheckAppUpdate',
          ($0.CheckAppUpdateRequest value) => value.writeToBuffer(),
          $0.CheckAppUpdateResponse.fromBuffer);
}

@$pb.GrpcServiceName('heyearth.v1.ClientApi')
abstract class ClientApiServiceBase extends $grpc.Service {
  $core.String get $name => 'heyearth.v1.ClientApi';

  ClientApiServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.ClaimByCodeRequest, $0.ClaimResponse>(
        'ClaimByCode',
        claimByCode_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ClaimByCodeRequest.fromBuffer(value),
        ($0.ClaimResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ClaimByTokenRequest, $0.ClaimResponse>(
        'ClaimByToken',
        claimByToken_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ClaimByTokenRequest.fromBuffer(value),
        ($0.ClaimResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetMeRequest, $0.UserProfileResponse>(
        'GetMe',
        getMe_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetMeRequest.fromBuffer(value),
        ($0.UserProfileResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.GetAccessStateRequest, $0.AccessStateResponse>(
            'GetAccessState',
            getAccessState_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.GetAccessStateRequest.fromBuffer(value),
            ($0.AccessStateResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetConnectionBundleRequest,
            $0.ConnectionBundleResponse>(
        'GetConnectionBundle',
        getConnectionBundle_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetConnectionBundleRequest.fromBuffer(value),
        ($0.ConnectionBundleResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListSubscriptionsRequest,
            $0.ListSubscriptionsResponse>(
        'ListSubscriptions',
        listSubscriptions_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ListSubscriptionsRequest.fromBuffer(value),
        ($0.ListSubscriptionsResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.ListPaymentsRequest, $0.ListPaymentsResponse>(
            'ListPayments',
            listPayments_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.ListPaymentsRequest.fromBuffer(value),
            ($0.ListPaymentsResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ApplyPromocodeRequest,
            $0.ApplyPromocodeResponse>(
        'ApplyPromocode',
        applyPromocode_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ApplyPromocodeRequest.fromBuffer(value),
        ($0.ApplyPromocodeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.CheckAppUpdateRequest,
            $0.CheckAppUpdateResponse>(
        'CheckAppUpdate',
        checkAppUpdate_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.CheckAppUpdateRequest.fromBuffer(value),
        ($0.CheckAppUpdateResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.ClaimResponse> claimByCode_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ClaimByCodeRequest> $request) async {
    return claimByCode($call, await $request);
  }

  $async.Future<$0.ClaimResponse> claimByCode(
      $grpc.ServiceCall call, $0.ClaimByCodeRequest request);

  $async.Future<$0.ClaimResponse> claimByToken_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ClaimByTokenRequest> $request) async {
    return claimByToken($call, await $request);
  }

  $async.Future<$0.ClaimResponse> claimByToken(
      $grpc.ServiceCall call, $0.ClaimByTokenRequest request);

  $async.Future<$0.UserProfileResponse> getMe_Pre(
      $grpc.ServiceCall $call, $async.Future<$0.GetMeRequest> $request) async {
    return getMe($call, await $request);
  }

  $async.Future<$0.UserProfileResponse> getMe(
      $grpc.ServiceCall call, $0.GetMeRequest request);

  $async.Future<$0.AccessStateResponse> getAccessState_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetAccessStateRequest> $request) async {
    return getAccessState($call, await $request);
  }

  $async.Future<$0.AccessStateResponse> getAccessState(
      $grpc.ServiceCall call, $0.GetAccessStateRequest request);

  $async.Future<$0.ConnectionBundleResponse> getConnectionBundle_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetConnectionBundleRequest> $request) async {
    return getConnectionBundle($call, await $request);
  }

  $async.Future<$0.ConnectionBundleResponse> getConnectionBundle(
      $grpc.ServiceCall call, $0.GetConnectionBundleRequest request);

  $async.Future<$0.ListSubscriptionsResponse> listSubscriptions_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ListSubscriptionsRequest> $request) async {
    return listSubscriptions($call, await $request);
  }

  $async.Future<$0.ListSubscriptionsResponse> listSubscriptions(
      $grpc.ServiceCall call, $0.ListSubscriptionsRequest request);

  $async.Future<$0.ListPaymentsResponse> listPayments_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ListPaymentsRequest> $request) async {
    return listPayments($call, await $request);
  }

  $async.Future<$0.ListPaymentsResponse> listPayments(
      $grpc.ServiceCall call, $0.ListPaymentsRequest request);

  $async.Future<$0.ApplyPromocodeResponse> applyPromocode_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ApplyPromocodeRequest> $request) async {
    return applyPromocode($call, await $request);
  }

  $async.Future<$0.ApplyPromocodeResponse> applyPromocode(
      $grpc.ServiceCall call, $0.ApplyPromocodeRequest request);

  $async.Future<$0.CheckAppUpdateResponse> checkAppUpdate_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.CheckAppUpdateRequest> $request) async {
    return checkAppUpdate($call, await $request);
  }

  $async.Future<$0.CheckAppUpdateResponse> checkAppUpdate(
      $grpc.ServiceCall call, $0.CheckAppUpdateRequest request);
}
