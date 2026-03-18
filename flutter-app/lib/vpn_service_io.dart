import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter_v2ray_plus/flutter_v2ray.dart';
import 'package:heyearth_vpn/vpn_service_interface.dart';

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

  @override
  Future<void> connect(String uri, {bool proxyOnly = false}) async {
    if (!isSupported) return;
    if (uri.trim().isEmpty) return;
    final v2 = _v2ray ?? FlutterV2ray();
    if (!await _ensureInitialized()) {
      return;
    }
    final parsed = FlutterV2ray.parseFromURL(uri.trim());
    final config = parsed.getFullConfiguration();
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
