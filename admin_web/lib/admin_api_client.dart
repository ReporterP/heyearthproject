import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'app_config.dart';
import 'models.dart';

class PagedResult<T> {
  const PagedResult({
    required this.items,
    required this.total,
    required this.page,
    required this.pageSize,
  });

  final List<T> items;
  final int total;
  final int page;
  final int pageSize;

  int get totalPages => (total / pageSize).ceil().clamp(1, 999999);
}

class AdminApiException implements Exception {
  const AdminApiException(this.message, {this.statusCode});

  final String message;
  final int? statusCode;

  @override
  String toString() => message;
}

class AdminApiClient {
  const AdminApiClient({this.apiBase = kDefaultAdminApiBase});

  final String apiBase;

  Uri _uri(String path) {
    final base = apiBase.trim().replaceFirst(RegExp(r'/$'), '');
    return Uri.parse('$base$path');
  }

  Future<LoginResult> login({
    required String username,
    required String password,
  }) async {
    final response = await http.post(
      _uri('/login'),
      headers: const {'Content-Type': 'application/json'},
      body: jsonEncode(<String, String>{
        'username': username.trim(),
        'password': password,
      }),
    );
    final body = _decodeBody(response);
    if (response.statusCode != 200) {
      throw AdminApiException(
        _extractError(body, fallback: 'Login failed'),
        statusCode: response.statusCode,
      );
    }
    return LoginResult.fromJson(body);
  }

  Future<DashboardOverview> fetchOverview(String token) async {
    final body = await _getBody('/overview', token);
    return DashboardOverview.fromJson(body);
  }

  Future<List<ManagedUser>> fetchUsers(String token) async {
    final body = await _getList('/users', token);
    return body.map(ManagedUser.fromJson).toList(growable: false);
  }

  Future<void> createUser(String token, Map<String, dynamic> payload) async {
    await _post('/users', token, payload, expectedStatus: 201);
  }

  Future<void> updateUser(
      String token, int id, Map<String, dynamic> payload) async {
    await _patch('/users/$id', token, payload);
  }

  Future<void> deleteUser(String token, int id) async {
    await _delete('/users/$id', token);
  }

  Future<List<UserDevice>> fetchUserDevices(String token, int userId) async {
    final body = await _getList('/users/$userId/devices', token);
    return body.map(UserDevice.fromJson).toList(growable: false);
  }

  Future<List<GroupModel>> fetchGroups(String token) async {
    final body = await _getList('/groups', token);
    return body.map(GroupModel.fromJson).toList(growable: false);
  }

  Future<void> createGroup(String token, Map<String, dynamic> payload) async {
    await _post('/groups', token, payload, expectedStatus: 201);
  }

  Future<void> updateGroup(
      String token, int id, Map<String, dynamic> payload) async {
    await _patch('/groups/$id', token, payload);
  }

  Future<void> deleteGroup(String token, int id) async {
    await _delete('/groups/$id', token);
  }

  Future<List<SubscriptionModel>> fetchSubscriptions(String token) async {
    final body = await _getList('/subscriptions', token);
    return body.map(SubscriptionModel.fromJson).toList(growable: false);
  }

  Future<void> saveSubscription(
      String token, Map<String, dynamic> payload) async {
    await _post('/subscriptions', token, payload);
  }

  Future<void> deleteSubscription(
      String token, int userDbId, int groupId) async {
    await _delete('/subscriptions/$userDbId/$groupId', token);
  }

  Future<List<PromocodeModel>> fetchPromocodes(String token) async {
    final body = await _getList('/promocodes', token);
    return body.map(PromocodeModel.fromJson).toList(growable: false);
  }

  Future<void> createPromocode(
      String token, Map<String, dynamic> payload) async {
    await _post('/promocodes', token, payload, expectedStatus: 201);
  }

  Future<void> updatePromocode(
      String token, String code, Map<String, dynamic> payload) async {
    await _patch('/promocodes/$code', token, payload);
  }

