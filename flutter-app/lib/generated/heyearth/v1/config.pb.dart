// This is a generated file - do not edit.
//
// Generated from heyearth/v1/config.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

class RegisterRequest extends $pb.GeneratedMessage {
  factory RegisterRequest({
    DeviceContext? device,
  }) {
    final result = create();
    if (device != null) result.device = device;
    return result;
  }

  RegisterRequest._();

  factory RegisterRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory RegisterRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'RegisterRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOM<DeviceContext>(1, _omitFieldNames ? '' : 'device',
        subBuilder: DeviceContext.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RegisterRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RegisterRequest copyWith(void Function(RegisterRequest) updates) =>
      super.copyWith((message) => updates(message as RegisterRequest))
          as RegisterRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static RegisterRequest create() => RegisterRequest._();
  @$core.override
  RegisterRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static RegisterRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<RegisterRequest>(create);
  static RegisterRequest? _defaultInstance;

  @$pb.TagNumber(1)
  DeviceContext get device => $_getN(0);
  @$pb.TagNumber(1)
  set device(DeviceContext value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasDevice() => $_has(0);
  @$pb.TagNumber(1)
  void clearDevice() => $_clearField(1);
  @$pb.TagNumber(1)
  DeviceContext ensureDevice() => $_ensure(0);
}

class LoginRequest extends $pb.GeneratedMessage {
  factory LoginRequest({
    $core.String? idOrUsername,
    $core.String? password,
    DeviceContext? device,
  }) {
    final result = create();
    if (idOrUsername != null) result.idOrUsername = idOrUsername;
    if (password != null) result.password = password;
    if (device != null) result.device = device;
    return result;
  }

  LoginRequest._();

