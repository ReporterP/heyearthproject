import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import 'connection_manager.dart';
import 'generated/heyearth/v1/config.pb.dart' as pb;
import 'grpc_client.dart';
import 'l10n.dart';
import 'update_service.dart';
import 'vpn_service_interface.dart';

const _keyUserId = 'heyearth_user_id';
const _keySsUri = 'heyearth_ss_uri';
const _keyVlessUri = 'heyearth_vless_uri';
const _keyVpnOn = 'heyearth_vpn_on';
const _keyApiBase = 'heyearth_api_base';
const _keySelectedOnly = 'heyearth_selected_only';
const _keyThemeMode = 'heyearth_theme_mode';
const _keyLocale = 'heyearth_locale';
const _keyDeviceId = 'heyearth_device_id';
const _keyApiEndpoints = 'heyearth_api_endpoints';
const _defaultApiBase = String.fromEnvironment(
  'API_BASE',
  defaultValue: 'https://quizgame.pro',
);

class AppState extends ChangeNotifier {
  AppState({required VpnService vpn}) : _vpn = vpn;

  final VpnService _vpn;
  final ConnectionManager _connMgr = ConnectionManager();

  String? _userId;
  String? _ssUri;
  String? _vlessUri;
  List<pb.ConnectionOption> _connectionOptions = const [];
  pb.ConnectionOption? _activeOption;
  Timer? _healthTimer;
  int _failoverAttempts = 0;
  bool _allowManualChoice = false;
  String _deviceId = '';
  bool _vpnOn = false;
  bool _vpnConnecting = false;
  bool _selectedOnlyMode = false;
  bool _loading = true;
  bool _accountLoading = false;
  String? _error;
  String _apiBase = _defaultApiBase;
  List<String> _apiEndpoints = const [];
  ThemeMode _themeMode = ThemeMode.system;
  String _locale = 'ru';
  pb.UserProfileResponse? _profile;
  pb.AccessStateResponse? _accessState;
  List<pb.SubscriptionItem> _subscriptions = const [];
  List<pb.PaymentItem> _payments = const [];