  Future<void> deletePromocode(String token, String code) async {
    await _delete('/promocodes/$code', token);
  }

  Future<List<PaymentModel>> fetchPayments(String token) async {
    final body = await _getList('/payments', token);
    return body.map(PaymentModel.fromJson).toList(growable: false);
  }

  Future<void> createPayment(String token, Map<String, dynamic> payload) async {
    await _post('/payments', token, payload, expectedStatus: 201);
  }

  Future<void> updatePayment(
      String token, int id, Map<String, dynamic> payload) async {
    await _patch('/payments/$id', token, payload);
  }

  Future<List<SshKey>> fetchSshKeys(String token) async {
    final body = await _getList('/ssh-keys', token);
    return body.map(SshKey.fromJson).toList(growable: false);
  }

  Future<SshKey> createSshKey(
      String token, String name, String privateKey) async {
    final body = await _post(
        '/ssh-keys', token, {'name': name, 'private_key': privateKey},
        expectedStatus: 201);
    return SshKey.fromJson(body);
  }

  Future<SshKey> updateSshKey(
      String token, int id, String name, String privateKey) async {
    final body = await _patch(
        '/ssh-keys/$id', token, {'name': name, 'private_key': privateKey});
    return SshKey.fromJson(body);
  }

  Future<void> deleteSshKey(String token, int id) async {
    await _delete('/ssh-keys/$id', token);
  }

  Future<List<AdminServer>> fetchServers(String token) async {
    final body = await _getList('/servers', token);
    return body.map(AdminServer.fromJson).toList(growable: false);
  }

  Future<AdminServer> fetchServerDetail(String token, int id) async {
    final body = await _getBody('/servers/$id', token);
    return AdminServer.fromJson(body);
  }

  Future<void> createServer(String token, ServerPayload payload) async {
    await _post('/servers', token, payload.toJson(), expectedStatus: 201);
  }

  Future<void> updateServer(String token, int id, ServerPayload payload) async {
    await _patch('/servers/$id', token, payload.toJson());
  }

  Future<void> deleteServer(String token, int id) async {
    await _delete('/servers/$id', token);
  }

  Future<void> runServerAction(String token, int id, String action) async {
    await _post(
        '/servers/$id/actions', token, <String, dynamic>{'action': action});
  }

  Future<List<AppRelease>> fetchReleases(String token) async {
    final body = await _getList('/releases', token);
    return body.map(AppRelease.fromJson).toList(growable: false);
  }

  Future<AppRelease> uploadRelease({
    required String token,
    required String platform,
    required String version,
    required String releaseNotes,
    required bool mandatory,
    required String minSupportedVersion,
    required String fileName,
    required Uint8List fileBytes,
    required bool activate,
  }) async {
    final request = http.MultipartRequest('POST', _uri('/releases'));
    request.headers['Authorization'] = 'Bearer $token';
    request.fields['platform'] = platform.trim();
    request.fields['version'] = version.trim();
    request.fields['release_notes'] = releaseNotes;
    request.fields['mandatory'] = mandatory ? 'true' : 'false';
    request.fields['min_supported_version'] = minSupportedVersion.trim();
    request.fields['activate'] = activate ? 'true' : 'false';
    request.files.add(
        http.MultipartFile.fromBytes('file', fileBytes, filename: fileName));

    final response = await http.Response.fromStream(await request.send());
    final body = _decodeBody(response);
    if (response.statusCode == 401 || response.statusCode == 403) {
      throw const AdminApiException('Unauthorized', statusCode: 401);
    }
    if (response.statusCode != 201) {
      throw AdminApiException(
        _extractError(body, fallback: 'Failed to upload release'),
        statusCode: response.statusCode,
      );
    }
    return AppRelease.fromJson(body);
  }

  Future<void> activateRelease(String token, int releaseId) async {
    await _post(
        '/releases/activate', token, <String, dynamic>{'id': releaseId});
  }

