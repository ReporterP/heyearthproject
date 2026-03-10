import 'package:shared_preferences/shared_preferences.dart';

class AdminSession {
  const AdminSession({
    required this.token,
    required this.apiBase,
  });

  final String token;
  final String apiBase;
}

class SessionStore {
  static const _tokenKey = 'admin_token';
  static const _apiBaseKey = 'admin_api_base';

  Future<String?> loadApiBase() async {
    final prefs = await SharedPreferences.getInstance();
    final value = prefs.getString(_apiBaseKey)?.trim();
    if (value == null || value.isEmpty) {
      return null;
    }
    return value;
  }

  Future<AdminSession?> load() async {
    final prefs = await SharedPreferences.getInstance();
    final token = prefs.getString(_tokenKey);
    final apiBase = prefs.getString(_apiBaseKey);
    if (token == null || token.trim().isEmpty) {
      return null;
    }
    return AdminSession(
      token: token.trim(),
      apiBase: (apiBase ?? '').trim(),
    );
  }

  Future<void> save(AdminSession session) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_tokenKey, session.token);
    await prefs.setString(_apiBaseKey, session.apiBase);
  }

  Future<void> clear() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_tokenKey);
  }
}
