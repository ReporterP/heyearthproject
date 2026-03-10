import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';

class AdminApiException implements Exception {
  const AdminApiException(this.message, {this.statusCode});

  final String message;
  final int? statusCode;

  @override
  String toString() => message;
}

class AdminApiClient {
  const AdminApiClient(this.apiBase);

  final String apiBase;

  Uri _uri(String path) {
    final base = apiBase.trim().replaceFirst(RegExp(r'/$'), '');
    if (base.isEmpty) {
      throw const AdminApiException('API base is empty');
    }
    if (base.startsWith('http://') || base.startsWith('https://')) {
      return Uri.parse('$base$path');
    }
    return Uri.parse('$base$path');
  }

  Future<String> login({
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
    final token = (body['token'] as String? ?? '').trim();
    if (token.isEmpty) {
      throw const AdminApiException('Server returned an empty token');
    }
    return token;
  }

  Future<List<AdminServer>> fetchServers(String token) async {
    final response = await http.get(
      _uri('/servers'),
      headers: <String, String>{'Authorization': 'Bearer $token'},
    );
    if (response.statusCode == 401 || response.statusCode == 403) {
      throw const AdminApiException('Unauthorized', statusCode: 401);
    }
    final body = _decodeJsonList(response);
    if (response.statusCode != 200) {
      throw AdminApiException('Failed to load servers', statusCode: response.statusCode);
    }
    return body
        .whereType<Map<String, dynamic>>()
        .map(AdminServer.fromJson)
        .toList(growable: false);
  }

  Future<void> addServer(String token, AddServerPayload payload) async {
    final response = await http.post(
      _uri('/servers'),
      headers: <String, String>{
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json',
      },
      body: jsonEncode(payload.toJson()),
    );
    if (response.statusCode == 401 || response.statusCode == 403) {
      throw const AdminApiException('Unauthorized', statusCode: 401);
    }
    if (response.statusCode != 201) {
      final body = _decodeBody(response);
      throw AdminApiException(
        _extractError(body, fallback: 'Failed to add server'),
        statusCode: response.statusCode,
      );
    }
  }

  Map<String, dynamic> _decodeBody(http.Response response) {
    final raw = response.body.trim();
    if (raw.isEmpty) {
      return <String, dynamic>{};
    }
    final decoded = jsonDecode(raw);
    if (decoded is Map<String, dynamic>) {
      return decoded;
    }
    return <String, dynamic>{'message': raw};
  }

  List<dynamic> _decodeJsonList(http.Response response) {
    final raw = response.body.trim();
    if (raw.isEmpty) {
      return const <dynamic>[];
    }
    final decoded = jsonDecode(raw);
    return decoded is List ? decoded : const <dynamic>[];
  }

  String _extractError(Map<String, dynamic> body, {required String fallback}) {
    final message = body['message'] ?? body['error'];
    if (message is String && message.trim().isNotEmpty) {
      return message.trim();
    }
    return fallback;
  }
}
