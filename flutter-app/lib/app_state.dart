import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import 'generated/heyearth/v1/config.pb.dart' as pb;
import 'grpc_client.dart';
import 'update_service.dart';
import 'vpn_service_interface.dart';

const _keyUserId = 'heyearth_user_id';
const _keySsUri = 'heyearth_ss_uri';
const _keyVpnOn = 'heyearth_vpn_on';
const _keyApiBase = 'heyearth_api_base';
const _keySelectedOnly = 'heyearth_selected_only';
const _keyThemeMode = 'heyearth_theme_mode';
const _keyLocale = 'heyearth_locale';
const _keyDeviceId = 'heyearth_device_id';
const _defaultApiBase = 'https://quizgame.pro';

class AppState extends ChangeNotifier {
  AppState({required VpnService vpn}) : _vpn = vpn;

  final VpnService _vpn;

  String? _userId;
  String? _ssUri;
  String _deviceId = '';
  bool _vpnOn = false;
  bool _vpnConnecting = false;
  bool _selectedOnlyMode = false;
  bool _loading = true;
  bool _accountLoading = false;
  String? _error;
  String _apiBase = _defaultApiBase;
  ThemeMode _themeMode = ThemeMode.system;
  String _locale = 'ru';
  pb.UserProfileResponse? _profile;
  pb.AccessStateResponse? _accessState;
  List<pb.SubscriptionItem> _subscriptions = const [];
  List<pb.PaymentItem> _payments = const [];

  String? get userId => _userId;
  String? get ssUri => _ssUri;
  String get deviceId => _deviceId;
  bool get vpnOn => _vpnOn;
  bool get vpnConnecting => _vpnConnecting;
  bool get selectedOnlyMode => _selectedOnlyMode;
  bool get loading => _loading;
  bool get accountLoading => _accountLoading;
  bool get hasUserId => _userId != null && _userId!.trim().isNotEmpty;
  String? get error => _error;
  String get apiBase => _apiBase;
  bool get vpnSupported => _vpn.isSupported;
  ThemeMode get themeMode => _themeMode;
  String get locale => _locale;
  pb.UserProfileResponse? get profile => _profile;
  pb.AccessStateResponse? get accessState => _accessState;
  List<pb.SubscriptionItem> get subscriptions => _subscriptions;
  List<pb.PaymentItem> get payments => _payments;
  bool get isBlocked => _accessState?.isBlocked ?? _profile?.isBlocked ?? false;
  bool get legacyHappFallback =>
      _accessState?.legacyHappFallback ?? _profile?.legacyHappFallback ?? false;
  bool get canConnect => _accessState?.canConnect ?? false;

  Future<SharedPreferences> _prefs() => SharedPreferences.getInstance();

  Future<void> load() async {
    _loading = true;
    notifyListeners();
    try {
      final prefs = await _prefs();
      _userId = prefs.getString(_keyUserId)?.trim();
      _ssUri = prefs.getString(_keySsUri)?.trim();
      _vpnOn = false;
      _selectedOnlyMode = prefs.getBool(_keySelectedOnly) ?? false;
      final savedApi = prefs.getString(_keyApiBase);
      if (savedApi != null && savedApi.isNotEmpty) {
        _apiBase = savedApi.replaceFirst(RegExp(r'/$'), '');
      }
      final savedLocale = prefs.getString(_keyLocale);
      if (savedLocale != null && (savedLocale == 'en' || savedLocale == 'ru')) {
        _locale = savedLocale;
      }
      final themeStr = prefs.getString(_keyThemeMode);
      _themeMode = themeStr == 'light'
          ? ThemeMode.light
          : themeStr == 'dark'
              ? ThemeMode.dark
              : ThemeMode.system;
      _deviceId = prefs.getString(_keyDeviceId)?.trim() ?? '';
      if (_deviceId.isEmpty) {
        _deviceId = const Uuid().v4();
        await prefs.setString(_keyDeviceId, _deviceId);
      }
      if (hasUserId) {
        await refreshAccountData(notify: false);
      }
    } catch (e) {
      _error = e.toString();
    }
    _loading = false;
    notifyListeners();
  }

  Future<void> setApiBase(String value) async {
    final clean = value.trim().replaceFirst(RegExp(r'/$'), '');
    if (clean.isEmpty) {
      return;
    }
    _apiBase = clean;
    final prefs = await _prefs();
    await prefs.setString(_keyApiBase, _apiBase);
    _error = null;
    if (hasUserId) {
      await refreshAccountData(notify: false);
    }
    notifyListeners();
  }

  Future<void> setSelectedOnlyMode(bool value) async {
    _selectedOnlyMode = value;
    final prefs = await _prefs();
    await prefs.setBool(_keySelectedOnly, value);
    if (_vpnOn) {
      await setVpnOn(false);
      await setVpnOn(true);
    }
    notifyListeners();
  }

