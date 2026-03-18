// This is a generated file - do not edit.
//
// Generated from heyearth/v1/config.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports
// ignore_for_file: unused_import

import 'dart:convert' as $convert;
import 'dart:core' as $core;
import 'dart:typed_data' as $typed_data;

@$core.Deprecated('Use registerRequestDescriptor instead')
const RegisterRequest$json = {
  '1': 'RegisterRequest',
  '2': [
    {
      '1': 'device',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.DeviceContext',
      '10': 'device'
    },
  ],
};

/// Descriptor for `RegisterRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List registerRequestDescriptor = $convert.base64Decode(
    'Cg9SZWdpc3RlclJlcXVlc3QSMgoGZGV2aWNlGAEgASgLMhouaGV5ZWFydGgudjEuRGV2aWNlQ2'
    '9udGV4dFIGZGV2aWNl');

@$core.Deprecated('Use loginRequestDescriptor instead')
const LoginRequest$json = {
  '1': 'LoginRequest',
  '2': [
    {'1': 'id_or_username', '3': 1, '4': 1, '5': 9, '10': 'idOrUsername'},
    {'1': 'password', '3': 2, '4': 1, '5': 9, '10': 'password'},
    {
      '1': 'device',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.DeviceContext',
      '10': 'device'
    },
  ],
};

/// Descriptor for `LoginRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List loginRequestDescriptor = $convert.base64Decode(
    'CgxMb2dpblJlcXVlc3QSJAoOaWRfb3JfdXNlcm5hbWUYASABKAlSDGlkT3JVc2VybmFtZRIaCg'
    'hwYXNzd29yZBgCIAEoCVIIcGFzc3dvcmQSMgoGZGV2aWNlGAMgASgLMhouaGV5ZWFydGgudjEu'
    'RGV2aWNlQ29udGV4dFIGZGV2aWNl');

@$core.Deprecated('Use authResponseDescriptor instead')
const AuthResponse$json = {
  '1': 'AuthResponse',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'token', '3': 2, '4': 1, '5': 9, '10': 'token'},
    {
      '1': 'profile',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.UserProfileResponse',
      '10': 'profile'
    },
  ],
};

/// Descriptor for `AuthResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List authResponseDescriptor = $convert.base64Decode(
    'CgxBdXRoUmVzcG9uc2USFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklkEhQKBXRva2VuGAIgASgJUg'
    'V0b2tlbhI6Cgdwcm9maWxlGAMgASgLMiAuaGV5ZWFydGgudjEuVXNlclByb2ZpbGVSZXNwb25z'
    'ZVIHcHJvZmlsZQ==');

@$core.Deprecated('Use updateProfileRequestDescriptor instead')
const UpdateProfileRequest$json = {
  '1': 'UpdateProfileRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'username', '3': 2, '4': 1, '5': 9, '10': 'username'},
    {'1': 'new_password', '3': 3, '4': 1, '5': 9, '10': 'newPassword'},
    {'1': 'current_password', '3': 4, '4': 1, '5': 9, '10': 'currentPassword'},
  ],
};

/// Descriptor for `UpdateProfileRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List updateProfileRequestDescriptor = $convert.base64Decode(
    'ChRVcGRhdGVQcm9maWxlUmVxdWVzdBIXCgd1c2VyX2lkGAEgASgJUgZ1c2VySWQSGgoIdXNlcm'
    '5hbWUYAiABKAlSCHVzZXJuYW1lEiEKDG5ld19wYXNzd29yZBgDIAEoCVILbmV3UGFzc3dvcmQS'
    'KQoQY3VycmVudF9wYXNzd29yZBgEIAEoCVIPY3VycmVudFBhc3N3b3Jk');

@$core.Deprecated('Use deviceContextDescriptor instead')
const DeviceContext$json = {
  '1': 'DeviceContext',
  '2': [
    {'1': 'device_id', '3': 1, '4': 1, '5': 9, '10': 'deviceId'},
    {'1': 'platform', '3': 2, '4': 1, '5': 9, '10': 'platform'},
    {'1': 'device_label', '3': 3, '4': 1, '5': 9, '10': 'deviceLabel'},
    {'1': 'locale', '3': 4, '4': 1, '5': 9, '10': 'locale'},
    {'1': 'linked_via', '3': 5, '4': 1, '5': 9, '10': 'linkedVia'},
  ],
};