  factory LoginRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LoginRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LoginRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'idOrUsername')
    ..aOS(2, _omitFieldNames ? '' : 'password')
    ..aOM<DeviceContext>(3, _omitFieldNames ? '' : 'device',
        subBuilder: DeviceContext.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LoginRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LoginRequest copyWith(void Function(LoginRequest) updates) =>
      super.copyWith((message) => updates(message as LoginRequest))
          as LoginRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LoginRequest create() => LoginRequest._();
  @$core.override
  LoginRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LoginRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<LoginRequest>(create);
  static LoginRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get idOrUsername => $_getSZ(0);
  @$pb.TagNumber(1)
  set idOrUsername($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIdOrUsername() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdOrUsername() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get password => $_getSZ(1);
  @$pb.TagNumber(2)
  set password($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasPassword() => $_has(1);
  @$pb.TagNumber(2)
  void clearPassword() => $_clearField(2);

  @$pb.TagNumber(3)
  DeviceContext get device => $_getN(2);
  @$pb.TagNumber(3)
  set device(DeviceContext value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasDevice() => $_has(2);
  @$pb.TagNumber(3)
  void clearDevice() => $_clearField(3);
  @$pb.TagNumber(3)
  DeviceContext ensureDevice() => $_ensure(2);
}

class AuthResponse extends $pb.GeneratedMessage {
  factory AuthResponse({
    $core.String? userId,
    $core.String? token,
    UserProfileResponse? profile,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (token != null) result.token = token;
    if (profile != null) result.profile = profile;
    return result;
  }

  AuthResponse._();

  factory AuthResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AuthResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AuthResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'token')
    ..aOM<UserProfileResponse>(3, _omitFieldNames ? '' : 'profile',
        subBuilder: UserProfileResponse.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AuthResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AuthResponse copyWith(void Function(AuthResponse) updates) =>
      super.copyWith((message) => updates(message as AuthResponse))
          as AuthResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AuthResponse create() => AuthResponse._();
  @$core.override
  AuthResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AuthResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AuthResponse>(create);
  static AuthResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get token => $_getSZ(1);
  @$pb.TagNumber(2)
  set token($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasToken() => $_has(1);
  @$pb.TagNumber(2)
  void clearToken() => $_clearField(2);

  @$pb.TagNumber(3)
  UserProfileResponse get profile => $_getN(2);
  @$pb.TagNumber(3)
  set profile(UserProfileResponse value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasProfile() => $_has(2);
  @$pb.TagNumber(3)
  void clearProfile() => $_clearField(3);
  @$pb.TagNumber(3)
  UserProfileResponse ensureProfile() => $_ensure(2);
}

class UpdateProfileRequest extends $pb.GeneratedMessage {
  factory UpdateProfileRequest({
    $core.String? userId,
    $core.String? username,
    $core.String? newPassword,
    $core.String? currentPassword,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (username != null) result.username = username;
    if (newPassword != null) result.newPassword = newPassword;
    if (currentPassword != null) result.currentPassword = currentPassword;
    return result;
  }

  UpdateProfileRequest._();

  factory UpdateProfileRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory UpdateProfileRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'UpdateProfileRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'username')
    ..aOS(3, _omitFieldNames ? '' : 'newPassword')
    ..aOS(4, _omitFieldNames ? '' : 'currentPassword')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UpdateProfileRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UpdateProfileRequest copyWith(void Function(UpdateProfileRequest) updates) =>
      super.copyWith((message) => updates(message as UpdateProfileRequest))
          as UpdateProfileRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static UpdateProfileRequest create() => UpdateProfileRequest._();
  @$core.override
  UpdateProfileRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static UpdateProfileRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<UpdateProfileRequest>(create);
  static UpdateProfileRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get username => $_getSZ(1);
  @$pb.TagNumber(2)
  set username($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasUsername() => $_has(1);
  @$pb.TagNumber(2)
  void clearUsername() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get newPassword => $_getSZ(2);
  @$pb.TagNumber(3)
  set newPassword($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNewPassword() => $_has(2);
  @$pb.TagNumber(3)
  void clearNewPassword() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get currentPassword => $_getSZ(3);
  @$pb.TagNumber(4)
  set currentPassword($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasCurrentPassword() => $_has(3);
  @$pb.TagNumber(4)
  void clearCurrentPassword() => $_clearField(4);
}

class DeviceContext extends $pb.GeneratedMessage {
  factory DeviceContext({
    $core.String? deviceId,
    $core.String? platform,
    $core.String? deviceLabel,
    $core.String? locale,
    $core.String? linkedVia,
  }) {
    final result = create();
    if (deviceId != null) result.deviceId = deviceId;
    if (platform != null) result.platform = platform;
    if (deviceLabel != null) result.deviceLabel = deviceLabel;
    if (locale != null) result.locale = locale;
    if (linkedVia != null) result.linkedVia = linkedVia;
    return result;
  }

  DeviceContext._();

  factory DeviceContext.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeviceContext.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeviceContext',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'deviceId')
    ..aOS(2, _omitFieldNames ? '' : 'platform')
    ..aOS(3, _omitFieldNames ? '' : 'deviceLabel')
    ..aOS(4, _omitFieldNames ? '' : 'locale')
    ..aOS(5, _omitFieldNames ? '' : 'linkedVia')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeviceContext clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeviceContext copyWith(void Function(DeviceContext) updates) =>
      super.copyWith((message) => updates(message as DeviceContext))
          as DeviceContext;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeviceContext create() => DeviceContext._();
  @$core.override
  DeviceContext createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeviceContext getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeviceContext>(create);
  static DeviceContext? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get deviceId => $_getSZ(0);
  @$pb.TagNumber(1)
  set deviceId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDeviceId() => $_has(0);
  @$pb.TagNumber(1)
  void clearDeviceId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get platform => $_getSZ(1);
  @$pb.TagNumber(2)
  set platform($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasPlatform() => $_has(1);
  @$pb.TagNumber(2)
  void clearPlatform() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get deviceLabel => $_getSZ(2);
  @$pb.TagNumber(3)
  set deviceLabel($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasDeviceLabel() => $_has(2);
  @$pb.TagNumber(3)
  void clearDeviceLabel() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get locale => $_getSZ(3);
  @$pb.TagNumber(4)
  set locale($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasLocale() => $_has(3);
  @$pb.TagNumber(4)
  void clearLocale() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get linkedVia => $_getSZ(4);
  @$pb.TagNumber(5)
  set linkedVia($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasLinkedVia() => $_has(4);
  @$pb.TagNumber(5)
  void clearLinkedVia() => $_clearField(5);
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

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  HealthRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  HealthRequest copyWith(void Function(HealthRequest) updates) =>
      super.copyWith((message) => updates(message as HealthRequest))
          as HealthRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static HealthRequest create() => HealthRequest._();
  @$core.override
  HealthRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static HealthRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<HealthRequest>(create);
  static HealthRequest? _defaultInstance;
}

class HealthResponse extends $pb.GeneratedMessage {
  factory HealthResponse({
    $core.String? status,
  }) {
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

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  HealthResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  HealthResponse copyWith(void Function(HealthResponse) updates) =>
      super.copyWith((message) => updates(message as HealthResponse))
          as HealthResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static HealthResponse create() => HealthResponse._();
  @$core.override
  HealthResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static HealthResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<HealthResponse>(create);
  static HealthResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get status => $_getSZ(0);
  @$pb.TagNumber(1)
  set status($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasStatus() => $_has(0);
  @$pb.TagNumber(1)
  void clearStatus() => $_clearField(1);
}

class CheckAppUpdateRequest extends $pb.GeneratedMessage {
  factory CheckAppUpdateRequest({
    $core.String? platform,
  }) {
    final result = create();
    if (platform != null) result.platform = platform;
    return result;
  }

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
    ..aOS(1, _omitFieldNames ? '' : 'platform')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CheckAppUpdateRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CheckAppUpdateRequest copyWith(
          void Function(CheckAppUpdateRequest) updates) =>
      super.copyWith((message) => updates(message as CheckAppUpdateRequest))
          as CheckAppUpdateRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CheckAppUpdateRequest create() => CheckAppUpdateRequest._();
  @$core.override
  CheckAppUpdateRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CheckAppUpdateRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CheckAppUpdateRequest>(create);
  static CheckAppUpdateRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get platform => $_getSZ(0);
  @$pb.TagNumber(1)
  set platform($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPlatform() => $_has(0);
  @$pb.TagNumber(1)
  void clearPlatform() => $_clearField(1);
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
    $core.bool? mandatory,
    $core.String? minSupportedVersion,
    $core.Iterable<$core.String>? apiEndpoints,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (androidUrl != null) result.androidUrl = androidUrl;
    if (windowsUrl != null) result.windowsUrl = windowsUrl;
    if (macosUrl != null) result.macosUrl = macosUrl;
    if (linuxUrl != null) result.linuxUrl = linuxUrl;
    if (iosUrl != null) result.iosUrl = iosUrl;
    if (releaseNotes != null) result.releaseNotes = releaseNotes;
    if (mandatory != null) result.mandatory = mandatory;
    if (minSupportedVersion != null)
      result.minSupportedVersion = minSupportedVersion;
    if (apiEndpoints != null) result.apiEndpoints.addAll(apiEndpoints);
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
    ..aOB(8, _omitFieldNames ? '' : 'mandatory')
    ..aOS(9, _omitFieldNames ? '' : 'minSupportedVersion')
    ..pPS(10, _omitFieldNames ? '' : 'apiEndpoints')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CheckAppUpdateResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CheckAppUpdateResponse copyWith(
          void Function(CheckAppUpdateResponse) updates) =>
      super.copyWith((message) => updates(message as CheckAppUpdateResponse))
          as CheckAppUpdateResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CheckAppUpdateResponse create() => CheckAppUpdateResponse._();
  @$core.override
  CheckAppUpdateResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CheckAppUpdateResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CheckAppUpdateResponse>(create);
  static CheckAppUpdateResponse? _defaultInstance;

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

  @$pb.TagNumber(8)
  $core.bool get mandatory => $_getBF(7);
  @$pb.TagNumber(8)
  set mandatory($core.bool value) => $_setBool(7, value);
  @$pb.TagNumber(8)
  $core.bool hasMandatory() => $_has(7);
  @$pb.TagNumber(8)
  void clearMandatory() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.String get minSupportedVersion => $_getSZ(8);
  @$pb.TagNumber(9)
  set minSupportedVersion($core.String value) => $_setString(8, value);
  @$pb.TagNumber(9)
  $core.bool hasMinSupportedVersion() => $_has(8);
  @$pb.TagNumber(9)
  void clearMinSupportedVersion() => $_clearField(9);

  @$pb.TagNumber(10)
  $pb.PbList<$core.String> get apiEndpoints => $_getList(9);
}

class ApplyPromocodeRequest extends $pb.GeneratedMessage {
  factory ApplyPromocodeRequest({
    $core.String? userId,
    $core.String? code,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (code != null) result.code = code;
    return result;
  }

  ApplyPromocodeRequest._();

  factory ApplyPromocodeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplyPromocodeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplyPromocodeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'code')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyPromocodeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyPromocodeRequest copyWith(
          void Function(ApplyPromocodeRequest) updates) =>
      super.copyWith((message) => updates(message as ApplyPromocodeRequest))
          as ApplyPromocodeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplyPromocodeRequest create() => ApplyPromocodeRequest._();
  @$core.override
  ApplyPromocodeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ApplyPromocodeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplyPromocodeRequest>(create);
  static ApplyPromocodeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get code => $_getSZ(1);
  @$pb.TagNumber(2)
  set code($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCode() => $_has(1);
  @$pb.TagNumber(2)
  void clearCode() => $_clearField(2);
}

class ApplyPromocodeResponse extends $pb.GeneratedMessage {
  factory ApplyPromocodeResponse({
    $core.bool? success,
    $core.String? message,
  }) {
    final result = create();
    if (success != null) result.success = success;
    if (message != null) result.message = message;
    return result;
  }

  ApplyPromocodeResponse._();

  factory ApplyPromocodeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplyPromocodeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplyPromocodeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'success')
    ..aOS(2, _omitFieldNames ? '' : 'message')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyPromocodeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyPromocodeResponse copyWith(
          void Function(ApplyPromocodeResponse) updates) =>
      super.copyWith((message) => updates(message as ApplyPromocodeResponse))
          as ApplyPromocodeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplyPromocodeResponse create() => ApplyPromocodeResponse._();
  @$core.override
  ApplyPromocodeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ApplyPromocodeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplyPromocodeResponse>(create);
  static ApplyPromocodeResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get success => $_getBF(0);
  @$pb.TagNumber(1)
  set success($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSuccess() => $_has(0);
  @$pb.TagNumber(1)
  void clearSuccess() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get message => $_getSZ(1);
  @$pb.TagNumber(2)
  set message($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMessage() => $_has(1);
  @$pb.TagNumber(2)
  void clearMessage() => $_clearField(2);
}

class GetMeRequest extends $pb.GeneratedMessage {
  factory GetMeRequest({
    $core.String? userId,
    DeviceContext? device,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (device != null) result.device = device;
    return result;
  }

  GetMeRequest._();

  factory GetMeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetMeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetMeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOM<DeviceContext>(2, _omitFieldNames ? '' : 'device',
        subBuilder: DeviceContext.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetMeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetMeRequest copyWith(void Function(GetMeRequest) updates) =>
      super.copyWith((message) => updates(message as GetMeRequest))
          as GetMeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetMeRequest create() => GetMeRequest._();
  @$core.override
  GetMeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetMeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetMeRequest>(create);
  static GetMeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  DeviceContext get device => $_getN(1);
  @$pb.TagNumber(2)
  set device(DeviceContext value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasDevice() => $_has(1);
  @$pb.TagNumber(2)
  void clearDevice() => $_clearField(2);
  @$pb.TagNumber(2)
  DeviceContext ensureDevice() => $_ensure(1);
}

class UserProfileResponse extends $pb.GeneratedMessage {
  factory UserProfileResponse({
    $core.String? userId,
    $core.String? username,
    $core.bool? hasPassword,
    $core.String? preferredPlatform,
    $core.String? locale,
    $core.bool? isBlocked,
    $core.String? currentPlan,
    $core.String? validUntil,
    $core.String? accessStatus,
    $core.String? statusReason,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (username != null) result.username = username;
    if (hasPassword != null) result.hasPassword = hasPassword;
    if (preferredPlatform != null) result.preferredPlatform = preferredPlatform;
    if (locale != null) result.locale = locale;
    if (isBlocked != null) result.isBlocked = isBlocked;
    if (currentPlan != null) result.currentPlan = currentPlan;
    if (validUntil != null) result.validUntil = validUntil;
    if (accessStatus != null) result.accessStatus = accessStatus;
    if (statusReason != null) result.statusReason = statusReason;
    return result;
  }

  UserProfileResponse._();

  factory UserProfileResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory UserProfileResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'UserProfileResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'username')
    ..aOB(3, _omitFieldNames ? '' : 'hasPassword')
    ..aOS(4, _omitFieldNames ? '' : 'preferredPlatform')
    ..aOS(5, _omitFieldNames ? '' : 'locale')
    ..aOB(6, _omitFieldNames ? '' : 'isBlocked')
    ..aOS(7, _omitFieldNames ? '' : 'currentPlan')
    ..aOS(8, _omitFieldNames ? '' : 'validUntil')
    ..aOS(9, _omitFieldNames ? '' : 'accessStatus')
    ..aOS(10, _omitFieldNames ? '' : 'statusReason')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UserProfileResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UserProfileResponse copyWith(void Function(UserProfileResponse) updates) =>
      super.copyWith((message) => updates(message as UserProfileResponse))
          as UserProfileResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static UserProfileResponse create() => UserProfileResponse._();
  @$core.override
  UserProfileResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static UserProfileResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<UserProfileResponse>(create);
  static UserProfileResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get username => $_getSZ(1);
  @$pb.TagNumber(2)
  set username($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasUsername() => $_has(1);
  @$pb.TagNumber(2)
  void clearUsername() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get hasPassword => $_getBF(2);
  @$pb.TagNumber(3)
  set hasPassword($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasHasPassword() => $_has(2);
  @$pb.TagNumber(3)
  void clearHasPassword() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get preferredPlatform => $_getSZ(3);
  @$pb.TagNumber(4)
  set preferredPlatform($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasPreferredPlatform() => $_has(3);
  @$pb.TagNumber(4)
  void clearPreferredPlatform() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get locale => $_getSZ(4);
  @$pb.TagNumber(5)
  set locale($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasLocale() => $_has(4);
  @$pb.TagNumber(5)
  void clearLocale() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.bool get isBlocked => $_getBF(5);
  @$pb.TagNumber(6)
  set isBlocked($core.bool value) => $_setBool(5, value);
  @$pb.TagNumber(6)
  $core.bool hasIsBlocked() => $_has(5);
  @$pb.TagNumber(6)
  void clearIsBlocked() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get currentPlan => $_getSZ(6);
  @$pb.TagNumber(7)
  set currentPlan($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasCurrentPlan() => $_has(6);
  @$pb.TagNumber(7)
  void clearCurrentPlan() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.String get validUntil => $_getSZ(7);
  @$pb.TagNumber(8)
  set validUntil($core.String value) => $_setString(7, value);
  @$pb.TagNumber(8)
  $core.bool hasValidUntil() => $_has(7);
  @$pb.TagNumber(8)
  void clearValidUntil() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.String get accessStatus => $_getSZ(8);
  @$pb.TagNumber(9)
  set accessStatus($core.String value) => $_setString(8, value);
  @$pb.TagNumber(9)
  $core.bool hasAccessStatus() => $_has(8);
  @$pb.TagNumber(9)
  void clearAccessStatus() => $_clearField(9);

  @$pb.TagNumber(10)
  $core.String get statusReason => $_getSZ(9);
  @$pb.TagNumber(10)
  set statusReason($core.String value) => $_setString(9, value);
  @$pb.TagNumber(10)
  $core.bool hasStatusReason() => $_has(9);
  @$pb.TagNumber(10)
  void clearStatusReason() => $_clearField(10);
}

class GetAccessStateRequest extends $pb.GeneratedMessage {
  factory GetAccessStateRequest({
    $core.String? userId,
    $core.String? platform,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (platform != null) result.platform = platform;
    return result;
  }

  GetAccessStateRequest._();

  factory GetAccessStateRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetAccessStateRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetAccessStateRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'platform')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAccessStateRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAccessStateRequest copyWith(
          void Function(GetAccessStateRequest) updates) =>
      super.copyWith((message) => updates(message as GetAccessStateRequest))
          as GetAccessStateRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetAccessStateRequest create() => GetAccessStateRequest._();
  @$core.override
  GetAccessStateRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetAccessStateRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetAccessStateRequest>(create);
  static GetAccessStateRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get platform => $_getSZ(1);
  @$pb.TagNumber(2)
  set platform($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasPlatform() => $_has(1);
  @$pb.TagNumber(2)
  void clearPlatform() => $_clearField(2);
}

class AccessStateResponse extends $pb.GeneratedMessage {
  factory AccessStateResponse({
    $core.String? userId,
    $core.String? currentPlan,
    $core.String? validUntil,
    $core.String? preferredPlatform,
    $core.bool? isBlocked,
    $core.bool? canConnect,
    $core.String? reason,
    $core.bool? isPremium,
    $core.bool? trafficMaskingAvailable,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (currentPlan != null) result.currentPlan = currentPlan;
    if (validUntil != null) result.validUntil = validUntil;
    if (preferredPlatform != null) result.preferredPlatform = preferredPlatform;
    if (isBlocked != null) result.isBlocked = isBlocked;
    if (canConnect != null) result.canConnect = canConnect;
    if (reason != null) result.reason = reason;
    if (isPremium != null) result.isPremium = isPremium;
    if (trafficMaskingAvailable != null)
      result.trafficMaskingAvailable = trafficMaskingAvailable;
    return result;
  }

  AccessStateResponse._();

  factory AccessStateResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AccessStateResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AccessStateResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'currentPlan')
    ..aOS(3, _omitFieldNames ? '' : 'validUntil')
    ..aOS(4, _omitFieldNames ? '' : 'preferredPlatform')
    ..aOB(5, _omitFieldNames ? '' : 'isBlocked')
    ..aOB(6, _omitFieldNames ? '' : 'canConnect')
    ..aOS(7, _omitFieldNames ? '' : 'reason')
    ..aOB(8, _omitFieldNames ? '' : 'isPremium')
    ..aOB(9, _omitFieldNames ? '' : 'trafficMaskingAvailable')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AccessStateResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AccessStateResponse copyWith(void Function(AccessStateResponse) updates) =>
      super.copyWith((message) => updates(message as AccessStateResponse))
          as AccessStateResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AccessStateResponse create() => AccessStateResponse._();
  @$core.override
  AccessStateResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AccessStateResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AccessStateResponse>(create);
  static AccessStateResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get currentPlan => $_getSZ(1);
  @$pb.TagNumber(2)
  set currentPlan($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCurrentPlan() => $_has(1);
  @$pb.TagNumber(2)
  void clearCurrentPlan() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get validUntil => $_getSZ(2);
  @$pb.TagNumber(3)
  set validUntil($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasValidUntil() => $_has(2);
  @$pb.TagNumber(3)
  void clearValidUntil() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get preferredPlatform => $_getSZ(3);
  @$pb.TagNumber(4)
  set preferredPlatform($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasPreferredPlatform() => $_has(3);
  @$pb.TagNumber(4)
  void clearPreferredPlatform() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.bool get isBlocked => $_getBF(4);
  @$pb.TagNumber(5)
  set isBlocked($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasIsBlocked() => $_has(4);
  @$pb.TagNumber(5)
  void clearIsBlocked() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.bool get canConnect => $_getBF(5);
  @$pb.TagNumber(6)
  set canConnect($core.bool value) => $_setBool(5, value);
  @$pb.TagNumber(6)
  $core.bool hasCanConnect() => $_has(5);
  @$pb.TagNumber(6)
  void clearCanConnect() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get reason => $_getSZ(6);
  @$pb.TagNumber(7)
  set reason($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasReason() => $_has(6);
  @$pb.TagNumber(7)
  void clearReason() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.bool get isPremium => $_getBF(7);
  @$pb.TagNumber(8)
  set isPremium($core.bool value) => $_setBool(7, value);
  @$pb.TagNumber(8)
  $core.bool hasIsPremium() => $_has(7);
  @$pb.TagNumber(8)
  void clearIsPremium() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.bool get trafficMaskingAvailable => $_getBF(8);
  @$pb.TagNumber(9)
  set trafficMaskingAvailable($core.bool value) => $_setBool(8, value);
  @$pb.TagNumber(9)
  $core.bool hasTrafficMaskingAvailable() => $_has(8);
  @$pb.TagNumber(9)
  void clearTrafficMaskingAvailable() => $_clearField(9);
}

class GetConnectionBundleRequest extends $pb.GeneratedMessage {
  factory GetConnectionBundleRequest({
    $core.String? userId,
    $core.String? platform,
    $core.bool? forceRefresh,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (platform != null) result.platform = platform;
    if (forceRefresh != null) result.forceRefresh = forceRefresh;
    return result;
  }

  GetConnectionBundleRequest._();

  factory GetConnectionBundleRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetConnectionBundleRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetConnectionBundleRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..aOS(2, _omitFieldNames ? '' : 'platform')
    ..aOB(3, _omitFieldNames ? '' : 'forceRefresh')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetConnectionBundleRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetConnectionBundleRequest copyWith(
          void Function(GetConnectionBundleRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetConnectionBundleRequest))
          as GetConnectionBundleRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetConnectionBundleRequest create() => GetConnectionBundleRequest._();
  @$core.override
  GetConnectionBundleRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetConnectionBundleRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetConnectionBundleRequest>(create);
  static GetConnectionBundleRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get platform => $_getSZ(1);
  @$pb.TagNumber(2)
  set platform($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasPlatform() => $_has(1);
  @$pb.TagNumber(2)
  void clearPlatform() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get forceRefresh => $_getBF(2);
  @$pb.TagNumber(3)
  set forceRefresh($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasForceRefresh() => $_has(2);
  @$pb.TagNumber(3)
  void clearForceRefresh() => $_clearField(3);
}

class ConnectionOption extends $pb.GeneratedMessage {
  factory ConnectionOption({
    $core.String? uri,
    $core.String? protocol,
    $core.String? label,
    $core.Iterable<$core.String>? recommendedNetworks,
    $core.int? priority,
  }) {
    final result = create();
    if (uri != null) result.uri = uri;
    if (protocol != null) result.protocol = protocol;
    if (label != null) result.label = label;
    if (recommendedNetworks != null)
      result.recommendedNetworks.addAll(recommendedNetworks);
    if (priority != null) result.priority = priority;
    return result;
  }

  ConnectionOption._();

  factory ConnectionOption.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ConnectionOption.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ConnectionOption',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'uri')
    ..aOS(2, _omitFieldNames ? '' : 'protocol')
    ..aOS(3, _omitFieldNames ? '' : 'label')
    ..pPS(4, _omitFieldNames ? '' : 'recommendedNetworks')
    ..aI(5, _omitFieldNames ? '' : 'priority')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ConnectionOption clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ConnectionOption copyWith(void Function(ConnectionOption) updates) =>
      super.copyWith((message) => updates(message as ConnectionOption))
          as ConnectionOption;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ConnectionOption create() => ConnectionOption._();
  @$core.override
  ConnectionOption createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ConnectionOption getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ConnectionOption>(create);
  static ConnectionOption? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get uri => $_getSZ(0);
  @$pb.TagNumber(1)
  set uri($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUri() => $_has(0);
  @$pb.TagNumber(1)
  void clearUri() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get protocol => $_getSZ(1);
  @$pb.TagNumber(2)
  set protocol($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasProtocol() => $_has(1);
  @$pb.TagNumber(2)
  void clearProtocol() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get label => $_getSZ(2);
  @$pb.TagNumber(3)
  set label($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasLabel() => $_has(2);
  @$pb.TagNumber(3)
  void clearLabel() => $_clearField(3);

  @$pb.TagNumber(4)
  $pb.PbList<$core.String> get recommendedNetworks => $_getList(3);

  @$pb.TagNumber(5)
  $core.int get priority => $_getIZ(4);
  @$pb.TagNumber(5)
  set priority($core.int value) => $_setSignedInt32(4, value);
  @$pb.TagNumber(5)
  $core.bool hasPriority() => $_has(4);
  @$pb.TagNumber(5)
  void clearPriority() => $_clearField(5);
}

class ConnectionBundleResponse extends $pb.GeneratedMessage {
  factory ConnectionBundleResponse({
    $core.String? userId,
    $core.Iterable<ConnectionOption>? connectionOptions,
    $core.String? statusMessage,
    $core.bool? trafficMaskingEnabled,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    if (connectionOptions != null)
      result.connectionOptions.addAll(connectionOptions);
    if (statusMessage != null) result.statusMessage = statusMessage;
    if (trafficMaskingEnabled != null)
      result.trafficMaskingEnabled = trafficMaskingEnabled;
    return result;
  }

  ConnectionBundleResponse._();

  factory ConnectionBundleResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ConnectionBundleResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ConnectionBundleResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..pPM<ConnectionOption>(2, _omitFieldNames ? '' : 'connectionOptions',
        subBuilder: ConnectionOption.create)
    ..aOS(3, _omitFieldNames ? '' : 'statusMessage')
    ..aOB(4, _omitFieldNames ? '' : 'trafficMaskingEnabled')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ConnectionBundleResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ConnectionBundleResponse copyWith(
          void Function(ConnectionBundleResponse) updates) =>
      super.copyWith((message) => updates(message as ConnectionBundleResponse))
          as ConnectionBundleResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ConnectionBundleResponse create() => ConnectionBundleResponse._();
  @$core.override
  ConnectionBundleResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ConnectionBundleResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ConnectionBundleResponse>(create);
  static ConnectionBundleResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<ConnectionOption> get connectionOptions => $_getList(1);

  @$pb.TagNumber(3)
  $core.String get statusMessage => $_getSZ(2);
  @$pb.TagNumber(3)
  set statusMessage($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasStatusMessage() => $_has(2);
  @$pb.TagNumber(3)
  void clearStatusMessage() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.bool get trafficMaskingEnabled => $_getBF(3);
  @$pb.TagNumber(4)
  set trafficMaskingEnabled($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasTrafficMaskingEnabled() => $_has(3);
  @$pb.TagNumber(4)
  void clearTrafficMaskingEnabled() => $_clearField(4);
}

class ListSubscriptionsRequest extends $pb.GeneratedMessage {
  factory ListSubscriptionsRequest({
    $core.String? userId,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    return result;
  }

  ListSubscriptionsRequest._();

  factory ListSubscriptionsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListSubscriptionsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListSubscriptionsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSubscriptionsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSubscriptionsRequest copyWith(
          void Function(ListSubscriptionsRequest) updates) =>
      super.copyWith((message) => updates(message as ListSubscriptionsRequest))
          as ListSubscriptionsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListSubscriptionsRequest create() => ListSubscriptionsRequest._();
  @$core.override
  ListSubscriptionsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListSubscriptionsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListSubscriptionsRequest>(create);
  static ListSubscriptionsRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);
}

class SubscriptionItem extends $pb.GeneratedMessage {
  factory SubscriptionItem({
    $core.int? groupId,
    $core.String? groupName,
    $core.String? validUntil,
    $core.bool? autoRenew,
    $core.bool? active,
    $core.String? plan,
    $core.int? priceRub,
  }) {
    final result = create();
    if (groupId != null) result.groupId = groupId;
    if (groupName != null) result.groupName = groupName;
    if (validUntil != null) result.validUntil = validUntil;
    if (autoRenew != null) result.autoRenew = autoRenew;
    if (active != null) result.active = active;
    if (plan != null) result.plan = plan;
    if (priceRub != null) result.priceRub = priceRub;
    return result;
  }

  SubscriptionItem._();

  factory SubscriptionItem.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SubscriptionItem.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SubscriptionItem',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'groupId')
    ..aOS(2, _omitFieldNames ? '' : 'groupName')
    ..aOS(3, _omitFieldNames ? '' : 'validUntil')
    ..aOB(4, _omitFieldNames ? '' : 'autoRenew')
    ..aOB(5, _omitFieldNames ? '' : 'active')
    ..aOS(6, _omitFieldNames ? '' : 'plan')
    ..aI(7, _omitFieldNames ? '' : 'priceRub')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscriptionItem clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscriptionItem copyWith(void Function(SubscriptionItem) updates) =>
      super.copyWith((message) => updates(message as SubscriptionItem))
          as SubscriptionItem;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SubscriptionItem create() => SubscriptionItem._();
  @$core.override
  SubscriptionItem createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SubscriptionItem getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SubscriptionItem>(create);
  static SubscriptionItem? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get groupId => $_getIZ(0);
  @$pb.TagNumber(1)
  set groupId($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasGroupId() => $_has(0);
  @$pb.TagNumber(1)
  void clearGroupId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get groupName => $_getSZ(1);
  @$pb.TagNumber(2)
  set groupName($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasGroupName() => $_has(1);
  @$pb.TagNumber(2)
  void clearGroupName() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get validUntil => $_getSZ(2);
  @$pb.TagNumber(3)
  set validUntil($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasValidUntil() => $_has(2);
  @$pb.TagNumber(3)
  void clearValidUntil() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.bool get autoRenew => $_getBF(3);
  @$pb.TagNumber(4)
  set autoRenew($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasAutoRenew() => $_has(3);
  @$pb.TagNumber(4)
  void clearAutoRenew() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.bool get active => $_getBF(4);
  @$pb.TagNumber(5)
  set active($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasActive() => $_has(4);
  @$pb.TagNumber(5)
  void clearActive() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get plan => $_getSZ(5);
  @$pb.TagNumber(6)
  set plan($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasPlan() => $_has(5);
  @$pb.TagNumber(6)
  void clearPlan() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.int get priceRub => $_getIZ(6);
  @$pb.TagNumber(7)
  set priceRub($core.int value) => $_setSignedInt32(6, value);
  @$pb.TagNumber(7)
  $core.bool hasPriceRub() => $_has(6);
  @$pb.TagNumber(7)
  void clearPriceRub() => $_clearField(7);
}

class ListSubscriptionsResponse extends $pb.GeneratedMessage {
  factory ListSubscriptionsResponse({
    $core.Iterable<SubscriptionItem>? items,
  }) {
    final result = create();
    if (items != null) result.items.addAll(items);
    return result;
  }

  ListSubscriptionsResponse._();

  factory ListSubscriptionsResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListSubscriptionsResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListSubscriptionsResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..pPM<SubscriptionItem>(1, _omitFieldNames ? '' : 'items',
        subBuilder: SubscriptionItem.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSubscriptionsResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSubscriptionsResponse copyWith(
          void Function(ListSubscriptionsResponse) updates) =>
      super.copyWith((message) => updates(message as ListSubscriptionsResponse))
          as ListSubscriptionsResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListSubscriptionsResponse create() => ListSubscriptionsResponse._();
  @$core.override
  ListSubscriptionsResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListSubscriptionsResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListSubscriptionsResponse>(create);
  static ListSubscriptionsResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SubscriptionItem> get items => $_getList(0);
}

class ListPaymentsRequest extends $pb.GeneratedMessage {
  factory ListPaymentsRequest({
    $core.String? userId,
  }) {
    final result = create();
    if (userId != null) result.userId = userId;
    return result;
  }

  ListPaymentsRequest._();

  factory ListPaymentsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListPaymentsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListPaymentsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'userId')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPaymentsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPaymentsRequest copyWith(void Function(ListPaymentsRequest) updates) =>
      super.copyWith((message) => updates(message as ListPaymentsRequest))
          as ListPaymentsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListPaymentsRequest create() => ListPaymentsRequest._();
  @$core.override
  ListPaymentsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListPaymentsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListPaymentsRequest>(create);
  static ListPaymentsRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get userId => $_getSZ(0);
  @$pb.TagNumber(1)
  set userId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasUserId() => $_has(0);
  @$pb.TagNumber(1)
  void clearUserId() => $_clearField(1);
}

class PaymentItem extends $pb.GeneratedMessage {
  factory PaymentItem({
    $core.int? id,
    $core.double? amount,
    $core.String? currency,
    $core.String? status,
    $core.String? provider,
    $core.String? externalId,
    $core.String? notes,
    $core.String? createdAt,
    $core.String? confirmedAt,
  }) {
    final result = create();
    if (id != null) result.id = id;
    if (amount != null) result.amount = amount;
    if (currency != null) result.currency = currency;
    if (status != null) result.status = status;
    if (provider != null) result.provider = provider;
    if (externalId != null) result.externalId = externalId;
    if (notes != null) result.notes = notes;
    if (createdAt != null) result.createdAt = createdAt;
    if (confirmedAt != null) result.confirmedAt = confirmedAt;
    return result;
  }

  PaymentItem._();

  factory PaymentItem.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PaymentItem.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PaymentItem',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'id')
    ..aD(2, _omitFieldNames ? '' : 'amount')
    ..aOS(3, _omitFieldNames ? '' : 'currency')
    ..aOS(4, _omitFieldNames ? '' : 'status')
    ..aOS(5, _omitFieldNames ? '' : 'provider')
    ..aOS(6, _omitFieldNames ? '' : 'externalId')
    ..aOS(7, _omitFieldNames ? '' : 'notes')
    ..aOS(8, _omitFieldNames ? '' : 'createdAt')
    ..aOS(9, _omitFieldNames ? '' : 'confirmedAt')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PaymentItem clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PaymentItem copyWith(void Function(PaymentItem) updates) =>
      super.copyWith((message) => updates(message as PaymentItem))
          as PaymentItem;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PaymentItem create() => PaymentItem._();
  @$core.override
  PaymentItem createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PaymentItem getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PaymentItem>(create);
  static PaymentItem? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get id => $_getIZ(0);
  @$pb.TagNumber(1)
  set id($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasId() => $_has(0);
  @$pb.TagNumber(1)
  void clearId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.double get amount => $_getN(1);
  @$pb.TagNumber(2)
  set amount($core.double value) => $_setDouble(1, value);
  @$pb.TagNumber(2)
  $core.bool hasAmount() => $_has(1);
  @$pb.TagNumber(2)
  void clearAmount() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get currency => $_getSZ(2);
  @$pb.TagNumber(3)
  set currency($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCurrency() => $_has(2);
  @$pb.TagNumber(3)
  void clearCurrency() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get status => $_getSZ(3);
  @$pb.TagNumber(4)
  set status($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasStatus() => $_has(3);
  @$pb.TagNumber(4)
  void clearStatus() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get provider => $_getSZ(4);
  @$pb.TagNumber(5)
  set provider($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasProvider() => $_has(4);
  @$pb.TagNumber(5)
  void clearProvider() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get externalId => $_getSZ(5);
  @$pb.TagNumber(6)
  set externalId($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasExternalId() => $_has(5);
  @$pb.TagNumber(6)
  void clearExternalId() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get notes => $_getSZ(6);
  @$pb.TagNumber(7)
  set notes($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasNotes() => $_has(6);
  @$pb.TagNumber(7)
  void clearNotes() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.String get createdAt => $_getSZ(7);
  @$pb.TagNumber(8)
  set createdAt($core.String value) => $_setString(7, value);
  @$pb.TagNumber(8)
  $core.bool hasCreatedAt() => $_has(7);
  @$pb.TagNumber(8)
  void clearCreatedAt() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.String get confirmedAt => $_getSZ(8);
  @$pb.TagNumber(9)
  set confirmedAt($core.String value) => $_setString(8, value);
  @$pb.TagNumber(9)
  $core.bool hasConfirmedAt() => $_has(8);
  @$pb.TagNumber(9)
  void clearConfirmedAt() => $_clearField(9);
}

class ListPaymentsResponse extends $pb.GeneratedMessage {
  factory ListPaymentsResponse({
    $core.Iterable<PaymentItem>? items,
  }) {
    final result = create();
    if (items != null) result.items.addAll(items);
    return result;
  }

  ListPaymentsResponse._();

  factory ListPaymentsResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListPaymentsResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListPaymentsResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'heyearth.v1'),
      createEmptyInstance: create)
    ..pPM<PaymentItem>(1, _omitFieldNames ? '' : 'items',
        subBuilder: PaymentItem.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPaymentsResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListPaymentsResponse copyWith(void Function(ListPaymentsResponse) updates) =>
      super.copyWith((message) => updates(message as ListPaymentsResponse))
          as ListPaymentsResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListPaymentsResponse create() => ListPaymentsResponse._();
  @$core.override
  ListPaymentsResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListPaymentsResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListPaymentsResponse>(create);
  static ListPaymentsResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<PaymentItem> get items => $_getList(0);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
