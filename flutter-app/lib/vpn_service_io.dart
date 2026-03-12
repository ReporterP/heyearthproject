import 'dart:convert';
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter_v2ray_plus/flutter_v2ray.dart';
import 'package:heyearth_vpn/vpn_service_interface.dart';

/// Домены для режима «VPN только для выбранных» (через туннель; остальное — direct).
const List<String> _selectedOnlyDomains = [
  'youtube.com',
  'googlevideo.com',
  'ytimg.com',
  'google.com',
  'instagram.com',
  'cdninstagram.com',
  'fbcdn.net',
  'linkedin.com',
  'spotify.com',
  'openai.com',
  'telegram.org',
  't.me',
  'telegram.me',
];

/// Реализация VPN на Android/iOS через flutter_v2ray_plus.
class VpnServiceIo implements VpnService {
  FlutterV2ray? _v2ray;
  bool _initialized = false;

  static const String _providerBundleId =
      'com.example.heyearth_vpn.VPNProvider';
  static const String _groupId = 'group.com.example.heyearth_vpn';

  @override
  bool get isSupported => !kIsWeb && (Platform.isAndroid || Platform.isIOS);

  Future<bool> _ensureInitialized() async {
    if (!isSupported) return false;
    if (_initialized && _v2ray != null) return true;
    try {
      _v2ray ??= FlutterV2ray();
      await _v2ray!.initializeVless(
        providerBundleIdentifier: _providerBundleId,
        groupIdentifier: _groupId,
      );
      _initialized = true;
      return true;
    } catch (_) {
      return false;
    }
  }

  void _enableSniffingOnInbounds(Map<String, dynamic> map) {
    final inbounds = map['inbounds'];
    if (inbounds is! List) return;
    for (final e in inbounds) {
      if (e is Map<String, dynamic>) {
        e['sniffing'] = {
          'enabled': true,
          'destOverride': ['http', 'tls'],
          'metadataOnly': false,
        };
      }
    }
  }

  String _configWithSelectedOnlyRouting(String configJson) {
    final map = jsonDecode(configJson) as Map<String, dynamic>;
    _enableSniffingOnInbounds(map);
    final routing = map['routing'] as Map<String, dynamic>? ?? {};
    final domainList = _selectedOnlyDomains.map((d) => 'domain:$d').toList();
    routing['domainStrategy'] = 'IPIfNonMatch';
    routing['rules'] = [
      {'type': 'field', 'domain': domainList, 'outboundTag': 'proxy'},
      {'type': 'field', 'network': 'tcp,udp', 'outboundTag': 'direct'},
    ];
    map['routing'] = routing;
    return const JsonEncoder().convert(map);
  }

  @override
  Future<void> connect(String ssUri,
      {bool proxyOnly = false, bool selectedOnly = false}) async {
    if (!isSupported) return;
    if (ssUri.trim().isEmpty) return;
    final v2 = _v2ray ?? FlutterV2ray();
    if (!await _ensureInitialized()) {
      return;
    }
    final parsed = FlutterV2ray.parseFromURL(ssUri.trim());
    String config = parsed.getFullConfiguration();
    if (selectedOnly) {
      config = _configWithSelectedOnlyRouting(config);
    }
    final allowed = await v2.requestPermission();
    if (!allowed) return;
    await v2.startVless(
      remark: parsed.remark.isNotEmpty ? parsed.remark : 'HeYearth',
      config: config,
      proxyOnly: false,
    );
  }

  @override
  Future<void> disconnect() async {
    if (!isSupported) return;
    try {
      await (_v2ray ?? FlutterV2ray()).stopVless();
    } catch (_) {}
  }

  @override
  Future<bool> isTrafficFlowing() async => true;
}
