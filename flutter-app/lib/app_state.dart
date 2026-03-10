import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import 'package:heyearth_vpn/grpc_client.dart';
import 'package:heyearth_vpn/vpn_service_interface.dart';

const _keyUserId = 'heyearth_user_id';
const _keySsUri = 'heyearth_ss_uri';
const _keyVpnOn = 'heyearth_vpn_on';
const _keyApiBase = 'heyearth_api_base';
const _keySelectedOnly = 'heyearth_selected_only';
const _keyThemeMode = 'heyearth_theme_mode';
const _keyLocale = 'heyearth_locale';
const _defaultApiBase = 'https://heyearth.ru';

/// Состояние приложения. VPN включается/выключается одной кнопкой;
/// конфиг ss:// запрашивается с сервера и не показывается пользователю.
class AppState extends ChangeNotifier {
  String? _userId;
  String? _ssUri;
  bool _vpnOn = false;
  bool _vpnConnecting = false;
  bool _selectedOnlyMode = false;
  bool _loading = true;
  String? _error;
  String _apiBase = _defaultApiBase;
  ThemeMode _themeMode = ThemeMode.system;
  String _locale = 'ru';

  final VpnService _vpn;

  AppState({required VpnService vpn}) : _vpn = vpn;

  String? get userId => _userId;
  String? get ssUri => _ssUri;
  bool get vpnOn => _vpnOn;
  bool get vpnConnecting => _vpnConnecting;
  bool get selectedOnlyMode => _selectedOnlyMode;
  bool get loading => _loading;
  bool get hasUserId => _userId != null && _userId!.trim().isNotEmpty;
  String? get error => _error;
  String get apiBase => _apiBase;
  bool get vpnSupported => _vpn.isSupported;
  ThemeMode get themeMode => _themeMode;
  String get locale => _locale;

  Future<SharedPreferences> _prefs() => SharedPreferences.getInstance();

  Future<void> load() async {
    _loading = true;
    notifyListeners();
    try {
      final prefs = await _prefs();
      _userId = prefs.getString(_keyUserId);
      _ssUri = prefs.getString(_keySsUri);
      // Не восстанавливаем _vpnOn при старте: после перезапуска приложения VPN-сервис не активен.
      _vpnOn = false;
      _selectedOnlyMode = prefs.getBool(_keySelectedOnly) ?? false;
      final themeStr = prefs.getString(_keyThemeMode);
      _themeMode = themeStr == 'light'
          ? ThemeMode.light
          : themeStr == 'dark'
              ? ThemeMode.dark
              : ThemeMode.system;
      final savedApi = prefs.getString(_keyApiBase);
      if (savedApi != null && savedApi.isNotEmpty) {
        _apiBase = savedApi.replaceFirst(RegExp(r'/$'), '');
      }
      final savedLocale = prefs.getString(_keyLocale);
      if (savedLocale != null && (savedLocale == 'en' || savedLocale == 'ru')) {
        _locale = savedLocale;
      }
      if (!_vpn.isSupported) _vpnOn = false;
    } catch (_) {}
    _loading = false;
    _error = null;
    notifyListeners();
  }

  /// Выбор «Я новый пользователь» — создаём ID и переходим в основное приложение.
  Future<void> chooseNewUser() async {
    _userId = const Uuid().v4();
    final prefs = await _prefs();
    await prefs.setString(_keyUserId, _userId!);
    _error = null;
    notifyListeners();
  }

  Future<void> setApiBase(String value) async {
    final clean = value.trim().replaceFirst(RegExp(r'/$'), '');
    if (clean.isEmpty) return;
    _apiBase = clean;
    final prefs = await _prefs();
    await prefs.setString(_keyApiBase, _apiBase);
    _error = null;
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

  /// Получить конфиг с сервера (ss_uri) по gRPC. Вызывается автоматически при включении VPN.
  Future<bool> fetchConfig() async {
    if (_userId == null) return false;
    _error = null;
    notifyListeners();
    try {
      final resp = await getConfigGrpc(_apiBase, _userId!).timeout(const Duration(seconds: 15));
      if (resp == null) {
        _error = 'Сервер не ответил.';
        notifyListeners();
        return false;
      }
      _ssUri = resp.ssUri.trim().isEmpty ? null : resp.ssUri;
      if (_ssUri == null || _ssUri!.trim().isEmpty) {
        _error = 'Сервер не вернул конфиг. Попробуйте ещё раз.';
        notifyListeners();
        return false;
      }
      final prefs = await _prefs();
      await prefs.setString(_keySsUri, _ssUri!);
      notifyListeners();
      return true;
    } catch (e) {
      final msg = e.toString().toLowerCase();
      if (msg.contains('socketexception') || msg.contains('отклонил') ||
          msg.contains('connection refused') || msg.contains('connection reset')) {
        _error = 'Сервер недоступен. Проверьте интернет и что $_apiBase открыт.';
      } else if (msg.contains('timeout') || msg.contains('timed out')) {
        _error = 'Таймаут. Сервер не отвечает.';
      } else {
        _error = e.toString().replaceFirst(RegExp(r'Exception:?\s*'), '');
      }
      notifyListeners();
      return false;
    }
  }

  /// Включить или выключить VPN. При включении при необходимости запрашивает конфиг и поднимает туннель внутри приложения.
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
      if (_ssUri == null || _ssUri!.trim().isEmpty) {
        final ok = await fetchConfig();
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
      if (_error!.length > 120) _error = '${_error!.substring(0, 117)}...';
    } finally {
      _vpnConnecting = false;
      notifyListeners();
    }
  }

  void setError(String? e) {
    _error = e;
    notifyListeners();
  }

  Future<void> setThemeMode(ThemeMode mode) async {
    _themeMode = mode;
    final prefs = await _prefs();
    await prefs.setString(
      _keyThemeMode,
      mode == ThemeMode.light ? 'light' : mode == ThemeMode.dark ? 'dark' : 'system',
    );
    notifyListeners();
  }

  Future<void> setLocale(String languageCode) async {
    if (languageCode != 'ru' && languageCode != 'en') return;
    _locale = languageCode;
    final prefs = await _prefs();
    await prefs.setString(_keyLocale, _locale);
    notifyListeners();
  }

  /// Войти по ID с другого устройства (восстановить аккаунт).
  Future<bool> restoreUserId(String id) async {
    final clean = id.trim();
    if (clean.isEmpty) return false;
    _userId = clean;
    final prefs = await _prefs();
    await prefs.setString(_keyUserId, _userId!);
    await prefs.remove(_keySsUri);
    _ssUri = null;
    _error = null;
    if (_vpnOn) {
      await setVpnOn(false);
    }
    notifyListeners();
    return await fetchConfig();
  }
}