  Future<void> setThemeMode(ThemeMode mode) async {
    _themeMode = mode;
    final prefs = await _prefs();
    await prefs.setString(
      _keyThemeMode,
      mode == ThemeMode.light
          ? 'light'
          : mode == ThemeMode.dark
              ? 'dark'
              : 'system',
    );
    notifyListeners();
  }

  Future<void> setLocale(String languageCode) async {
    if (languageCode != 'ru' && languageCode != 'en') {
      return;
    }
    _locale = languageCode;
    final prefs = await _prefs();
    await prefs.setString(_keyLocale, _locale);
    notifyListeners();
  }

  Future<void> signOut() async {
    if (_vpnOn) {
      await setVpnOn(false);
    }
    final prefs = await _prefs();
    await prefs.remove(_keyUserId);
    await prefs.remove(_keySsUri);
    _userId = null;
    _ssUri = null;
    _profile = null;
    _accessState = null;
    _subscriptions = const [];
    _payments = const [];
    _error = null;
    notifyListeners();
  }

  Future<bool> restoreUserId(String id) async {
    final clean = id.trim();
    if (clean.isEmpty) {
      return false;
    }
    if (_vpnOn) {
      await setVpnOn(false);
    }
    _userId = clean;
    final prefs = await _prefs();
    await prefs.setString(_keyUserId, clean);
    await _invalidateConnectionBundle();
    final ok = await refreshAccountData(notify: false);
    notifyListeners();
    return ok;
  }

  Future<bool> claimByCode(String code) async {
    final clean = code.trim().toUpperCase();
    if (clean.isEmpty) {
      return false;
    }
    try {
      final resp = await claimByCodeGrpc(
        apiBase: _apiBase,
        code: clean,
        device: _deviceContext(linkedVia: 'claim_code'),
      ).timeout(const Duration(seconds: 15));
      if (resp == null || resp.profile.userId.trim().isEmpty) {
        _error = 'Не удалось привязать приложение к аккаунту.';
        notifyListeners();
        return false;
      }
      return await _acceptClaim(resp);
    } catch (e) {
      _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      notifyListeners();
      return false;
    }
  }

  Future<bool> claimByToken(String token) async {
    final clean = token.trim();
    if (clean.isEmpty) {
      return false;
    }
    try {
      final resp = await claimByTokenGrpc(
        apiBase: _apiBase,
        token: clean,
        device: _deviceContext(linkedVia: 'claim_token'),
      ).timeout(const Duration(seconds: 15));
      if (resp == null || resp.profile.userId.trim().isEmpty) {
        _error = 'Не удалось открыть вход по ссылке.';
        notifyListeners();
        return false;
      }
      return await _acceptClaim(resp);
    } catch (e) {
      _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      notifyListeners();
      return false;
    }
  }

  Future<void> handleIncomingUri(Uri uri) async {
    final token = uri.queryParameters['token']?.trim() ?? '';
    final code = uri.queryParameters['code']?.trim() ?? '';
    final userId = uri.queryParameters['user_id']?.trim() ?? '';
    if (token.isNotEmpty) {
      await claimByToken(token);
      return;
    }
    if (code.isNotEmpty) {
      await claimByCode(code);
      return;
    }
    if (userId.isNotEmpty) {
      await restoreUserId(userId);
    }
  }

  Future<bool> refreshAccountData({bool notify = true}) async {
    if (!hasUserId) {
      return false;
    }
    _accountLoading = true;
    if (notify) {
      notifyListeners();
    }
    try {
      final me = await getMeGrpc(
        apiBase: _apiBase,
        userId: _userId!,
        device: _deviceContext(linkedVia: 'app_open'),
      ).timeout(const Duration(seconds: 15));
      final access = await getAccessStateGrpc(
        apiBase: _apiBase,
        userId: _userId!,
        platform: _currentPlatform(),
      ).timeout(const Duration(seconds: 15));
      final subs = await listSubscriptionsGrpc(
        apiBase: _apiBase,
        userId: _userId!,
      ).timeout(const Duration(seconds: 15));
      final pays = await listPaymentsGrpc(
        apiBase: _apiBase,
        userId: _userId!,
      ).timeout(const Duration(seconds: 15));

      _profile = me;
      _accessState = access;
      _subscriptions = subs?.items ?? const [];
      _payments = pays?.items ?? const [];
      _error = null;

      if (isBlocked || legacyHappFallback || !canConnect) {
        await _invalidateConnectionBundle();
      }
      return true;
    } catch (e) {
      _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      return false;
    } finally {
      _accountLoading = false;
      if (notify) {
        notifyListeners();
      }
    }
  }

