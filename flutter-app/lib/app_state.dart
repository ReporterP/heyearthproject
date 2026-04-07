import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import 'connection_manager.dart';
import 'generated/heyearth/v1/config.pb.dart' as pb;
import 'grpc_client.dart';
import 'l10n.dart';
import 'update_service.dart';
import 'user_api.dart';
import 'vpn_service_interface.dart';

const _keyUserId = 'heyearth_user_id';
const _keyJwtToken = 'jwt_token';
const _keyVpnOn = 'heyearth_vpn_on';
const _keyApiBase = 'heyearth_api_base';
const _keyThemeMode = 'heyearth_theme_mode';
const _keyLocale = 'heyearth_locale';
const _keyDeviceId = 'heyearth_device_id';
const _keyApiEndpoints = 'heyearth_api_endpoints';
const _keySavedAccounts = 'heyearth_saved_accounts';
const _defaultApiBase = String.fromEnvironment(
  'API_BASE',
  defaultValue: 'https://hiearth.ru',
);

class AppState extends ChangeNotifier {
  AppState({required VpnService vpn}) : _vpn = vpn;

  final VpnService _vpn;
  final ConnectionManager _connMgr = ConnectionManager();

  String? _userId;
  String? _jwtToken;
  String? _username;
  bool _hasPassword = false;
  List<pb.ConnectionOption> _connectionOptions = const [];
  pb.ConnectionOption? _activeOption;
  Timer? _healthTimer;
  int _failoverAttempts = 0;
  String _deviceId = '';
  bool _vpnOn = false;
  bool _vpnConnecting = false;
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
  List<String> _savedAccounts = const [];

  String? get userId => _userId;
  String? get jwtToken => _jwtToken;
  String? get username => _username;
  bool get hasPassword => _hasPassword;
  List<String> get savedAccounts => _savedAccounts;
  String? get preferredUri => _activeOption?.uri;
  List<pb.ConnectionOption> get connectionOptions => _connectionOptions;
  pb.ConnectionOption? get activeOption => _activeOption;
  NetworkType get currentNetwork => _connMgr.currentNetwork;
  String get deviceId => _deviceId;
  bool get vpnOn => _vpnOn;
  bool get vpnConnecting => _vpnConnecting;
  bool get loading => _loading;
  bool get accountLoading => _accountLoading;
  bool get hasUserId => _userId != null && _userId!.trim().isNotEmpty;
  bool get hasJwt => _jwtToken != null && _jwtToken!.trim().isNotEmpty;
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
  // These fields will be available after proto regeneration.
  // For now, access them safely via try-catch until the generated code is updated.
  bool get isPremium {
    try {
      return (_accessState as dynamic).isPremium as bool? ?? false;
    } catch (_) {
      return false;
    }
  }

  bool get trafficMaskingAvailable {
    try {
      return (_accessState as dynamic).trafficMaskingAvailable as bool? ?? false;
    } catch (_) {
      return false;
    }
  }
  bool get canConnect => _accessState?.canConnect ?? false;

  Future<SharedPreferences> _prefs() => SharedPreferences.getInstance();

  UserApi get _userApi => UserApi(_apiBase);

