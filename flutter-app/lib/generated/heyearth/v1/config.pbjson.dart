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

@$core.Deprecated('Use getConfigRequestDescriptor instead')
const GetConfigRequest$json = {
  '1': 'GetConfigRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
  ],
};

/// Descriptor for `GetConfigRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getConfigRequestDescriptor = $convert.base64Decode(
    'ChBHZXRDb25maWdSZXF1ZXN0EhcKB3VzZXJfaWQYASABKAlSBnVzZXJJZA==');

@$core.Deprecated('Use getConfigResponseDescriptor instead')
const GetConfigResponse$json = {
  '1': 'GetConfigResponse',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'ss_uri', '3': 2, '4': 1, '5': 9, '10': 'ssUri'},
  ],
};

/// Descriptor for `GetConfigResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getConfigResponseDescriptor = $convert.base64Decode(
    'ChFHZXRDb25maWdSZXNwb25zZRIXCgd1c2VyX2lkGAEgASgJUgZ1c2VySWQSFQoGc3NfdXJpGA'
    'IgASgJUgVzc1VyaQ==');

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
  ],
};

/// Descriptor for `CheckAppUpdateResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List checkAppUpdateResponseDescriptor = $convert.base64Decode(
    'ChZDaGVja0FwcFVwZGF0ZVJlc3BvbnNlEhgKB3ZlcnNpb24YASABKAlSB3ZlcnNpb24SHwoLYW'
    '5kcm9pZF91cmwYAiABKAlSCmFuZHJvaWRVcmwSHwoLd2luZG93c191cmwYAyABKAlSCndpbmRv'
    'd3NVcmwSGwoJbWFjb3NfdXJsGAQgASgJUghtYWNvc1VybBIbCglsaW51eF91cmwYBSABKAlSCG'
    'xpbnV4VXJsEhcKB2lvc191cmwYBiABKAlSBmlvc1VybBIjCg1yZWxlYXNlX25vdGVzGAcgASgJ'
    'UgxyZWxlYXNlTm90ZXMSHAoJbWFuZGF0b3J5GAggASgIUgltYW5kYXRvcnkSMgoVbWluX3N1cH'
    'BvcnRlZF92ZXJzaW9uGAkgASgJUhNtaW5TdXBwb3J0ZWRWZXJzaW9u');

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

@$core.Deprecated('Use getProfileRequestDescriptor instead')
const GetProfileRequest$json = {
  '1': 'GetProfileRequest',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
  ],
};

/// Descriptor for `GetProfileRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getProfileRequestDescriptor = $convert.base64Decode(
    'ChFHZXRQcm9maWxlUmVxdWVzdBIXCgd1c2VyX2lkGAEgASgJUgZ1c2VySWQ=');

@$core.Deprecated('Use getProfileResponseDescriptor instead')
const GetProfileResponse$json = {
  '1': 'GetProfileResponse',
  '2': [
    {'1': 'current_plan', '3': 1, '4': 1, '5': 9, '10': 'currentPlan'},
    {'1': 'valid_until', '3': 2, '4': 1, '5': 9, '10': 'validUntil'},
  ],
};

/// Descriptor for `GetProfileResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getProfileResponseDescriptor = $convert.base64Decode(
    'ChJHZXRQcm9maWxlUmVzcG9uc2USIQoMY3VycmVudF9wbGFuGAEgASgJUgtjdXJyZW50UGxhbh'
    'IfCgt2YWxpZF91bnRpbBgCIAEoCVIKdmFsaWRVbnRpbA==');

@$core.Deprecated('Use ensureTelegramUserRequestDescriptor instead')
const EnsureTelegramUserRequest$json = {
  '1': 'EnsureTelegramUserRequest',
  '2': [
    {'1': 'telegram_id', '3': 1, '4': 1, '5': 3, '10': 'telegramId'},
    {
      '1': 'preferred_platform',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'preferredPlatform'
    },
    {'1': 'locale', '3': 3, '4': 1, '5': 9, '10': 'locale'},
  ],
};