/// Descriptor for `DeviceContext`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deviceContextDescriptor = $convert.base64Decode(
    'Cg1EZXZpY2VDb250ZXh0EhsKCWRldmljZV9pZBgBIAEoCVIIZGV2aWNlSWQSGgoIcGxhdGZvcm'
    '0YAiABKAlSCHBsYXRmb3JtEiEKDGRldmljZV9sYWJlbBgDIAEoCVILZGV2aWNlTGFiZWwSFgoG'
    'bG9jYWxlGAQgASgJUgZsb2NhbGUSHQoKbGlua2VkX3ZpYRgFIAEoCVIJbGlua2VkVmlh');

@$core.Deprecated('Use healthRequestDescriptor instead')
const HealthRequest$json = {
  '1': 'HealthRequest',
};

/// Descriptor for `HealthRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List healthRequestDescriptor =
    $convert.base64Decode('Cg1IZWFsdGhSZXF1ZXN0');

@$core.Deprecated('Use healthResponseDescriptor instead')
const HealthResponse$json = {
  '1': 'HealthResponse',
  '2': [
    {'1': 'status', '3': 1, '4': 1, '5': 9, '10': 'status'},
  ],
};

/// Descriptor for `HealthResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List healthResponseDescriptor = $convert
    .base64Decode('Cg5IZWFsdGhSZXNwb25zZRIWCgZzdGF0dXMYASABKAlSBnN0YXR1cw==');

@$core.Deprecated('Use checkAppUpdateRequestDescriptor instead')
const CheckAppUpdateRequest$json = {
  '1': 'CheckAppUpdateRequest',
  '2': [
    {'1': 'platform', '3': 1, '4': 1, '5': 9, '10': 'platform'},
  ],
};

/// Descriptor for `CheckAppUpdateRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List checkAppUpdateRequestDescriptor =
    $convert.base64Decode(
        'ChVDaGVja0FwcFVwZGF0ZVJlcXVlc3QSGgoIcGxhdGZvcm0YASABKAlSCHBsYXRmb3Jt');

@$core.Deprecated('Use checkAppUpdateResponseDescriptor instead')
const CheckAppUpdateResponse$json = {
  '1': 'CheckAppUpdateResponse',
  '2': [
    {'1': 'version', '3': 1, '4': 1, '5': 9, '10': 'version'},
    {'1': 'android_url', '3': 2, '4': 1, '5': 9, '10': 'androidUrl'},
    {'1': 'windows_url', '3': 3, '4': 1, '5': 9, '10': 'windowsUrl'},
    {'1': 'macos_url', '3': 4, '4': 1, '5': 9, '10': 'macosUrl'},
    {'1': 'linux_url', '3': 5, '4': 1, '5': 9, '10': 'linuxUrl'},
    {'1': 'ios_url', '3': 6, '4': 1, '5': 9, '10': 'iosUrl'},
    {'1': 'release_notes', '3': 7, '4': 1, '5': 9, '10': 'releaseNotes'},
    {'1': 'mandatory', '3': 8, '4': 1, '5': 8, '10': 'mandatory'},
    {
      '1': 'min_supported_version',
      '3': 9,
      '4': 1,
      '5': 9,
      '10': 'minSupportedVersion'
    },
    {'1': 'api_endpoints', '3': 10, '4': 3, '5': 9, '10': 'apiEndpoints'},
  ],
};

/// Descriptor for `CheckAppUpdateResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List checkAppUpdateResponseDescriptor = $convert.base64Decode(
    'ChZDaGVja0FwcFVwZGF0ZVJlc3BvbnNlEhgKB3ZlcnNpb24YASABKAlSB3ZlcnNpb24SHwoLYW'
    '5kcm9pZF91cmwYAiABKAlSCmFuZHJvaWRVcmwSHwoLd2luZG93c191cmwYAyABKAlSCndpbmRv'
    'd3NVcmwSGwoJbWFjb3NfdXJsGAQgASgJUghtYWNvc1VybBIbCglsaW51eF91cmwYBSABKAlSCG'
    'xpbnV4VXJsEhcKB2lvc191cmwYBiABKAlSBmlvc1VybBIjCg1yZWxlYXNlX25vdGVzGAcgASgJ'
    'UgxyZWxlYXNlTm90ZXMSHAoJbWFuZGF0b3J5GAggASgIUgltYW5kYXRvcnkSMgoVbWluX3N1cH'
    'BvcnRlZF92ZXJzaW9uGAkgASgJUhNtaW5TdXBwb3J0ZWRWZXJzaW9uEiMKDWFwaV9lbmRwb2lu'
    'dHMYCiADKAlSDGFwaUVuZHBvaW50cw==');