  String? get userId => _userId;
  String? get ssUri => _ssUri;
  String? get vlessUri => _vlessUri;
  String? get preferredUri => _activeOption?.uri ?? _vlessUri ?? _ssUri;
  List<pb.ConnectionOption> get connectionOptions => _connectionOptions;
  pb.ConnectionOption? get activeOption => _activeOption;
  bool get allowManualChoice => _allowManualChoice;
  NetworkType get currentNetwork => _connMgr.currentNetwork;
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
      await _connMgr.init();
      _connMgr.onNetworkChanged = _onNetworkChanged;
      final prefs = await _prefs();
      _userId = prefs.getString(_keyUserId)?.trim();
      _ssUri = prefs.getString(_keySsUri)?.trim();
      _vlessUri = prefs.getString(_keyVlessUri)?.trim();
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
      final savedEndpoints = prefs.getStringList(_keyApiEndpoints);
      if (savedEndpoints != null && savedEndpoints.isNotEmpty) {
        _apiEndpoints = savedEndpoints;
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
    await prefs.remove(_keyVlessUri);
    _userId = null;
    _ssUri = null;
    _vlessUri = null;
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
        _error = _l10n.claimFailed;
        notifyListeners();
        return false;
      }
      return await _acceptClaim(resp);
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
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
        _error = _l10n.claimByTokenFailed;
        notifyListeners();
        return false;
      }
      return await _acceptClaim(resp);
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
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
      _allowManualChoice = access?.allowManualConnectionChoice ?? false;
      _subscriptions = subs?.items ?? const [];
      _payments = pays?.items ?? const [];
      _error = null;

      if (isBlocked || legacyHappFallback || !canConnect) {
        await _invalidateConnectionBundle();
      }
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
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
        final serverMsg = resp?.message.trim() ?? '';
        _error = serverMsg.isNotEmpty ? serverMsg : _l10n.promoFailed;
        notifyListeners();
        return false;
      }
      await _invalidateConnectionBundle();
      await refreshAccountData(notify: false);
      _error = null;
      notifyListeners();
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
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
        _error = _l10n.serverNoResponse;
        notifyListeners();
        return false;
      }
      if (resp.legacyHappFallback) {
        final instr = resp.fallbackInstruction.trim();
        _error = instr.isNotEmpty ? instr : _l10n.legacyHappFallback;
        await _invalidateConnectionBundle();
        notifyListeners();
        return false;
      }
      final ssUri = resp.ssUri.trim();
      final vlessUri = resp.vlessUri.trim();
      _connectionOptions = List.unmodifiable(resp.connectionOptions);
      if (ssUri.isEmpty && vlessUri.isEmpty && _connectionOptions.isEmpty) {
        final statusMsg = resp.statusMessage.trim();
        _error = statusMsg.isNotEmpty ? statusMsg : _l10n.serverNoConfig;
        notifyListeners();
        return false;
      }
      _ssUri = ssUri.isNotEmpty ? ssUri : null;
      _vlessUri = vlessUri.isNotEmpty ? vlessUri : null;
      _activeOption = _connMgr.pickBest(_connectionOptions);
      final prefs = await _prefs();
      if (_ssUri != null) {
        await prefs.setString(_keySsUri, _ssUri!);
      } else {
        await prefs.remove(_keySsUri);
      }
      if (_vlessUri != null) {
        await prefs.setString(_keyVlessUri, _vlessUri!);
      } else {
        await prefs.remove(_keyVlessUri);
      }
      _error = null;
      notifyListeners();
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
      notifyListeners();
      return false;
    }
  }

  Future<void> setVpnOn(bool on) async {
    if (!_vpn.isSupported && on) {
      _error = _l10n.vpnUnsupported;
      notifyListeners();
      return;
    }
    if (!on) {
      _vpnConnecting = false;
      _vpnOn = false;
      _error = null;
      _healthTimer?.cancel();
      _healthTimer = null;
      _failoverAttempts = 0;
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
      if (update.apiEndpoints != null && update.apiEndpoints!.isNotEmpty) {
        _apiEndpoints = update.apiEndpoints!;
        final prefs = await _prefs();
        await prefs.setStringList(_keyApiEndpoints, _apiEndpoints);
      }
      if (update.update?.requiredByPolicy == true) {
        _error = update.update!.mandatory
            ? _l10n.mandatoryUpdateRequiredMsg(update.update!.latestVersion)
            : _l10n.versionUnsupported;
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
        _error = _l10n.accessBlocked;
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (legacyHappFallback) {
        _error = _l10n.legacyFallbackVpn;
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (!canConnect) {
        final reason = _accessState?.reason.trim() ?? '';
        _error = reason.isNotEmpty ? reason : _l10n.currentConnectionUnavailable;
        _vpnConnecting = false;
        notifyListeners();
        return;
      }
      if (preferredUri == null || preferredUri!.trim().isEmpty) {
        final ok = await fetchConnectionBundle(forceRefresh: true);
        if (!ok || preferredUri == null) {
          _vpnConnecting = false;
          notifyListeners();
          return;
        }
      }
      await _vpn.connect(preferredUri!, selectedOnly: _selectedOnlyMode);
      _vpnOn = true;
      _failoverAttempts = 0;
      _startHealthMonitor();
      final prefs = await _prefs();
      await prefs.setBool(_keyVpnOn, true);
    } catch (e) {
      _vpnOn = false;
      final msg = _l10n.grpcErrorToMessage(e);
      _error = msg.length > 160 ? '${msg.substring(0, 157)}...' : msg;
    } finally {
      _vpnConnecting = false;
      notifyListeners();
    }
  }

  void _startHealthMonitor() {
    _healthTimer?.cancel();
    _healthTimer = Timer.periodic(const Duration(seconds: 20), (_) {
      _checkConnectionHealth();
    });
  }

  Future<void> _checkConnectionHealth() async {
    if (!_vpnOn || _vpnConnecting) return;
    if (_connectionOptions.length <= 1) return;
    if (_failoverAttempts >= _connectionOptions.length) return;

    final healthy = await _vpn.isTrafficFlowing();
    if (healthy) {
      _failoverAttempts = 0;
      return;
    }

    if (_activeOption == null) return;
    final next = _connMgr.nextFallback(_connectionOptions, _activeOption!);
    if (next == null) return;

    _failoverAttempts++;
    debugPrint('Auto-failover: switching from ${_activeOption!.protocol} to ${next.protocol}');
    _activeOption = next;
    try {
      await _vpn.disconnect();
      await _vpn.connect(next.uri, selectedOnly: _selectedOnlyMode);
    } catch (e) {
      debugPrint('Failover connect error: $e');
    }
    notifyListeners();
  }

  void _onNetworkChanged(NetworkType newType) {
    if (!_vpnOn || _connectionOptions.isEmpty) return;
    final best = _connMgr.pickBest(_connectionOptions);
    if (best != null && best.uri != _activeOption?.uri) {
      _activeOption = best;
      _reconnectWithCurrentOption();
    }
  }

  Future<void> _reconnectWithCurrentOption() async {
    if (!_vpnOn || _activeOption == null) return;
    try {
      await _vpn.disconnect();
      await _vpn.connect(_activeOption!.uri, selectedOnly: _selectedOnlyMode);
    } catch (e) {
      debugPrint('Reconnect on network change error: $e');
    }
    notifyListeners();
  }

  void selectConnectionOption(pb.ConnectionOption option) {
    if (!_allowManualChoice) return;
    _activeOption = option;
    if (_vpnOn) {
      _reconnectWithCurrentOption();
    }
    notifyListeners();
  }

  void setError(String? e) {
    _error = e;
    notifyListeners();
  }

  L10n get _l10n => L10n(Locale(_locale));

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
    _vlessUri = null;
    _connectionOptions = const [];
    _activeOption = null;
    final prefs = await _prefs();
    await prefs.remove(_keySsUri);
    await prefs.remove(_keyVlessUri);
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