  Future<List<AdminProfile>> fetchAdminUsers(String token) async {
    final body = await _getList('/admin-users', token);
    return body.map(AdminProfile.fromJson).toList(growable: false);
  }

  Future<void> createAdminUser(
      String token, Map<String, dynamic> payload) async {
    await _post('/admin-users', token, payload, expectedStatus: 201);
  }

  Future<void> updateAdminUser(
      String token, int id, Map<String, dynamic> payload) async {
    await _patch('/admin-users/$id', token, payload);
  }

  Future<List<AdminRoleModel>> fetchAdminRoles(String token) async {
    final body = await _getList('/admin-roles', token);
    return body.map(AdminRoleModel.fromJson).toList(growable: false);
  }

  Future<void> createAdminRole(
      String token, Map<String, dynamic> payload) async {
    await _post('/admin-roles', token, payload, expectedStatus: 201);
  }

  Future<void> updateAdminRole(
      String token, int id, Map<String, dynamic> payload) async {
    await _patch('/admin-roles/$id', token, payload);
  }

  Future<List<AdminPermission>> fetchAdminPermissions(String token) async {
    final body = await _getList('/admin-permissions', token);
    return body.map(AdminPermission.fromJson).toList(growable: false);
  }

  Future<Map<String, dynamic>> _getBody(String path, String token) async {
    final response = await http.get(_uri(path), headers: _authHeaders(token));
    return _expectBody(response, fallback: 'Request failed');
  }

  Future<List<Map<String, dynamic>>> _getList(String path, String token) async {
    final response = await http.get(_uri(path), headers: _authHeaders(token));
    if (response.statusCode == 401 || response.statusCode == 403) {
      throw const AdminApiException('Unauthorized', statusCode: 401);
    }
    final decoded = _decodeJsonList(response);
    if (response.statusCode != 200) {
      throw AdminApiException('Request failed',
          statusCode: response.statusCode);
    }
    return decoded;
  }

  Future<Map<String, dynamic>> _post(
      String path, String token, Map<String, dynamic> payload,
      {int expectedStatus = 200}) async {
    final response = await http.post(
      _uri(path),
      headers: _jsonAuthHeaders(token),
      body: jsonEncode(payload),
    );
    return _expectBody(response,
        expectedStatus: expectedStatus, fallback: 'Request failed');
  }

  Future<Map<String, dynamic>> _patch(
      String path, String token, Map<String, dynamic> payload) async {
    final response = await http.patch(
      _uri(path),
      headers: _jsonAuthHeaders(token),
      body: jsonEncode(payload),
    );
    return _expectBody(response, fallback: 'Request failed');
  }

  Future<Map<String, dynamic>> _delete(String path, String token) async {
    final response =
        await http.delete(_uri(path), headers: _authHeaders(token));
    return _expectBody(response, fallback: 'Request failed');
  }

  Map<String, String> _authHeaders(String token) =>
      <String, String>{'Authorization': 'Bearer $token'};

  Map<String, String> _jsonAuthHeaders(String token) => <String, String>{
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json',
      };

  Map<String, dynamic> _expectBody(http.Response response,
      {int expectedStatus = 200, required String fallback}) {
    final body = _decodeBody(response);
    if (response.statusCode == 401 || response.statusCode == 403) {
      throw const AdminApiException('Unauthorized', statusCode: 401);
    }
    if (response.statusCode != expectedStatus) {
      throw AdminApiException(
        _extractError(body, fallback: fallback),
        statusCode: response.statusCode,
      );
    }
    return body;
  }

  // ── Tenants ──────────────────────────────────────────

