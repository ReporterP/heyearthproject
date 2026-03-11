import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'models.dart';

class AdminSession {
  const AdminSession({
    required this.token,
    this.loginResult,
  });

  final String token;
  final LoginResult? loginResult;
}

class SessionStore {
  static const _tokenKey = 'admin_token';
  static const _loginResultKey = 'admin_login_result';
  static const _themeModeKey = 'admin_theme_mode';
  static const _localeKey = 'admin_locale';

  Future<AdminSession?> load() async {
    final prefs = await SharedPreferences.getInstance();
    final token = prefs.getString(_tokenKey)?.trim();
    if (token == null || token.isEmpty) {
      return null;
    }
    final loginResultRaw = prefs.getString(_loginResultKey);
    LoginResult? loginResult;
    if (loginResultRaw != null && loginResultRaw.trim().isNotEmpty) {
      loginResult = LoginResult.fromJson(
          jsonDecode(loginResultRaw) as Map<String, dynamic>);
    }
    return AdminSession(token: token, loginResult: loginResult);
  }

  Future<void> save(AdminSession session) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_tokenKey, session.token);
    if (session.loginResult != null) {
      await prefs.setString(
          _loginResultKey,
          jsonEncode(<String, dynamic>{
            'token': session.loginResult!.token,
            'admin': <String, dynamic>{
              'id': session.loginResult!.admin.id,
              'username': session.loginResult!.admin.username,
              'display_name': session.loginResult!.admin.displayName,
              'is_active': session.loginResult!.admin.isActive,
              'roles': session.loginResult!.admin.roles,
              'permissions': session.loginResult!.admin.permissions,
              'created_at': session.loginResult!.admin.createdAt,
              'updated_at': session.loginResult!.admin.updatedAt,
            },
            'roles': session.loginResult!.roles
                .map((role) => <String, dynamic>{
                      'id': role.id,
                      'role_key': role.roleKey,
                      'name': role.name,
                      'description': role.description,
                      'is_system': role.isSystem,
                      'permissions': role.permissions,
                    })
                .toList(growable: false),
            'permissions': session.loginResult!.permissions,
          }));
    }
  }

  Future<void> clear() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_tokenKey);
    await prefs.remove(_loginResultKey);
  }

  Future<ThemeMode> loadThemeMode() async {
    final prefs = await SharedPreferences.getInstance();
    switch (prefs.getString(_themeModeKey)) {
      case 'light':
        return ThemeMode.light;
      case 'dark':
        return ThemeMode.dark;
      default:
        return ThemeMode.system;
    }
  }

  Future<String> loadLocale() async {
    final prefs = await SharedPreferences.getInstance();
    final v = prefs.getString(_localeKey);
    return (v == 'ru' || v == 'en') ? v! : 'en';
  }

  Future<void> saveLocale(String locale) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_localeKey, locale);
  }

  Future<void> saveThemeMode(ThemeMode mode) async {
    final prefs = await SharedPreferences.getInstance();
    final value = switch (mode) {
      ThemeMode.light => 'light',
      ThemeMode.dark => 'dark',
      ThemeMode.system => 'system',
    };
    await prefs.setString(_themeModeKey, value);
  }
}
