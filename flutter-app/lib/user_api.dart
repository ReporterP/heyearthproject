import 'dart:convert';
import 'package:http/http.dart' as http;

/// REST API client for the HeYearth user portal.
class UserApi {
  UserApi(this.apiBase);

  final String apiBase;

  Uri _uri(String path) =>
      Uri.parse('${apiBase.replaceFirst(RegExp(r'/$'), '')}$path');

  Map<String, String> _headers({String? token}) {
    final h = <String, String>{'Content-Type': 'application/json'};
    if (token != null && token.isNotEmpty) {
      h['Authorization'] = 'Bearer $token';
    }
    return h;
  }

  /// Register a new anonymous user.
  /// Returns `{ "user_id": "...", "token": "..." }`.
  Future<Map<String, dynamic>> register({
    required String platform,
    required String deviceId,
  }) async {
    final resp = await http.post(
      _uri('/api/v1/auth/register'),
      headers: _headers(),
      body: jsonEncode({
        'platform': platform,
        'device_id': deviceId,
      }),
    );
    return _parseResponse(resp);
  }

  /// Login by HeYearth ID or username, with optional password.
  /// Returns `{ "user_id": "...", "token": "..." }`.
  Future<Map<String, dynamic>> login({
    required String idOrUsername,
    String? password,
  }) async {
    final body = <String, dynamic>{'id_or_username': idOrUsername};
    if (password != null && password.isNotEmpty) {
      body['password'] = password;
    }
    final resp = await http.post(
      _uri('/api/v1/auth/login'),
      headers: _headers(),
      body: jsonEncode(body),
    );
    return _parseResponse(resp);
  }

  /// Get the current user profile.
  Future<Map<String, dynamic>> getProfile({required String token}) async {
    final resp = await http.get(
      _uri('/api/v1/user/profile'),
      headers: _headers(token: token),
    );
    return _parseResponse(resp);
  }

  /// Update user profile (username and/or cloud password).
  Future<Map<String, dynamic>> updateProfile({
    required String token,
    String? username,
    String? newPassword,
    String? currentPassword,
  }) async {
    final body = <String, dynamic>{};
    if (username != null) body['username'] = username;
    if (newPassword != null) body['new_password'] = newPassword;
    if (currentPassword != null) body['current_password'] = currentPassword;
    final resp = await http.patch(
      _uri('/api/v1/user/profile'),
      headers: _headers(token: token),
      body: jsonEncode(body),
    );
    return _parseResponse(resp);
  }

  /// Get connection options for the given platform.
  Future<Map<String, dynamic>> getConnections({
    required String token,
    required String platform,
  }) async {
    final resp = await http.get(
      _uri('/api/v1/user/connections?platform=$platform'),
      headers: _headers(token: token),
    );
    return _parseResponse(resp);
  }

  /// Get download links for all platforms.
  Future<Map<String, dynamic>> getDownloads() async {
    final resp = await http.get(
      _uri('/api/v1/user/downloads'),
      headers: _headers(),
    );
    return _parseResponse(resp);
  }

  Map<String, dynamic> _parseResponse(http.Response resp) {
    final body = jsonDecode(resp.body) as Map<String, dynamic>;
    if (resp.statusCode >= 400) {
      throw Exception(body['error'] ?? body['message'] ?? 'Request failed');
    }
    return body;
  }
}