@$core.Deprecated('Use applyPromocodeRequestDescriptor instead')
const ApplyPromocodeRequest$json = {
  '1': 'ApplyPromocodeRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'code', '3': 2, '4': 1, '5': 9, '10': 'code'},
  ],
};

/// Descriptor for `ApplyPromocodeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applyPromocodeRequestDescriptor = $convert.base64Decode(
    'ChVBcHBseVByb21vY29kZVJlcXVlc3QSFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklkEhIKBGNvZG'
    'UYAiABKAlSBGNvZGU=');

@$core.Deprecated('Use applyPromocodeResponseDescriptor instead')
const ApplyPromocodeResponse$json = {
  '1': 'ApplyPromocodeResponse',
  '2': [
    {'1': 'success', '3': 1, '4': 1, '5': 8, '10': 'success'},
    {'1': 'message', '3': 2, '4': 1, '5': 9, '10': 'message'},
  ],
};

/// Descriptor for `ApplyPromocodeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applyPromocodeResponseDescriptor =
    $convert.base64Decode(
        'ChZBcHBseVByb21vY29kZVJlc3BvbnNlEhgKB3N1Y2Nlc3MYASABKAhSB3N1Y2Nlc3MSGAoHbW'
        'Vzc2FnZRgCIAEoCVIHbWVzc2FnZQ==');

@$core.Deprecated('Use getMeRequestDescriptor instead')
const GetMeRequest$json = {
  '1': 'GetMeRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {
      '1': 'device',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.DeviceContext',
      '10': 'device'
    },
  ],
};

/// Descriptor for `GetMeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getMeRequestDescriptor = $convert.base64Decode(
    'CgxHZXRNZVJlcXVlc3QSFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklkEjIKBmRldmljZRgCIAEoCz'
    'IaLmhleWVhcnRoLnYxLkRldmljZUNvbnRleHRSBmRldmljZQ==');

@$core.Deprecated('Use userProfileResponseDescriptor instead')
const UserProfileResponse$json = {
  '1': 'UserProfileResponse',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'username', '3': 2, '4': 1, '5': 9, '10': 'username'},
    {'1': 'has_password', '3': 3, '4': 1, '5': 8, '10': 'hasPassword'},
    {
      '1': 'preferred_platform',
      '3': 4,
      '4': 1,
      '5': 9,
      '10': 'preferredPlatform'
    },
    {'1': 'locale', '3': 5, '4': 1, '5': 9, '10': 'locale'},
    {'1': 'is_blocked', '3': 6, '4': 1, '5': 8, '10': 'isBlocked'},
    {'1': 'current_plan', '3': 7, '4': 1, '5': 9, '10': 'currentPlan'},
    {'1': 'valid_until', '3': 8, '4': 1, '5': 9, '10': 'validUntil'},
    {'1': 'access_status', '3': 9, '4': 1, '5': 9, '10': 'accessStatus'},
    {'1': 'status_reason', '3': 10, '4': 1, '5': 9, '10': 'statusReason'},
  ],
};

/// Descriptor for `UserProfileResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List userProfileResponseDescriptor = $convert.base64Decode(
    'ChNVc2VyUHJvZmlsZVJlc3BvbnNlEhcKB3VzZXJfaWQYASABKAlSBnVzZXJJZBIaCgh1c2Vybm'
    'FtZRgCIAEoCVIIdXNlcm5hbWUSIQoMaGFzX3Bhc3N3b3JkGAMgASgIUgtoYXNQYXNzd29yZBIt'
    'ChJwcmVmZXJyZWRfcGxhdGZvcm0YBCABKAlSEXByZWZlcnJlZFBsYXRmb3JtEhYKBmxvY2FsZR'
    'gFIAEoCVIGbG9jYWxlEh0KCmlzX2Jsb2NrZWQYBiABKAhSCWlzQmxvY2tlZBIhCgxjdXJyZW50'
    'X3BsYW4YByABKAlSC2N1cnJlbnRQbGFuEh8KC3ZhbGlkX3VudGlsGAggASgJUgp2YWxpZFVudG'
    'lsEiMKDWFjY2Vzc19zdGF0dXMYCSABKAlSDGFjY2Vzc1N0YXR1cxIjCg1zdGF0dXNfcmVhc29u'
    'GAogASgJUgxzdGF0dXNSZWFzb24=');

