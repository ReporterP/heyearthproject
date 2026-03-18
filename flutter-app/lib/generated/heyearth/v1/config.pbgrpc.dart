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

/// ClientApi — main service for client applications (mobile, desktop, web).
@$pb.GrpcServiceName('heyearth.v1.ClientApi')
class ClientApiClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  ClientApiClient(super.channel, {super.options, super.interceptors});

  /// Auth
  $grpc.ResponseFuture<$0.AuthResponse> register(
    $0.RegisterRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$register, request, options: options);
  }

  $grpc.ResponseFuture<$0.AuthResponse> login(
    $0.LoginRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$login, request, options: options);
  }

  $grpc.ResponseFuture<$0.UserProfileResponse> updateProfile(
    $0.UpdateProfileRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$updateProfile, request, options: options);
  }

  /// User info
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

  /// Connections
  $grpc.ResponseFuture<$0.ConnectionBundleResponse> getConnectionBundle(
    $0.GetConnectionBundleRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getConnectionBundle, request, options: options);
  }

  /// Subscriptions & Payments
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

  /// App updates
  $grpc.ResponseFuture<$0.CheckAppUpdateResponse> checkAppUpdate(
    $0.CheckAppUpdateRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$checkAppUpdate, request, options: options);
  }

  /// Health
  $grpc.ResponseFuture<$0.HealthResponse> health(
    $0.HealthRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$health, request, options: options);
  }

  // method descriptors

  static final _$register =
      $grpc.ClientMethod<$0.RegisterRequest, $0.AuthResponse>(
          '/heyearth.v1.ClientApi/Register',
          ($0.RegisterRequest value) => value.writeToBuffer(),
          $0.AuthResponse.fromBuffer);
  static final _$login = $grpc.ClientMethod<$0.LoginRequest, $0.AuthResponse>(
      '/heyearth.v1.ClientApi/Login',
      ($0.LoginRequest value) => value.writeToBuffer(),
      $0.AuthResponse.fromBuffer);
  static final _$updateProfile =
      $grpc.ClientMethod<$0.UpdateProfileRequest, $0.UserProfileResponse>(
          '/heyearth.v1.ClientApi/UpdateProfile',
          ($0.UpdateProfileRequest value) => value.writeToBuffer(),
          $0.UserProfileResponse.fromBuffer);
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
  static final _$health =
      $grpc.ClientMethod<$0.HealthRequest, $0.HealthResponse>(
          '/heyearth.v1.ClientApi/Health',
          ($0.HealthRequest value) => value.writeToBuffer(),
          $0.HealthResponse.fromBuffer);
}

@$pb.GrpcServiceName('heyearth.v1.ClientApi')
abstract class ClientApiServiceBase extends $grpc.Service {
  $core.String get $name => 'heyearth.v1.ClientApi';

  ClientApiServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.RegisterRequest, $0.AuthResponse>(
        'Register',
        register_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.RegisterRequest.fromBuffer(value),
        ($0.AuthResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.LoginRequest, $0.AuthResponse>(
        'Login',
        login_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.LoginRequest.fromBuffer(value),
        ($0.AuthResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.UpdateProfileRequest, $0.UserProfileResponse>(
            'UpdateProfile',
            updateProfile_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.UpdateProfileRequest.fromBuffer(value),
            ($0.UserProfileResponse value) => value.writeToBuffer()));
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
    $addMethod($grpc.ServiceMethod<$0.HealthRequest, $0.HealthResponse>(
        'Health',
        health_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.HealthRequest.fromBuffer(value),
        ($0.HealthResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.AuthResponse> register_Pre($grpc.ServiceCall $call,
      $async.Future<$0.RegisterRequest> $request) async {
    return register($call, await $request);
  }

  $async.Future<$0.AuthResponse> register(
      $grpc.ServiceCall call, $0.RegisterRequest request);

  $async.Future<$0.AuthResponse> login_Pre(
      $grpc.ServiceCall $call, $async.Future<$0.LoginRequest> $request) async {
    return login($call, await $request);
  }

  $async.Future<$0.AuthResponse> login(
      $grpc.ServiceCall call, $0.LoginRequest request);

  $async.Future<$0.UserProfileResponse> updateProfile_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.UpdateProfileRequest> $request) async {
    return updateProfile($call, await $request);
  }

  $async.Future<$0.UserProfileResponse> updateProfile(
      $grpc.ServiceCall call, $0.UpdateProfileRequest request);

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

  $async.Future<$0.HealthResponse> health_Pre(
      $grpc.ServiceCall $call, $async.Future<$0.HealthRequest> $request) async {
    return health($call, await $request);
  }

  $async.Future<$0.HealthResponse> health(
      $grpc.ServiceCall call, $0.HealthRequest request);
}
