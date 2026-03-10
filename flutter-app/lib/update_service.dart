import 'package:flutter/foundation.dart';
import 'package:heyearth_vpn/grpc_client.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:url_launcher/url_launcher.dart';

/// Результат проверки обновлений (без Google Play / Microsoft Store).
class UpdateInfo {
  const UpdateInfo({
    required this.latestVersion,
    required this.downloadUrl,
    this.releaseNotes,
    required this.mandatory,
    required this.minSupportedVersion,
    required this.requiredByPolicy,
  });
  final String latestVersion;
  final String downloadUrl;
  final String? releaseNotes;
  final bool mandatory;
  final String minSupportedVersion;
  final bool requiredByPolicy;

  bool get hasUrl => downloadUrl.isNotEmpty;
}

/// Результат запроса проверки: версия с сервера (если есть) и данные обновления (если версия новее).
class UpdateCheckResult {
  const UpdateCheckResult({this.serverVersion, this.update});
  final String? serverVersion;
  final UpdateInfo? update;
}

/// Сравнение версий вида "1.0.0" или "1.0.0+1" (игнорируем build number).
bool isNewerVersion(String current, String latest) {
  final c = _parseVersion(current);
  final l = _parseVersion(latest);
  for (int i = 0; i < 3; i++) {
    final cn = i < c.length ? c[i] : 0;
    final ln = i < l.length ? l[i] : 0;
    if (ln > cn) return true;
    if (ln < cn) return false;
  }
  return false;
}

List<int> _parseVersion(String s) {
  final main = s.split('+').first.trim();
  return main.split('.').map((e) => int.tryParse(e) ?? 0).toList();
}

String _currentPlatformForUpdates() {
  if (kIsWeb) {
    return 'web';
  }
  switch (defaultTargetPlatform) {
    case TargetPlatform.android:
      return 'android';
    case TargetPlatform.windows:
      return 'windows';
    case TargetPlatform.macOS:
      return 'macos';
    case TargetPlatform.linux:
      return 'linux';
    case TargetPlatform.iOS:
      return 'ios';
    case TargetPlatform.fuchsia:
      return 'fuchsia';
  }
}

/// Запрос к API по gRPC: CheckAppUpdate.
/// Возвращает версию с сервера и [UpdateInfo], если доступна новая версия
/// или текущая версия ниже минимально поддерживаемой.
Future<UpdateCheckResult> checkForUpdate(String apiBase) async {
  try {
    final platform = _currentPlatformForUpdates();
    if (platform == 'web' || platform == 'fuchsia') {
      return const UpdateCheckResult();
    }
    final resp = await checkAppUpdateGrpc(apiBase, platform)
        .timeout(const Duration(seconds: 10));
    if (resp == null) return const UpdateCheckResult();
    final version = resp.version.trim();
    if (version.isEmpty) return const UpdateCheckResult();

    final info = await PackageInfo.fromPlatform();
    final current = info.version;
    final newer = isNewerVersion(current, version);
    final minSupportedVersion = resp.minSupportedVersion.trim();
    final belowMinSupported = minSupportedVersion.isNotEmpty &&
        isNewerVersion(current, minSupportedVersion);
    final requiredByPolicy = resp.mandatory || belowMinSupported;

    String downloadUrl = '';
    if (kIsWeb) {
      // веб — бинарь не скачиваем
    } else if (defaultTargetPlatform == TargetPlatform.android) {
      downloadUrl = resp.androidUrl.trim();
    } else if (defaultTargetPlatform == TargetPlatform.windows ||
        defaultTargetPlatform == TargetPlatform.macOS ||
        defaultTargetPlatform == TargetPlatform.linux) {
      downloadUrl = resp.windowsUrl.trim();
      if (downloadUrl.isEmpty &&
          defaultTargetPlatform == TargetPlatform.macOS) {
        downloadUrl = resp.macosUrl.trim();
      }
      if (downloadUrl.isEmpty &&
          defaultTargetPlatform == TargetPlatform.linux) {
        downloadUrl = resp.linuxUrl.trim();
      }
    } else if (defaultTargetPlatform == TargetPlatform.iOS) {
      downloadUrl = resp.iosUrl.trim();
    }

    UpdateInfo? update;
    if (newer || requiredByPolicy) {
      update = UpdateInfo(
        latestVersion: version,
        downloadUrl: downloadUrl,
        releaseNotes:
            resp.releaseNotes.trim().isEmpty ? null : resp.releaseNotes.trim(),
        mandatory: resp.mandatory,
        minSupportedVersion: minSupportedVersion,
        requiredByPolicy: requiredByPolicy,
      );
    }
    return UpdateCheckResult(serverVersion: version, update: update);
  } catch (_) {
    return const UpdateCheckResult();
  }
}

/// Открыть ссылку на скачивание в браузере (или системном обработчике).
Future<bool> openDownloadUrl(String url) async {
  final uri = Uri.tryParse(url);
  if (uri == null || !uri.hasScheme) return false;
  return launchUrl(uri, mode: LaunchMode.externalApplication);
}