/// Descriptor for `EnsureTelegramUserRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List ensureTelegramUserRequestDescriptor = $convert.base64Decode(
    'ChlFbnN1cmVUZWxlZ3JhbVVzZXJSZXF1ZXN0Eh8KC3RlbGVncmFtX2lkGAEgASgDUgp0ZWxlZ3'
    'JhbUlkEi0KEnByZWZlcnJlZF9wbGF0Zm9ybRgCIAEoCVIRcHJlZmVycmVkUGxhdGZvcm0SFgoG'
    'bG9jYWxlGAMgASgJUgZsb2NhbGU=');

@$core.Deprecated('Use setPreferredPlatformRequestDescriptor instead')
const SetPreferredPlatformRequest$json = {
  '1': 'SetPreferredPlatformRequest',
  '2': [
    {'1': 'telegram_id', '3': 1, '4': 1, '5': 3, '10': 'telegramId'},
    {
      '1': 'preferred_platform',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'preferredPlatform'
    },
  ],
};

/// Descriptor for `SetPreferredPlatformRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List setPreferredPlatformRequestDescriptor =
    $convert.base64Decode(
        'ChtTZXRQcmVmZXJyZWRQbGF0Zm9ybVJlcXVlc3QSHwoLdGVsZWdyYW1faWQYASABKANSCnRlbG'
        'VncmFtSWQSLQoScHJlZmVycmVkX3BsYXRmb3JtGAIgASgJUhFwcmVmZXJyZWRQbGF0Zm9ybQ==');

@$core.Deprecated('Use createPairingCodeRequestDescriptor instead')
const CreatePairingCodeRequest$json = {
  '1': 'CreatePairingCodeRequest',
  '2': [
    {'1': 'telegram_id', '3': 1, '4': 1, '5': 3, '10': 'telegramId'},
    {
      '1': 'preferred_platform',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'preferredPlatform'
    },
    {'1': 'locale', '3': 3, '4': 1, '5': 9, '10': 'locale'},
  ],
};

/// Descriptor for `CreatePairingCodeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List createPairingCodeRequestDescriptor = $convert.base64Decode(
    'ChhDcmVhdGVQYWlyaW5nQ29kZVJlcXVlc3QSHwoLdGVsZWdyYW1faWQYASABKANSCnRlbGVncm'
    'FtSWQSLQoScHJlZmVycmVkX3BsYXRmb3JtGAIgASgJUhFwcmVmZXJyZWRQbGF0Zm9ybRIWCgZs'
    'b2NhbGUYAyABKAlSBmxvY2FsZQ==');

@$core.Deprecated('Use createPairingCodeResponseDescriptor instead')
const CreatePairingCodeResponse$json = {
  '1': 'CreatePairingCodeResponse',
  '2': [
    {
      '1': 'profile',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.UserProfileResponse',
      '10': 'profile'
    },
    {'1': 'code', '3': 2, '4': 1, '5': 9, '10': 'code'},
    {'1': 'claim_token', '3': 3, '4': 1, '5': 9, '10': 'claimToken'},
    {'1': 'expires_at', '3': 4, '4': 1, '5': 9, '10': 'expiresAt'},
    {'1': 'deeplink_url', '3': 5, '4': 1, '5': 9, '10': 'deeplinkUrl'},
  ],
};

/// Descriptor for `CreatePairingCodeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List createPairingCodeResponseDescriptor = $convert.base64Decode(
    'ChlDcmVhdGVQYWlyaW5nQ29kZVJlc3BvbnNlEjoKB3Byb2ZpbGUYASABKAsyIC5oZXllYXJ0aC'
    '52MS5Vc2VyUHJvZmlsZVJlc3BvbnNlUgdwcm9maWxlEhIKBGNvZGUYAiABKAlSBGNvZGUSHwoL'
    'Y2xhaW1fdG9rZW4YAyABKAlSCmNsYWltVG9rZW4SHQoKZXhwaXJlc19hdBgEIAEoCVIJZXhwaX'
    'Jlc0F0EiEKDGRlZXBsaW5rX3VybBgFIAEoCVILZGVlcGxpbmtVcmw=');

