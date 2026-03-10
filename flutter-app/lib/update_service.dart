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
  });
  final String latestVersion;
  final String downloadUrl;
  final String? releaseNotes;

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

/// Запрос к API по gRPC: CheckAppUpdate.
/// Возвращает версию с сервера и [UpdateInfo] только если на сервере версия новее текущей.
Future<UpdateCheckResult> checkForUpdate(String apiBase) async {
  try {
    final resp = await checkAppUpdateGrpc(apiBase).timeout(const Duration(seconds: 10));
    if (resp == null) return const UpdateCheckResult();
    final version = resp.version.trim();
    if (version.isEmpty) return const UpdateCheckResult();

    final info = await PackageInfo.fromPlatform();
    final current = info.version;
    final newer = isNewerVersion(current, version);

    String downloadUrl = '';
    if (kIsWeb) {
      // веб — бинарь не скачиваем
    } else if (defaultTargetPlatform == TargetPlatform.android) {
      downloadUrl = resp.androidUrl.trim();
    } else if (defaultTargetPlatform == TargetPlatform.windows ||
        defaultTargetPlatform == TargetPlatform.macOS ||
        defaultTargetPlatform == TargetPlatform.linux) {
      downloadUrl = resp.windowsUrl.trim();
      if (downloadUrl.isEmpty && defaultTargetPlatform == TargetPlatform.macOS) {
        downloadUrl = resp.macosUrl.trim();
      }
      if (downloadUrl.isEmpty && defaultTargetPlatform == TargetPlatform.linux) {
        downloadUrl = resp.linuxUrl.trim();
      }
    } else if (defaultTargetPlatform == TargetPlatform.iOS) {
      downloadUrl = resp.iosUrl.trim();
    }

    UpdateInfo? update;
    if (newer) {
      update = UpdateInfo(
        latestVersion: version,
        downloadUrl: downloadUrl,
        releaseNotes: resp.releaseNotes.trim().isEmpty ? null : resp.releaseNotes.trim(),
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
