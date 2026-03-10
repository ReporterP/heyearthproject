// Generated from config.proto. Do not edit by hand.

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:grpc/grpc.dart' as $grpc;
import 'package:protobuf/protobuf.dart' as $pb;

import 'config.pb.dart' as $0;

export 'config.pb.dart';

@$pb.GrpcServiceName('heyearth.v1.ConfigApi')
class ConfigApiClient extends $grpc.Client {
  static final _$getConfig =
      $grpc.ClientMethod<$0.GetConfigRequest, $0.GetConfigResponse>(
    '/heyearth.v1.ConfigApi/GetConfig',
    ($0.GetConfigRequest value) => value.writeToBuffer(),
    $0.GetConfigResponse.fromBuffer,
  );
  static final _$health =
      $grpc.ClientMethod<$0.HealthRequest, $0.HealthResponse>(
    '/heyearth.v1.ConfigApi/Health',
    ($0.HealthRequest value) => value.writeToBuffer(),
    $0.HealthResponse.fromBuffer,
  );
  static final _$checkAppUpdate =
      $grpc.ClientMethod<$0.CheckAppUpdateRequest, $0.CheckAppUpdateResponse>(
    '/heyearth.v1.ConfigApi/CheckAppUpdate',
    ($0.CheckAppUpdateRequest value) => value.writeToBuffer(),
    $0.CheckAppUpdateResponse.fromBuffer,
  );

  ConfigApiClient($grpc.ClientChannel super.channel,
      {$grpc.CallOptions? options, super.interceptors});

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
}
