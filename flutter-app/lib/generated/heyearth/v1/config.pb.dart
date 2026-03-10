// Generated from config.proto. Do not edit by hand.

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

class GetConfigRequest extends $pb.GeneratedMessage {
  factory GetConfigRequest({$core.String? userId}) {
    final result = create();
    if (userId != null) result.userId = userId;
    return result;
  }
  GetConfigRequest._();
  factory GetConfigRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetConfigRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetConfigRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..hasRequiredFields = false;

  @$core.pragma('dart2js:noInline')
  static GetConfigRequest create() => GetConfigRequest._();
  @$core.override
  GetConfigRequest createEmptyInstance() => create();
  static GetConfigRequest? _defaultInstance;
  static GetConfigRequest getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor(create);

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$core.override
  $pb.BuilderInfo get info_ => _i;
}

class GetConfigResponse extends $pb.GeneratedMessage {
  factory GetConfigResponse({$core.String? userId, $core.String? ssUri}) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (ssUri != null) result.ssUri = ssUri;
    return result;
  }
  GetConfigResponse._();
  factory GetConfigResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetConfigResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetConfigResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'ssUri')
    ..hasRequiredFields = false;

  @$core.pragma('dart2js:noInline')
  static GetConfigResponse create() => GetConfigResponse._();
  @$core.override
  GetConfigResponse createEmptyInstance() => create();
  static GetConfigResponse? _defaultInstance;
  static GetConfigResponse getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor(create);

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get ssUri => $_getSZ(1);
  @$pb.TagNumber(2)
  set ssUri($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasSsUri() => $_has(1);
  @$pb.TagNumber(2)
  void clearSsUri() => $_clearField(2);

  @$core.override
  $pb.BuilderInfo get info_ => _i;
}

class HealthRequest extends $pb.GeneratedMessage {
  factory HealthRequest() => create();
  HealthRequest._();
  factory HealthRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory HealthRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'HealthRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.pragma('dart2js:noInline')
  static HealthRequest create() => HealthRequest._();
  @$core.override
  HealthRequest createEmptyInstance() => create();
  static HealthRequest? _defaultInstance;
  static HealthRequest getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor(create);

  @$core.override
  $pb.BuilderInfo get info_ => _i;
}

class HealthResponse extends $pb.GeneratedMessage {
  factory HealthResponse({$core.String? status}) {
    final result = create();
    if (status != null) result.status = status;
    return result;
  }
  HealthResponse._();
  factory HealthResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory HealthResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'HealthResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'status')
    ..hasRequiredFields = false;

  @$core.pragma('dart2js:noInline')
  static HealthResponse create() => HealthResponse._();
  @$core.override
  HealthResponse createEmptyInstance() => create();
  static HealthResponse? _defaultInstance;
  static HealthResponse getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor(create);

  @$pb.TagNumber(1)
  $core.String get status => $_getSZ(0);
  @$pb.TagNumber(1)
  set status($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasStatus() => $_has(0);
  @$pb.TagNumber(1)
  void clearStatus() => $_clearField(1);

  @$core.override
  $pb.BuilderInfo get info_ => _i;
}

class CheckAppUpdateRequest extends $pb.GeneratedMessage {
  factory CheckAppUpdateRequest() => create();
  CheckAppUpdateRequest._();
  factory CheckAppUpdateRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CheckAppUpdateRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CheckAppUpdateRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.pragma('dart2js:noInline')
  static CheckAppUpdateRequest create() => CheckAppUpdateRequest._();
  @$core.override
  CheckAppUpdateRequest createEmptyInstance() => create();
  static CheckAppUpdateRequest? _defaultInstance;
  static CheckAppUpdateRequest getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor(create);

  @$core.override
  $pb.BuilderInfo get info_ => _i;
}

class CheckAppUpdateResponse extends $pb.GeneratedMessage {
  factory CheckAppUpdateResponse({
    $core.String? version,
    $core.String? androidUrl,
    $core.String? windowsUrl,
    $core.String? macosUrl,
    $core.String? linuxUrl,
    $core.String? iosUrl,
    $core.String? releaseNotes,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (androidUrl != null) result.androidUrl = androidUrl;
    if (windowsUrl != null) result.windowsUrl = windowsUrl;
    if (macosUrl != null) result.macosUrl = macosUrl;
    if (linuxUrl != null) result.linuxUrl = linuxUrl;
    if (iosUrl != null) result.iosUrl = iosUrl;
    if (releaseNotes != null) result.releaseNotes = releaseNotes;
    return result;
  }
  CheckAppUpdateResponse._();
  factory CheckAppUpdateResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CheckAppUpdateResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CheckAppUpdateResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'version')
    ..aOS(2, _omitFieldNames ? '' : 'androidUrl')
    ..aOS(3, _omitFieldNames ? '' : 'windowsUrl')
    ..aOS(4, _omitFieldNames ? '' : 'macosUrl')
    ..aOS(5, _omitFieldNames ? '' : 'linuxUrl')
    ..aOS(6, _omitFieldNames ? '' : 'iosUrl')
    ..aOS(7, _omitFieldNames ? '' : 'releaseNotes')
    ..hasRequiredFields = false;

  @$core.pragma('dart2js:noInline')
  static CheckAppUpdateResponse create() => CheckAppUpdateResponse._();
  @$core.override
  CheckAppUpdateResponse createEmptyInstance() => create();
  static CheckAppUpdateResponse? _defaultInstance;
  static CheckAppUpdateResponse getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor(create);

  @$pb.TagNumber(1)
  $core.String get version => $_getSZ(0);
  @$pb.TagNumber(1)
  set version($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get androidUrl => $_getSZ(1);
  @$pb.TagNumber(2)
  set androidUrl($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasAndroidUrl() => $_has(1);
  @$pb.TagNumber(2)
  void clearAndroidUrl() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get windowsUrl => $_getSZ(2);
  @$pb.TagNumber(3)
  set windowsUrl($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasWindowsUrl() => $_has(2);
  @$pb.TagNumber(3)
  void clearWindowsUrl() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get macosUrl => $_getSZ(3);
  @$pb.TagNumber(4)
  set macosUrl($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasMacosUrl() => $_has(3);
  @$pb.TagNumber(4)
  void clearMacosUrl() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get linuxUrl => $_getSZ(4);
  @$pb.TagNumber(5)
  set linuxUrl($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasLinuxUrl() => $_has(4);
  @$pb.TagNumber(5)
  void clearLinuxUrl() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get iosUrl => $_getSZ(5);
  @$pb.TagNumber(6)
  set iosUrl($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasIosUrl() => $_has(5);
  @$pb.TagNumber(6)
  void clearIosUrl() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get releaseNotes => $_getSZ(6);
  @$pb.TagNumber(7)
  set releaseNotes($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasReleaseNotes() => $_has(6);
  @$pb.TagNumber(7)
  void clearReleaseNotes() => $_clearField(7);

  @$core.override
  $pb.BuilderInfo get info_ => _i;
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names', defaultValue: false);
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names', defaultValue: false);