@$core.Deprecated('Use getAccessStateRequestDescriptor instead')
const GetAccessStateRequest$json = {
  '1': 'GetAccessStateRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'platform', '3': 2, '4': 1, '5': 9, '10': 'platform'},
  ],
};

/// Descriptor for `GetAccessStateRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getAccessStateRequestDescriptor = $convert.base64Decode(
    'ChVHZXRBY2Nlc3NTdGF0ZVJlcXVlc3QSFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklkEhoKCHBsYX'
    'Rmb3JtGAIgASgJUghwbGF0Zm9ybQ==');

@$core.Deprecated('Use accessStateResponseDescriptor instead')
const AccessStateResponse$json = {
  '1': 'AccessStateResponse',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'current_plan', '3': 2, '4': 1, '5': 9, '10': 'currentPlan'},
    {'1': 'valid_until', '3': 3, '4': 1, '5': 9, '10': 'validUntil'},
    {
      '1': 'preferred_platform',
      '3': 4,
      '4': 1,
      '5': 9,
      '10': 'preferredPlatform'
    },
    {'1': 'is_blocked', '3': 5, '4': 1, '5': 8, '10': 'isBlocked'},
    {'1': 'can_connect', '3': 6, '4': 1, '5': 8, '10': 'canConnect'},
    {'1': 'reason', '3': 7, '4': 1, '5': 9, '10': 'reason'},
    {'1': 'is_premium', '3': 8, '4': 1, '5': 8, '10': 'isPremium'},
    {
      '1': 'traffic_masking_available',
      '3': 9,
      '4': 1,
      '5': 8,
      '10': 'trafficMaskingAvailable'
    },
  ],
};

/// Descriptor for `AccessStateResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List accessStateResponseDescriptor = $convert.base64Decode(
    'ChNBY2Nlc3NTdGF0ZVJlc3BvbnNlEhcKB3VzZXJfaWQYASABKAlSBnVzZXJJZBIhCgxjdXJyZW'
    '50X3BsYW4YAiABKAlSC2N1cnJlbnRQbGFuEh8KC3ZhbGlkX3VudGlsGAMgASgJUgp2YWxpZFVu'
    'dGlsEi0KEnByZWZlcnJlZF9wbGF0Zm9ybRgEIAEoCVIRcHJlZmVycmVkUGxhdGZvcm0SHQoKaX'
    'NfYmxvY2tlZBgFIAEoCFIJaXNCbG9ja2VkEh8KC2Nhbl9jb25uZWN0GAYgASgIUgpjYW5Db25u'
    'ZWN0EhYKBnJlYXNvbhgHIAEoCVIGcmVhc29uEh0KCmlzX3ByZW1pdW0YCCABKAhSCWlzUHJlbW'
    'l1bRI6Chl0cmFmZmljX21hc2tpbmdfYXZhaWxhYmxlGAkgASgIUhd0cmFmZmljTWFza2luZ0F2'
    'YWlsYWJsZQ==');

@$core.Deprecated('Use getConnectionBundleRequestDescriptor instead')
const GetConnectionBundleRequest$json = {
  '1': 'GetConnectionBundleRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'platform', '3': 2, '4': 1, '5': 9, '10': 'platform'},
    {'1': 'force_refresh', '3': 3, '4': 1, '5': 8, '10': 'forceRefresh'},
  ],
};

/// Descriptor for `GetConnectionBundleRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getConnectionBundleRequestDescriptor =
    $convert.base64Decode(
        'ChpHZXRDb25uZWN0aW9uQnVuZGxlUmVxdWVzdBIXCgd1c2VyX2lkGAEgASgJUgZ1c2VySWQSGg'
        'oIcGxhdGZvcm0YAiABKAlSCHBsYXRmb3JtEiMKDWZvcmNlX3JlZnJlc2gYAyABKAhSDGZvcmNl'
        'UmVmcmVzaA==');