@$core.Deprecated('Use getClientDistributionRequestDescriptor instead')
const GetClientDistributionRequest$json = {
  '1': 'GetClientDistributionRequest',
  '2': [
    {'1': 'platform', '3': 1, '4': 1, '5': 9, '10': 'platform'},
  ],
};

/// Descriptor for `GetClientDistributionRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getClientDistributionRequestDescriptor =
    $convert.base64Decode(
        'ChxHZXRDbGllbnREaXN0cmlidXRpb25SZXF1ZXN0EhoKCHBsYXRmb3JtGAEgASgJUghwbGF0Zm'
        '9ybQ==');

@$core.Deprecated('Use getClientDistributionResponseDescriptor instead')
const GetClientDistributionResponse$json = {
  '1': 'GetClientDistributionResponse',
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
  ],
};

/// Descriptor for `GetClientDistributionResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getClientDistributionResponseDescriptor = $convert.base64Decode(
    'Ch1HZXRDbGllbnREaXN0cmlidXRpb25SZXNwb25zZRIYCgd2ZXJzaW9uGAEgASgJUgd2ZXJzaW'
    '9uEh8KC2FuZHJvaWRfdXJsGAIgASgJUgphbmRyb2lkVXJsEh8KC3dpbmRvd3NfdXJsGAMgASgJ'
    'Ugp3aW5kb3dzVXJsEhsKCW1hY29zX3VybBgEIAEoCVIIbWFjb3NVcmwSGwoJbGludXhfdXJsGA'
    'UgASgJUghsaW51eFVybBIXCgdpb3NfdXJsGAYgASgJUgZpb3NVcmwSIwoNcmVsZWFzZV9ub3Rl'
    'cxgHIAEoCVIMcmVsZWFzZU5vdGVzEhwKCW1hbmRhdG9yeRgIIAEoCFIJbWFuZGF0b3J5EjIKFW'
    '1pbl9zdXBwb3J0ZWRfdmVyc2lvbhgJIAEoCVITbWluU3VwcG9ydGVkVmVyc2lvbg==');

@$core.Deprecated('Use issueLegacyAppleConfigRequestDescriptor instead')
const IssueLegacyAppleConfigRequest$json = {
  '1': 'IssueLegacyAppleConfigRequest',
  '2': [
    {'1': 'telegram_id', '3': 1, '4': 1, '5': 3, '10': 'telegramId'},
    {'1': 'force_new', '3': 2, '4': 1, '5': 8, '10': 'forceNew'},
  ],
};

/// Descriptor for `IssueLegacyAppleConfigRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List issueLegacyAppleConfigRequestDescriptor =
    $convert.base64Decode(
        'Ch1Jc3N1ZUxlZ2FjeUFwcGxlQ29uZmlnUmVxdWVzdBIfCgt0ZWxlZ3JhbV9pZBgBIAEoA1IKdG'
        'VsZWdyYW1JZBIbCglmb3JjZV9uZXcYAiABKAhSCGZvcmNlTmV3');