  Future<bool> applyPromocode(String code) async {
    final clean = code.trim();
    if (!hasUserId || clean.isEmpty) {
      return false;
    }
    try {
      final resp = await applyPromocodeGrpc(_apiBase, _userId!, clean)
          .timeout(const Duration(seconds: 15));
      if (resp == null || !resp.success) {
        _error = resp?.message ?? 'Не удалось применить промокод.';
        notifyListeners();
        return false;
      }
      await _invalidateConnectionBundle();
      await refreshAccountData(notify: false);
      _error = null;
      notifyListeners();
      return true;
    } catch (e) {
      _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      notifyListeners();
      return false;
    }
  }

  Future<bool> fetchConnectionBundle({bool forceRefresh = false}) async {
    if (!hasUserId) {
      return false;
    }
    try {
      final resp = await getConnectionBundleGrpc(
        apiBase: _apiBase,
        userId: _userId!,
        platform: _currentPlatform(),
        forceRefresh: forceRefresh,
      ).timeout(const Duration(seconds: 15));
      if (resp == null) {
        _error = 'Сервер не ответил.';
        notifyListeners();
        return false;
      }
      if (resp.legacyHappFallback) {
        _error = resp.fallbackInstruction.isNotEmpty
            ? resp.fallbackInstruction
            : 'Для этой платформы пока используется fallback через Happ.';
        await _invalidateConnectionBundle();
        notifyListeners();
        return false;
      }
      final ssUri = resp.ssUri.trim();
      if (ssUri.isEmpty) {
        _error = resp.statusMessage.isNotEmpty
            ? resp.statusMessage
            : 'Сервер не вернул конфиг подключения.';
        notifyListeners();
        return false;
      }
      _ssUri = ssUri;
      final prefs = await _prefs();
      await prefs.setString(_keySsUri, ssUri);
      _error = null;
      notifyListeners();
      return true;
    } catch (e) {
      _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      notifyListeners();
      return false;
    }
  }

  Future<void> setVpnOn(bool on) async {
    if (!_vpn.isSupported && on) {
      _error = 'HeYearth доступен только на Android, iOS и Windows.';
      notifyListeners();
      return;
    }
    if (!on) {
      _vpnConnecting = false;
      _vpnOn = false;
      _error = null;
      try {
        await _vpn.disconnect();
      } catch (_) {}
      final prefs = await _prefs();
      await prefs.setBool(_keyVpnOn, false);
      notifyListeners();
      return;
    }

    _vpnConnecting = true;
    _error = null;
    notifyListeners();
    try {
      final update = await checkForUpdate(_apiBase);
      if (update.update?.requiredByPolicy == true) {
        _error = update.update!.mandatory
            ? 'Требуется обязательное обновление до ${update.update!.latestVersion}.'
            : 'Текущая версия больше не поддерживается. Обнови приложение.';
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      final refreshed = await refreshAccountData(notify: false);
      if (!refreshed) {
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (isBlocked) {
        _error = 'Доступ заблокирован. Обратись в поддержку.';
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (legacyHappFallback) {
        _error =
            'Для этой платформы пока используется fallback через Happ/бот.';
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (!canConnect) {
        _error = _accessState?.reason.isNotEmpty == true
            ? _accessState!.reason
            : 'Сейчас подключение недоступно.';
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (_ssUri == null || _ssUri!.trim().isEmpty) {
        final ok = await fetchConnectionBundle(forceRefresh: true);
        if (!ok || _ssUri == null) {
          _vpnConnecting = false;
          notifyListeners();
          return;
        }
      }
      await _vpn.connect(_ssUri!, selectedOnly: _selectedOnlyMode);
      _vpnOn = true;
      final prefs = await _prefs();
      await prefs.setBool(_keyVpnOn, true);
    } catch (e) {
      _vpnOn = false;
      _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      if (_error!.length > 160) {
        _error = '${_error!.substring(0, 157)}...';
      }
    } finally {
      _vpnConnecting = false;
      notifyListeners();
    }
  }

  void setError(String? e) {
    _error = e;
    notifyListeners();
  }

  Future<bool> _acceptClaim(pb.ClaimResponse response) async {
    final userId = response.profile.userId.trim();
    if (userId.isEmpty) {
      return false;
    }
    if (_vpnOn) {
      await setVpnOn(false);
    }
    _userId = userId;
    _profile = response.profile;
    _accessState = response.accessState;
    final prefs = await _prefs();
    await prefs.setString(_keyUserId, userId);
    await _invalidateConnectionBundle();
    await refreshAccountData(notify: false);
    _error = null;
    notifyListeners();
    return true;
  }

  Future<void> _invalidateConnectionBundle() async {
    _ssUri = null;
    final prefs = await _prefs();
    await prefs.remove(_keySsUri);
  }

  String _currentPlatform() {
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

  pb.DeviceContext _deviceContext({required String linkedVia}) {
    return buildDeviceContext(
      platform: _currentPlatform(),
      deviceId: _deviceId,
      locale: _locale,
      linkedVia: linkedVia,
      deviceLabel: defaultTargetPlatform.name,
    );
  }
}
