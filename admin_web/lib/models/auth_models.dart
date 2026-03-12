class LoginResult {
  const LoginResult({
    required this.token,
    required this.admin,
    required this.roles,
    required this.permissions,
  });

  final String token;
  final AdminProfile admin;
  final List<AdminRoleModel> roles;
  final List<String> permissions;

  factory LoginResult.fromJson(Map<String, dynamic> json) {
    return LoginResult(
      token: (json['token'] as String? ?? '').trim(),
      admin: AdminProfile.fromJson(
          (json['admin'] as Map?)?.cast<String, dynamic>() ??
              const <String, dynamic>{}),
      roles: ((json['roles'] as List?) ?? const <dynamic>[])
          .whereType<Map>()
          .map((item) => AdminRoleModel.fromJson(item.cast<String, dynamic>()))
          .toList(growable: false),
      permissions: ((json['permissions'] as List?) ?? const <dynamic>[])
          .whereType<String>()
          .map((item) => item.trim())
          .where((item) => item.isNotEmpty)
          .toList(growable: false),
    );
  }
}

class AdminProfile {
  const AdminProfile({
    required this.id,
    required this.username,
    required this.displayName,
    required this.isActive,
    required this.roles,
    required this.permissions,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String username;
  final String displayName;
  final bool isActive;
  final List<String> roles;
  final List<String> permissions;
  final String createdAt;
  final String updatedAt;

  factory AdminProfile.fromJson(Map<String, dynamic> json) {
    return AdminProfile(
      id: (json['id'] as num?)?.toInt() ?? 0,
      username: (json['username'] as String? ?? '').trim(),
      displayName: (json['display_name'] as String? ?? '').trim(),
      isActive: json['is_active'] as bool? ?? false,
      roles: ((json['roles'] as List?) ?? const <dynamic>[])
          .whereType<String>()
          .map((item) => item.trim())
          .toList(growable: false),
      permissions: ((json['permissions'] as List?) ?? const <dynamic>[])
          .whereType<String>()
          .map((item) => item.trim())
          .toList(growable: false),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class AdminPermission {
  const AdminPermission({
    required this.key,
    required this.name,
    required this.description,
  });

  final String key;
  final String name;
  final String description;

  factory AdminPermission.fromJson(Map<String, dynamic> json) {
    return AdminPermission(
      key: (json['key'] as String? ?? '').trim(),
      name: (json['name'] as String? ?? '').trim(),
      description: (json['description'] as String? ?? '').trim(),
    );
  }
}

class AdminRoleModel {
  const AdminRoleModel({
    required this.id,
    required this.roleKey,
    required this.name,
    required this.description,
    required this.isSystem,
    required this.permissions,
  });

  final int id;
  final String roleKey;
  final String name;
  final String description;
  final bool isSystem;
  final List<String> permissions;

  factory AdminRoleModel.fromJson(Map<String, dynamic> json) {
    return AdminRoleModel(
      id: (json['id'] as num?)?.toInt() ?? 0,
      roleKey: (json['role_key'] as String? ?? '').trim(),
      name: (json['name'] as String? ?? '').trim(),
      description: (json['description'] as String? ?? '').trim(),
      isSystem: json['is_system'] as bool? ?? false,
      permissions: ((json['permissions'] as List?) ?? const <dynamic>[])
          .whereType<String>()
          .map((item) => item.trim())
          .toList(growable: false),
    );
  }
}