@$core.Deprecated('Use connectionOptionDescriptor instead')
const ConnectionOption$json = {
  '1': 'ConnectionOption',
  '2': [
    {'1': 'uri', '3': 1, '4': 1, '5': 9, '10': 'uri'},
    {'1': 'protocol', '3': 2, '4': 1, '5': 9, '10': 'protocol'},
    {'1': 'label', '3': 3, '4': 1, '5': 9, '10': 'label'},
    {
      '1': 'recommended_networks',
      '3': 4,
      '4': 3,
      '5': 9,
      '10': 'recommendedNetworks'
    },
    {'1': 'priority', '3': 5, '4': 1, '5': 5, '10': 'priority'},
  ],
};

/// Descriptor for `ConnectionOption`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List connectionOptionDescriptor = $convert.base64Decode(
    'ChBDb25uZWN0aW9uT3B0aW9uEhAKA3VyaRgBIAEoCVIDdXJpEhoKCHByb3RvY29sGAIgASgJUg'
    'hwcm90b2NvbBIUCgVsYWJlbBgDIAEoCVIFbGFiZWwSMQoUcmVjb21tZW5kZWRfbmV0d29ya3MY'
    'BCADKAlSE3JlY29tbWVuZGVkTmV0d29ya3MSGgoIcHJpb3JpdHkYBSABKAVSCHByaW9yaXR5');

@$core.Deprecated('Use connectionBundleResponseDescriptor instead')
const ConnectionBundleResponse$json = {
  '1': 'ConnectionBundleResponse',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {
      '1': 'connection_options',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.heyearth.v1.ConnectionOption',
      '10': 'connectionOptions'
    },
    {'1': 'status_message', '3': 3, '4': 1, '5': 9, '10': 'statusMessage'},
    {
      '1': 'traffic_masking_enabled',
      '3': 4,
      '4': 1,
      '5': 8,
      '10': 'trafficMaskingEnabled'
    },
  ],
};

/// Descriptor for `ConnectionBundleResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List connectionBundleResponseDescriptor = $convert.base64Decode(
    'ChhDb25uZWN0aW9uQnVuZGxlUmVzcG9uc2USFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklkEkwKEm'
    'Nvbm5lY3Rpb25fb3B0aW9ucxgCIAMoCzIdLmhleWVhcnRoLnYxLkNvbm5lY3Rpb25PcHRpb25S'
    'EWNvbm5lY3Rpb25PcHRpb25zEiUKDnN0YXR1c19tZXNzYWdlGAMgASgJUg1zdGF0dXNNZXNzYW'
    'dlEjYKF3RyYWZmaWNfbWFza2luZ19lbmFibGVkGAQgASgIUhV0cmFmZmljTWFza2luZ0VuYWJs'
    'ZWQ=');

@$core.Deprecated('Use listSubscriptionsRequestDescriptor instead')
const ListSubscriptionsRequest$json = {
  '1': 'ListSubscriptionsRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
  ],
};

/// Descriptor for `ListSubscriptionsRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listSubscriptionsRequestDescriptor =
    $convert.base64Decode(
        'ChhMaXN0U3Vic2NyaXB0aW9uc1JlcXVlc3QSFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklk');

@$core.Deprecated('Use subscriptionItemDescriptor instead')
const SubscriptionItem$json = {
  '1': 'SubscriptionItem',
  '2': [
    {'1': 'group_id', '3': 1, '4': 1, '5': 5, '10': 'groupId'},
    {'1': 'group_name', '3': 2, '4': 1, '5': 9, '10': 'groupName'},
    {'1': 'valid_until', '3': 3, '4': 1, '5': 9, '10': 'validUntil'},
    {'1': 'auto_renew', '3': 4, '4': 1, '5': 8, '10': 'autoRenew'},
    {'1': 'active', '3': 5, '4': 1, '5': 8, '10': 'active'},
    {'1': 'plan', '3': 6, '4': 1, '5': 9, '10': 'plan'},
    {'1': 'price_rub', '3': 7, '4': 1, '5': 5, '10': 'priceRub'},
  ],
};

