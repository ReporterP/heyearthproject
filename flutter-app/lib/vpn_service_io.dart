import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';
import 'package:flutter_v2ray_plus/flutter_v2ray.dart';
import 'package:heyearth_vpn/vpn_service_interface.dart';

/// Реализация VPN на Android/iOS через flutter_v2ray_plus.
class VpnServiceIo implements VpnService {
  FlutterV2ray? _v2ray;
  bool _initialized = false;

  static const String _providerBundleId =
      'ru.hiearth.app.VPNProvider';
  static const String _groupId = 'group.ru.hiearth.app';

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
    if (!await _ensureInitialized()) {
      return;
    }
    final parsed = FlutterV2ray.parseFromURL(uri.trim());
    final config = parsed.getFullConfiguration();
    final allowed = await _v2ray!.requestPermission();
    if (!allowed) return;
    await _v2ray!.startVless(
      remark: parsed.remark.isNotEmpty ? parsed.remark : 'HeYearth',
      config: config,
      proxyOnly: proxyOnly,
    );
  }

  @override
  Future<void> disconnect() async {
    if (!isSupported || _v2ray == null) return;
    try {
      await _v2ray!.stopVless();
    } catch (_) {}
  }

  @override
  Future<bool> isTrafficFlowing() async {
    if (!_initialized || _v2ray == null) return false;
    try {
      // Check V2Ray core status — returns connection state
      final delay = await _v2ray!.getServerDelay(url: 'https://www.google.com/generate_204');
      return delay > 0 && delay < 10000; // responsive within 10s
    } catch (_) {
      return false;
    }
  }
}
