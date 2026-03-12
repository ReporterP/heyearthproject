import 'dart:convert';
import 'dart:io' show File, Platform, Process;

import 'package:flutter/foundation.dart';
import 'package:flutter_v2ray_plus/flutter_v2ray.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';
import 'package:proxy_manager/proxy_manager.dart';
import 'package:heyearth_vpn/vpn_service_interface.dart';

/// Порт SOCKS по умолчанию в конфиге Xray (из flutter_v2ray_plus url.dart).
const int _defaultSocksPort = 10807;

/// Домены для режима «только для выбранных» (как на Android).
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

/// Реализация VPN на Windows: системный прокси + локальный Xray (тот же конфиг, что и на Android).
class VpnServiceWindows implements VpnService {
  Process? _process;
  File? _configFile;
  final ProxyManager _proxyManager = ProxyManager();

  @override
  bool get isSupported => !kIsWeb && Platform.isWindows;

  static Future<String?> _findXrayPath() async {
    final exePath = Platform.resolvedExecutable;
    final exeDir = path.dirname(exePath);
    final candidates = [
      path.join(exeDir, 'xray.exe'),
      path.join(exeDir, 'xray', 'xray.exe'),
    ];
    for (final p in candidates) {
      if (File(p).existsSync()) return p;
    }
    try {
      final appSupport = await getApplicationSupportDirectory();
      final supportPath = path.join(appSupport.path, 'xray', 'xray.exe');
      if (File(supportPath).existsSync()) return supportPath;
    } catch (_) {}
    final envPath = Platform.environment['XRAY_PATH'];
    if (envPath != null && envPath.isNotEmpty) {
      final p = path.normalize(envPath);
      if (File(p).existsSync()) return p;
    }
    return null;
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

  int _socksPortFromConfig(String configJson) {
    try {
      final map = jsonDecode(configJson) as Map<String, dynamic>;
      final inbounds = map['inbounds'];
      if (inbounds is List && inbounds.isNotEmpty) {
        final first = inbounds.first;
        if (first is Map<String, dynamic>) {
          final port = first['port'];
          if (port is int) return port;
        }
      }
    } catch (_) {}
    return _defaultSocksPort;
  }

  @override
  Future<void> connect(String ssUri,
      {bool proxyOnly = false, bool selectedOnly = false}) async {
    if (!isSupported) return;
    if (ssUri.trim().isEmpty) return;

    final xrayPath = await _findXrayPath();
    if (xrayPath == null) {
      throw Exception(
        'Xray не найден. Скачайте Xray-core с https://github.com/XTLS/Xray-core/releases '
        '(файл Xray-windows-64.zip) и поместите xray.exe в папку с приложением '
        'или в %APPDATA%\\heyearth_vpn\\xray\\',
      );
    }

    final parsed = FlutterV2ray.parseFromURL(ssUri.trim());
    String config = parsed.getFullConfiguration();
    if (selectedOnly) {
      config = _configWithSelectedOnlyRouting(config);
    }
    final socksPort = _socksPortFromConfig(config);

    final tempDir = await getTemporaryDirectory();
    _configFile = File(path.join(tempDir.path,
        'heyearth_xray_${DateTime.now().millisecondsSinceEpoch}.json'));
    await _configFile!.writeAsString(config, flush: true);

    try {
      _process = await Process.start(
        xrayPath,
        ['run', '-config', _configFile!.path],
        runInShell: false,
      );
    } catch (e) {
      try {
        await _configFile?.delete();
      } catch (_) {}
      _configFile = null;
      rethrow;
    }

    try {
      await _proxyManager.setAsSystemProxy(
          ProxyTypes.socks, '127.0.0.1', socksPort);
    } catch (e) {
      _process?.kill();
      _process = null;
      try {
        await _configFile?.delete();
      } catch (_) {}
      _configFile = null;
      rethrow;
    }
  }

  @override
  Future<void> disconnect() async {
    if (!isSupported) return;
    try {
      await _proxyManager.cleanSystemProxy();
    } catch (_) {}
    try {
      _process?.kill();
    } catch (_) {}
    _process = null;
    try {
      if (_configFile != null && _configFile!.existsSync()) {
        await _configFile!.delete();
      }
    } catch (_) {}
    _configFile = null;
  }

  @override
  Future<bool> isTrafficFlowing() async => true;
}
