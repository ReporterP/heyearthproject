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
  ],
};

/// Descriptor for `CheckAppUpdateResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List checkAppUpdateResponseDescriptor = $convert.base64Decode(
    'ChZDaGVja0FwcFVwZGF0ZVJlc3BvbnNlEhgKB3ZlcnNpb24YASABKAlSB3ZlcnNpb24SHwoLYW'
    '5kcm9pZF91cmwYAiABKAlSCmFuZHJvaWRVcmwSHwoLd2luZG93c191cmwYAyABKAlSCndpbmRv'
    'd3NVcmwSGwoJbWFjb3NfdXJsGAQgASgJUghtYWNvc1VybBIbCglsaW51eF91cmwYBSABKAlSCG'
    'xpbnV4VXJsEhcKB2lvc191cmwYBiABKAlSBmlvc1VybBIjCg1yZWxlYXNlX25vdGVzGAcgASgJ'
    'UgxyZWxlYXNlTm90ZXM=');

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