@$core.Deprecated('Use claimByCodeRequestDescriptor instead')
const ClaimByCodeRequest$json = {
  '1': 'ClaimByCodeRequest',
  '2': [
    {'1': 'code', '3': 1, '4': 1, '5': 9, '10': 'code'},
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

/// Descriptor for `ClaimByCodeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List claimByCodeRequestDescriptor = $convert.base64Decode(
    'ChJDbGFpbUJ5Q29kZVJlcXVlc3QSEgoEY29kZRgBIAEoCVIEY29kZRIyCgZkZXZpY2UYAiABKA'
    'syGi5oZXllYXJ0aC52MS5EZXZpY2VDb250ZXh0UgZkZXZpY2U=');

@$core.Deprecated('Use claimByTokenRequestDescriptor instead')
const ClaimByTokenRequest$json = {
  '1': 'ClaimByTokenRequest',
  '2': [
    {'1': 'claim_token', '3': 1, '4': 1, '5': 9, '10': 'claimToken'},
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

/// Descriptor for `ClaimByTokenRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List claimByTokenRequestDescriptor = $convert.base64Decode(
    'ChNDbGFpbUJ5VG9rZW5SZXF1ZXN0Eh8KC2NsYWltX3Rva2VuGAEgASgJUgpjbGFpbVRva2VuEj'
    'IKBmRldmljZRgCIAEoCzIaLmhleWVhcnRoLnYxLkRldmljZUNvbnRleHRSBmRldmljZQ==');

@$core.Deprecated('Use claimResponseDescriptor instead')
const ClaimResponse$json = {
  '1': 'ClaimResponse',
  '2': [
    {
      '1': 'profile',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.UserProfileResponse',
      '10': 'profile'
    },
    {
      '1': 'access_state',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.heyearth.v1.AccessStateResponse',
      '10': 'accessState'
    },
  ],
};

/// Descriptor for `ClaimResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List claimResponseDescriptor = $convert.base64Decode(
    'Cg1DbGFpbVJlc3BvbnNlEjoKB3Byb2ZpbGUYASABKAsyIC5oZXllYXJ0aC52MS5Vc2VyUHJvZm'
    'lsZVJlc3BvbnNlUgdwcm9maWxlEkMKDGFjY2Vzc19zdGF0ZRgCIAEoCzIgLmhleWVhcnRoLnYx'
    'LkFjY2Vzc1N0YXRlUmVzcG9uc2VSC2FjY2Vzc1N0YXRl');

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
    {'1': 'telegram_id', '3': 2, '4': 1, '5': 3, '10': 'telegramId'},
    {'1': 'telegram_linked', '3': 3, '4': 1, '5': 8, '10': 'telegramLinked'},
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
    {
      '1': 'legacy_happ_fallback',
      '3': 9,
      '4': 1,
      '5': 8,
      '10': 'legacyHappFallback'
    },
    {'1': 'access_status', '3': 10, '4': 1, '5': 9, '10': 'accessStatus'},
    {'1': 'status_reason', '3': 11, '4': 1, '5': 9, '10': 'statusReason'},
  ],
};

/// Descriptor for `UserProfileResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List userProfileResponseDescriptor = $convert.base64Decode(
    'ChNVc2VyUHJvZmlsZVJlc3BvbnNlEhcKB3VzZXJfaWQYASABKAlSBnVzZXJJZBIfCgt0ZWxlZ3'
    'JhbV9pZBgCIAEoA1IKdGVsZWdyYW1JZBInCg90ZWxlZ3JhbV9saW5rZWQYAyABKAhSDnRlbGVn'
    'cmFtTGlua2VkEi0KEnByZWZlcnJlZF9wbGF0Zm9ybRgEIAEoCVIRcHJlZmVycmVkUGxhdGZvcm'
    '0SFgoGbG9jYWxlGAUgASgJUgZsb2NhbGUSHQoKaXNfYmxvY2tlZBgGIAEoCFIJaXNCbG9ja2Vk'
    'EiEKDGN1cnJlbnRfcGxhbhgHIAEoCVILY3VycmVudFBsYW4SHwoLdmFsaWRfdW50aWwYCCABKA'
    'lSCnZhbGlkVW50aWwSMAoUbGVnYWN5X2hhcHBfZmFsbGJhY2sYCSABKAhSEmxlZ2FjeUhhcHBG'
    'YWxsYmFjaxIjCg1hY2Nlc3Nfc3RhdHVzGAogASgJUgxhY2Nlc3NTdGF0dXMSIwoNc3RhdHVzX3'
    'JlYXNvbhgLIAEoCVIMc3RhdHVzUmVhc29u');

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
    {
      '1': 'legacy_happ_fallback',
      '3': 7,
      '4': 1,
      '5': 8,
      '10': 'legacyHappFallback'
    },
    {'1': 'reason', '3': 8, '4': 1, '5': 9, '10': 'reason'},
  ],
};

