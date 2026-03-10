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

/// ConfigApi — выдача персонального SS URI по user_id и проверка обновлений приложения.
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