  Future<void> load() async {
    _loading = true;
    notifyListeners();
    try {
      await _connMgr.init();
      _connMgr.onNetworkChanged = _onNetworkChanged;
      final prefs = await _prefs();
      _userId = prefs.getString(_keyUserId)?.trim();
      _jwtToken = prefs.getString(_keyJwtToken)?.trim();
      _vpnOn = false;
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
      final savedJson = prefs.getString(_keySavedAccounts);
      if (savedJson != null && savedJson.isNotEmpty) {
        try {
          final decoded = jsonDecode(savedJson) as List<dynamic>?;
          _savedAccounts = decoded != null
              ? List<String>.from(decoded.map((e) => e.toString()))
              : const [];
        } catch (_) {
          _savedAccounts = const [];
        }
      }
      if (_userId != null &&
          _userId!.trim().isNotEmpty &&
          !_savedAccounts.contains(_userId)) {
        _savedAccounts = [..._savedAccounts, _userId!];
        await prefs.setString(
            _keySavedAccounts, jsonEncode(_savedAccounts));
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

  // ── Auth: register ────────────────────────────────────────────────────────

  /// Register a new anonymous account via REST API.
  Future<bool> register() async {
    try {
      _accountLoading = true;
      _error = null;
      notifyListeners();

      final resp = await _userApi
          .register(
            platform: _currentPlatform(),
            deviceId: _deviceId,
          )
          .timeout(const Duration(seconds: 15));

      final newUserId = (resp['user_id'] as String?)?.trim() ?? '';
      final token = (resp['token'] as String?)?.trim() ?? '';
      if (newUserId.isEmpty || token.isEmpty) {
        _error = _l10n.registerFailed;
        return false;
      }

      if (_vpnOn) {
        await setVpnOn(false);
      }
      _userId = newUserId;
      _jwtToken = token;
      final prefs = await _prefs();
      await prefs.setString(_keyUserId, newUserId);
      await prefs.setString(_keyJwtToken, token);
      await _invalidateConnectionBundle();
      await addAccount(newUserId);
      await refreshAccountData(notify: false);
      _error = null;
      notifyListeners();
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
      notifyListeners();
      return false;
    } finally {
      _accountLoading = false;
      notifyListeners();
    }
  }

  // ── Auth: login ───────────────────────────────────────────────────────────

  /// Login by HeYearth ID or username, with optional password.
  Future<bool> login(String idOrUsername, String? password) async {
    final clean = idOrUsername.trim();
    if (clean.isEmpty) {
      return false;
    }
    try {
      _accountLoading = true;
      _error = null;
      notifyListeners();

      final resp = await _userApi
          .login(
            idOrUsername: clean,
            password: password,
          )
          .timeout(const Duration(seconds: 15));

      final newUserId = (resp['user_id'] as String?)?.trim() ?? '';
      final token = (resp['token'] as String?)?.trim() ?? '';
      if (newUserId.isEmpty) {
        _error = _l10n.loginFailed;
        return false;
      }

      if (_vpnOn) {
        await setVpnOn(false);
      }
      _userId = newUserId;
      _jwtToken = token.isNotEmpty ? token : _jwtToken;
      final prefs = await _prefs();
      await prefs.setString(_keyUserId, newUserId);
      if (token.isNotEmpty) {
        await prefs.setString(_keyJwtToken, token);
      }
      await _invalidateConnectionBundle();
      await addAccount(newUserId);
      await refreshAccountData(notify: false);
      _error = null;
      notifyListeners();
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
      notifyListeners();
      return false;
    } finally {
      _accountLoading = false;
      notifyListeners();
    }
  }

  // ── Profile: set username ─────────────────────────────────────────────────

  /// Set or change the username via REST API.
  Future<bool> setUsername(String username) async {
    final clean = username.trim();
    if (clean.isEmpty || !hasJwt) {
      return false;
    }
    try {
      _error = null;
      await _userApi
          .updateProfile(token: _jwtToken!, username: clean)
          .timeout(const Duration(seconds: 15));
      _username = clean;
      notifyListeners();
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
      notifyListeners();
      return false;
    }
  }

  // ── Profile: set cloud password ───────────────────────────────────────────

  /// Set or change the cloud password via REST API.
  Future<bool> setCloudPassword(
      String newPassword, String? currentPassword) async {
    if (newPassword.trim().isEmpty || !hasJwt) {
      return false;
    }
    try {
      _error = null;
      await _userApi
          .updateProfile(
            token: _jwtToken!,
            newPassword: newPassword,
            currentPassword: currentPassword,
          )
          .timeout(const Duration(seconds: 15));
      _hasPassword = true;
      notifyListeners();
      return true;
    } catch (e) {
      _error = _l10n.grpcErrorToMessage(e);
      notifyListeners();
      return false;
    }
  }

  // ── Sign out ──────────────────────────────────────────────────────────────

  Future<void> signOut() async {
    if (_vpnOn) {
      await setVpnOn(false);
    }
    final prefs = await _prefs();
    await prefs.remove(_keyUserId);
    await prefs.remove(_keyJwtToken);
    _userId = null;
    _jwtToken = null;
    _username = null;
    _hasPassword = false;
    _profile = null;
    _accessState = null;
    _subscriptions = const [];
    _payments = const [];
    _connectionOptions = const [];
    _activeOption = null;
    _error = null;
    notifyListeners();
  }

  // ── Multi-account management ──────────────────────────────────────────────

  Future<void> addAccount(String id) async {
    final clean = id.trim();
    if (clean.isEmpty || _savedAccounts.contains(clean)) return;
    _savedAccounts = [..._savedAccounts, clean];
    final prefs = await _prefs();
    await prefs.setString(_keySavedAccounts, jsonEncode(_savedAccounts));
    notifyListeners();
  }

  Future<void> removeAccount(String id) async {
    final clean = id.trim();
    if (clean.isEmpty) return;
    _savedAccounts = _savedAccounts.where((a) => a != clean).toList();
    final prefs = await _prefs();
    await prefs.setString(_keySavedAccounts, jsonEncode(_savedAccounts));
    if (_userId == clean) {
      if (_savedAccounts.isNotEmpty) {
        await switchAccount(_savedAccounts.first);
      } else {
        await signOut();
      }
    }
    notifyListeners();
  }

  Future<void> switchAccount(String id) async {
    final clean = id.trim();
    if (clean.isEmpty || clean == _userId) return;
    final wasVpnOn = _vpnOn;
    if (_vpnOn) {
      await setVpnOn(false);
    }
    _userId = clean;
    // JWT may not be available for a switched account; user must login again
    _jwtToken = null;
    final prefs = await _prefs();
    await prefs.setString(_keyUserId, clean);
    await prefs.remove(_keyJwtToken);
    await _invalidateConnectionBundle();
    final ok = await refreshAccountData(notify: false);
    if (!ok) {
      _userId = null;
      await prefs.remove(_keyUserId);
    }
    if (wasVpnOn && ok && canConnect) {
      await setVpnOn(true);
    }
    notifyListeners();
  }

  /// Restore a user ID directly (used for backwards-compatible login by ID).
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
    if (ok) await addAccount(clean);
    notifyListeners();
    return ok;
  }

  // ── Deep link handling ────────────────────────────────────────────────────

  Future<void> handleIncomingUri(Uri uri) async {
    final userId = uri.queryParameters['user_id']?.trim() ?? '';
    if (userId.isNotEmpty) {
      await login(userId, null);
    }
  }

  // ── Account data refresh ──────────────────────────────────────────────────

  Future<bool> refreshAccountData({bool notify = true}) async {
    if (!hasUserId) {
      return false;
    }
    _accountLoading = true;
    if (notify) {
      notifyListeners();
    }
    try {
      // Parallel gRPC calls (independent, no need to be sequential)
      final results = await Future.wait([
        getMeGrpc(apiBase: _apiBase, userId: _userId!, device: _deviceContext(linkedVia: 'app_open')).timeout(const Duration(seconds: 15)),
        getAccessStateGrpc(apiBase: _apiBase, userId: _userId!, platform: _currentPlatform()).timeout(const Duration(seconds: 15)),
        listSubscriptionsGrpc(apiBase: _apiBase, userId: _userId!).timeout(const Duration(seconds: 15)),
        listPaymentsGrpc(apiBase: _apiBase, userId: _userId!).timeout(const Duration(seconds: 15)),
      ]);
      final me = results[0];
      final access = results[1];
      final subs = results[2];
      final pays = results[3];

      _profile = me;
      _accessState = access;
      // New proto fields — access safely until proto is regenerated.
      try {
        final u = (me as dynamic).username as String?;
        if (u != null && u.isNotEmpty) _username = u;
      } catch (_) {}
      try {
        _hasPassword = (me as dynamic).hasPassword as bool? ?? _hasPassword;
      } catch (_) {}
      _subscriptions = subs?.items ?? const [];
      _payments = pays?.items ?? const [];
      _error = null;

      if (isBlocked || !canConnect) {
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
      _connectionOptions = List.unmodifiable(resp.connectionOptions);
      if (_connectionOptions.isEmpty) {
        final statusMsg = resp.statusMessage.trim();
        _error = statusMsg.isNotEmpty ? statusMsg : _l10n.serverNoConfig;
        notifyListeners();
        return false;
      }
      _activeOption = _connMgr.pickBest(_connectionOptions);
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
      await _vpn.connect(preferredUri!);
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
    if (_failoverAttempts >= _connectionOptions.length) {
      // Reset after cooldown so failover can retry (previously-failed options may recover)
      _failoverAttempts = 0;
      return;
    }

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
      await _vpn.connect(next.uri);
    } catch (e) {
      debugPrint('Failover connect error: $e');
    }
    notifyListeners();
  }

  void _onNetworkChanged(NetworkType newType) {
    if (!_vpnOn || _vpnConnecting || _connectionOptions.isEmpty) return;
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
      await _vpn.connect(_activeOption!.uri);
    } catch (e) {
      debugPrint('Reconnect on network change error: $e');
    }
    notifyListeners();
  }

  void selectConnectionOption(pb.ConnectionOption option) {
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

  Future<void> _invalidateConnectionBundle() async {
    _connectionOptions = const [];
    _activeOption = null;
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

  @override
  void dispose() {
    _healthTimer?.cancel();
    _connMgr.dispose();
    super.dispose();
  }
}