/// Descriptor for `AccessStateResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List accessStateResponseDescriptor = $convert.base64Decode(
    'ChNBY2Nlc3NTdGF0ZVJlc3BvbnNlEhcKB3VzZXJfaWQYASABKAlSBnVzZXJJZBIhCgxjdXJyZW'
    '50X3BsYW4YAiABKAlSC2N1cnJlbnRQbGFuEh8KC3ZhbGlkX3VudGlsGAMgASgJUgp2YWxpZFVu'
    'dGlsEi0KEnByZWZlcnJlZF9wbGF0Zm9ybRgEIAEoCVIRcHJlZmVycmVkUGxhdGZvcm0SHQoKaX'
    'NfYmxvY2tlZBgFIAEoCFIJaXNCbG9ja2VkEh8KC2Nhbl9jb25uZWN0GAYgASgIUgpjYW5Db25u'
    'ZWN0EjAKFGxlZ2FjeV9oYXBwX2ZhbGxiYWNrGAcgASgIUhJsZWdhY3lIYXBwRmFsbGJhY2sSFg'
    'oGcmVhc29uGAggASgJUgZyZWFzb24=');

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

@$core.Deprecated('Use connectionBundleResponseDescriptor instead')
const ConnectionBundleResponse$json = {
  '1': 'ConnectionBundleResponse',
  '2': [
    {'1': 'user_id', '3': 1, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'ss_uri', '3': 2, '4': 1, '5': 9, '10': 'ssUri'},
    {'1': 'connect_mode', '3': 3, '4': 1, '5': 9, '10': 'connectMode'},
    {'1': 'status_message', '3': 4, '4': 1, '5': 9, '10': 'statusMessage'},
    {
      '1': 'legacy_happ_fallback',
      '3': 5,
      '4': 1,
      '5': 8,
      '10': 'legacyHappFallback'
    },
    {
      '1': 'fallback_instruction',
      '3': 6,
      '4': 1,
      '5': 9,
      '10': 'fallbackInstruction'
    },
  ],
};

/// Descriptor for `ConnectionBundleResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List connectionBundleResponseDescriptor = $convert.base64Decode(
    'ChhDb25uZWN0aW9uQnVuZGxlUmVzcG9uc2USFwoHdXNlcl9pZBgBIAEoCVIGdXNlcklkEhUKBn'
    'NzX3VyaRgCIAEoCVIFc3NVcmkSIQoMY29ubmVjdF9tb2RlGAMgASgJUgtjb25uZWN0TW9kZRIl'
    'Cg5zdGF0dXNfbWVzc2FnZRgEIAEoCVINc3RhdHVzTWVzc2FnZRIwChRsZWdhY3lfaGFwcF9mYW'
    'xsYmFjaxgFIAEoCFISbGVnYWN5SGFwcEZhbGxiYWNrEjEKFGZhbGxiYWNrX2luc3RydWN0aW9u'
    'GAYgASgJUhNmYWxsYmFja0luc3RydWN0aW9u');

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
  ],
};

/// Descriptor for `SubscriptionItem`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List subscriptionItemDescriptor = $convert.base64Decode(
    'ChBTdWJzY3JpcHRpb25JdGVtEhkKCGdyb3VwX2lkGAEgASgFUgdncm91cElkEh0KCmdyb3VwX2'
    '5hbWUYAiABKAlSCWdyb3VwTmFtZRIfCgt2YWxpZF91bnRpbBgDIAEoCVIKdmFsaWRVbnRpbBId'
    'CgphdXRvX3JlbmV3GAQgASgIUglhdXRvUmVuZXcSFgoGYWN0aXZlGAUgASgIUgZhY3RpdmU=');

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