/// Descriptor for `SubscriptionItem`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List subscriptionItemDescriptor = $convert.base64Decode(
    'ChBTdWJzY3JpcHRpb25JdGVtEhkKCGdyb3VwX2lkGAEgASgFUgdncm91cElkEh0KCmdyb3VwX2'
    '5hbWUYAiABKAlSCWdyb3VwTmFtZRIfCgt2YWxpZF91bnRpbBgDIAEoCVIKdmFsaWRVbnRpbBId'
    'CgphdXRvX3JlbmV3GAQgASgIUglhdXRvUmVuZXcSFgoGYWN0aXZlGAUgASgIUgZhY3RpdmUSEg'
    'oEcGxhbhgGIAEoCVIEcGxhbhIbCglwcmljZV9ydWIYByABKAVSCHByaWNlUnVi');

@$core.Deprecated('Use listSubscriptionsResponseDescriptor instead')
const ListSubscriptionsResponse$json = {
  '1': 'ListSubscriptionsResponse',
  '2': [
    {
      '1': 'items',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.heyearth.v1.SubscriptionItem',
      '10': 'items'
    },
  ],
};

/// Descriptor for `ListSubscriptionsResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listSubscriptionsResponseDescriptor =
    $convert.base64Decode(
        'ChlMaXN0U3Vic2NyaXB0aW9uc1Jlc3BvbnNlEjMKBWl0ZW1zGAEgAygLMh0uaGV5ZWFydGgudj'
        'EuU3Vic2NyaXB0aW9uSXRlbVIFaXRlbXM=');

@$core.Deprecated('Use listPaymentsRequestDescriptor instead')
const ListPaymentsRequest$json = {
  '1': 'ListPaymentsRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
  ],
};

/// Descriptor for `ListPaymentsRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listPaymentsRequestDescriptor =
    $convert.base64Decode(
        'ChNMaXN0UGF5bWVudHNSZXF1ZXN0EhcKB3VzZXJfaWQYASABKAlSBnVzZXJJZA==');

@$core.Deprecated('Use paymentItemDescriptor instead')
const PaymentItem$json = {
  '1': 'PaymentItem',
  '2': [
    {'1': 'id', '3': 1, '4': 1, '5': 5, '10': 'id'},
    {'1': 'amount', '3': 2, '4': 1, '5': 1, '10': 'amount'},
    {'1': 'currency', '3': 3, '4': 1, '5': 9, '10': 'currency'},
    {'1': 'status', '3': 4, '4': 1, '5': 9, '10': 'status'},
    {'1': 'provider', '3': 5, '4': 1, '5': 9, '10': 'provider'},
    {'1': 'external_id', '3': 6, '4': 1, '5': 9, '10': 'externalId'},
    {'1': 'notes', '3': 7, '4': 1, '5': 9, '10': 'notes'},
    {'1': 'created_at', '3': 8, '4': 1, '5': 9, '10': 'createdAt'},
    {'1': 'confirmed_at', '3': 9, '4': 1, '5': 9, '10': 'confirmedAt'},
  ],
};

/// Descriptor for `PaymentItem`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List paymentItemDescriptor = $convert.base64Decode(
    'CgtQYXltZW50SXRlbRIOCgJpZBgBIAEoBVICaWQSFgoGYW1vdW50GAIgASgBUgZhbW91bnQSGg'
    'oIY3VycmVuY3kYAyABKAlSCGN1cnJlbmN5EhYKBnN0YXR1cxgEIAEoCVIGc3RhdHVzEhoKCHBy'
    'b3ZpZGVyGAUgASgJUghwcm92aWRlchIfCgtleHRlcm5hbF9pZBgGIAEoCVIKZXh0ZXJuYWxJZB'
    'IUCgVub3RlcxgHIAEoCVIFbm90ZXMSHQoKY3JlYXRlZF9hdBgIIAEoCVIJY3JlYXRlZEF0EiEK'
    'DGNvbmZpcm1lZF9hdBgJIAEoCVILY29uZmlybWVkQXQ=');

@$core.Deprecated('Use listPaymentsResponseDescriptor instead')
const ListPaymentsResponse$json = {
  '1': 'ListPaymentsResponse',
  '2': [
    {
      '1': 'items',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.heyearth.v1.PaymentItem',
      '10': 'items'
    },
  ],
};

/// Descriptor for `ListPaymentsResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listPaymentsResponseDescriptor = $convert.base64Decode(
    'ChRMaXN0UGF5bWVudHNSZXNwb25zZRIuCgVpdGVtcxgBIAMoCzIYLmhleWVhcnRoLnYxLlBheW'
    '1lbnRJdGVtUgVpdGVtcw==');