  Future<PagedResult<TenantModel>> fetchTenants(
    String token, {
    int page = 1,
    int pageSize = 20,
    String? search,
    String? status,
  }) async {
    final params = <String, String>{
      'page': '$page',
      'page_size': '$pageSize',
    };
    if (search != null && search.isNotEmpty) params['search'] = search;
    if (status != null && status.isNotEmpty) params['status'] = status;
    final uri = _uri('/tenants').replace(queryParameters: params);
    final response = await http.get(uri, headers: _authHeaders(token));
    final body = _expectBody(response, fallback: 'Failed to load tenants');
    final items = ((body['Items'] as List?) ?? const [])
        .whereType<Map>()
        .map((e) => TenantModel.fromJson(e.cast<String, dynamic>()))
        .toList(growable: false);
    return PagedResult<TenantModel>(
      items: items,
      total: (body['Total'] as num?)?.toInt() ?? items.length,
      page: (body['Page'] as num?)?.toInt() ?? page,
      pageSize: (body['PageSize'] as num?)?.toInt() ?? pageSize,
    );
  }

  Future<void> createTenant(
      String token, Map<String, dynamic> payload) async {
    await _post('/tenants', token, payload, expectedStatus: 201);
  }

  Future<void> updateTenant(
      String token, int id, Map<String, dynamic> payload) async {
    await _patch('/tenants/$id', token, payload);
  }

  Future<void> deleteTenant(String token, int id) async {
    await _delete('/tenants/$id', token);
  }

  // ── Settings ──────────────────────────────────────────

  Future<Map<String, String>> getSettings(String token) async {
    final response = await http.get(
      _uri('/settings'),
      headers: {'Authorization': 'Bearer $token'},
    );
    _ensureJsonResponse(response);
    if (response.statusCode != 200) {
      throw AdminApiException('Failed to load settings',
          statusCode: response.statusCode);
    }
    final decoded = jsonDecode(response.body.trim());
    if (decoded is Map) {
      return decoded.cast<String, dynamic>().map(
            (k, v) => MapEntry(k, v?.toString() ?? ''),
          );
    }
    return const <String, String>{};
  }

  Future<void> updateSetting(String token, String key, String value) async {
    final response = await http.post(
      _uri('/settings'),
      headers: {
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json',
      },
      body: jsonEncode({'key': key, 'value': value}),
    );
    if (response.statusCode != 200) {
      throw AdminApiException('Failed to save setting',
          statusCode: response.statusCode);
    }
  }

  void _ensureJsonResponse(http.Response response) {
    final raw = response.body.trim();
    if (raw.isEmpty) {
      return;
    }
    if (raw.startsWith('<')) {
      throw AdminApiException(
        'Server returned HTML instead of JSON. '
        'For local dev: 1) Start API: cd server/backend && go run ./cmd/api (port 8000). '
        '2) Run admin_web with: flutter run -d chrome --dart-define=HEYEARTH_ADMIN_API_BASE=http://127.0.0.1:8000/api/admin',
        statusCode: response.statusCode,
      );
    }
  }

  Map<String, dynamic> _decodeBody(http.Response response) {
    _ensureJsonResponse(response);
    final raw = response.body.trim();
    if (raw.isEmpty) {
      return const <String, dynamic>{};
    }
    final decoded = jsonDecode(raw);
    if (decoded is Map<String, dynamic>) {
      return decoded;
    }
    return <String, dynamic>{'message': raw};
  }

  List<Map<String, dynamic>> _decodeJsonList(http.Response response) {
    _ensureJsonResponse(response);
    final raw = response.body.trim();
    if (raw.isEmpty) {
      return const <Map<String, dynamic>>[];
    }
    final decoded = jsonDecode(raw);
    if (decoded is List) {
      return decoded
          .whereType<Map>()
          .map((item) => item.cast<String, dynamic>())
          .toList(growable: false);
    }
    if (decoded is Map) {
      final items = decoded['items'];
      if (items is List) {
        return items
            .whereType<Map>()
            .map((item) => item.cast<String, dynamic>())
            .toList(growable: false);
      }
    }
    return const <Map<String, dynamic>>[];
  }

  String _extractError(Map<String, dynamic> body, {required String fallback}) {
    final message = body['message'] ?? body['error'];
    if (message is String && message.trim().isNotEmpty) {
      return message.trim();
    }
    return fallback;
  }
}
