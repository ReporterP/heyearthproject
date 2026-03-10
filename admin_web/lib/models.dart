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

class DashboardOverview {
  const DashboardOverview({
    required this.usersCount,
    required this.activeUsersCount,
    required this.serversCount,
    required this.onlineServersCount,
    required this.groupsCount,
    required this.promocodesCount,
    required this.paymentsCount,
    required this.activeReleases,
    required this.adminsCount,
  });

  final int usersCount;
  final int activeUsersCount;
  final int serversCount;
  final int onlineServersCount;
  final int groupsCount;
  final int promocodesCount;
  final int paymentsCount;
  final int activeReleases;
  final int adminsCount;

  factory DashboardOverview.fromJson(Map<String, dynamic> json) {
    return DashboardOverview(
      usersCount: (json['users_count'] as num?)?.toInt() ?? 0,
      activeUsersCount: (json['active_users_count'] as num?)?.toInt() ?? 0,
      serversCount: (json['servers_count'] as num?)?.toInt() ?? 0,
      onlineServersCount: (json['online_servers_count'] as num?)?.toInt() ?? 0,
      groupsCount: (json['groups_count'] as num?)?.toInt() ?? 0,
      promocodesCount: (json['promocodes_count'] as num?)?.toInt() ?? 0,
      paymentsCount: (json['payments_count'] as num?)?.toInt() ?? 0,
      activeReleases: (json['active_releases'] as num?)?.toInt() ?? 0,
      adminsCount: (json['admins_count'] as num?)?.toInt() ?? 0,
    );
  }
}

class ManagedUser {
  const ManagedUser({
    required this.id,
    required this.userId,
    required this.telegramId,
    required this.role,
    required this.isBlocked,
    required this.currentPlan,
    required this.validUntil,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String userId;
  final int? telegramId;
  final String role;
  final bool isBlocked;
  final String currentPlan;
  final String validUntil;
  final String createdAt;
  final String updatedAt;

  factory ManagedUser.fromJson(Map<String, dynamic> json) {
    return ManagedUser(
      id: (json['id'] as num?)?.toInt() ?? 0,
      userId: (json['user_id'] as String? ?? '').trim(),
      telegramId: (json['telegram_id'] as num?)?.toInt(),
      role: (json['role'] as String? ?? '').trim(),
      isBlocked: json['is_blocked'] as bool? ?? false,
      currentPlan: (json['current_plan'] as String? ?? '').trim(),
      validUntil: (json['valid_until'] as String? ?? '').trim(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class GroupModel {
  const GroupModel({
    required this.id,
    required this.name,
    required this.description,
    required this.isPremium,
    required this.companyId,
    required this.updatedAt,
  });

  final int id;
  final String name;
  final String description;
  final bool isPremium;
  final int? companyId;
  final String updatedAt;

  factory GroupModel.fromJson(Map<String, dynamic> json) {
    return GroupModel(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] as String? ?? '').trim(),
      description: (json['description'] as String? ?? '').trim(),
      isPremium: json['is_premium'] as bool? ?? false,
      companyId: (json['company_id'] as num?)?.toInt(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class SubscriptionModel {
  const SubscriptionModel({
    required this.userDbId,
    required this.userId,
    required this.groupId,
    required this.groupName,
    required this.validUntil,
    required this.autoRenew,
  });

  final int userDbId;
  final String userId;
  final int groupId;
  final String groupName;
  final String validUntil;
  final bool autoRenew;

  factory SubscriptionModel.fromJson(Map<String, dynamic> json) {
    return SubscriptionModel(
      userDbId: (json['user_db_id'] as num?)?.toInt() ?? 0,
      userId: (json['user_id'] as String? ?? '').trim(),
      groupId: (json['group_id'] as num?)?.toInt() ?? 0,
      groupName: (json['group_name'] as String? ?? '').trim(),
      validUntil: (json['valid_until'] as String? ?? '').trim(),
      autoRenew: json['auto_renew'] as bool? ?? false,
    );
  }
}

class PromocodeModel {
  const PromocodeModel({
    required this.code,
    required this.groupIdGranted,
    required this.groupName,
    required this.durationDays,
    required this.maxUses,
    required this.currentUses,
    required this.expiresAt,
    required this.createdAt,
    required this.updatedAt,
  });

  final String code;
  final int groupIdGranted;
  final String groupName;
  final int durationDays;
  final int maxUses;
  final int currentUses;
  final String expiresAt;
  final String createdAt;
  final String updatedAt;

  factory PromocodeModel.fromJson(Map<String, dynamic> json) {
    return PromocodeModel(
      code: (json['code'] as String? ?? '').trim(),
      groupIdGranted: (json['group_id_granted'] as num?)?.toInt() ?? 0,
      groupName: (json['group_name'] as String? ?? '').trim(),
      durationDays: (json['duration_days'] as num?)?.toInt() ?? 0,
      maxUses: (json['max_uses'] as num?)?.toInt() ?? 0,
      currentUses: (json['current_uses'] as num?)?.toInt() ?? 0,
      expiresAt: (json['expires_at'] as String? ?? '').trim(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class PaymentModel {
  const PaymentModel({
    required this.id,
    required this.userDbId,
    required this.userId,
    required this.amount,
    required this.currency,
    required this.status,
    required this.provider,
    required this.externalId,
    required this.notes,
    required this.metadata,
    required this.createdAt,
    required this.updatedAt,
    required this.confirmedAt,
  });

  final int id;
  final int userDbId;
  final String userId;
  final double amount;
  final String currency;
  final String status;
  final String provider;
  final String externalId;
  final String notes;
  final Map<String, dynamic> metadata;
  final String createdAt;
  final String updatedAt;
  final String confirmedAt;

  factory PaymentModel.fromJson(Map<String, dynamic> json) {
    return PaymentModel(
      id: (json['id'] as num?)?.toInt() ?? 0,
      userDbId: (json['user_db_id'] as num?)?.toInt() ?? 0,
      userId: (json['user_id'] as String? ?? '').trim(),
      amount: (json['amount'] as num?)?.toDouble() ?? 0,
      currency: (json['currency'] as String? ?? '').trim(),
      status: (json['status'] as String? ?? '').trim(),
      provider: (json['provider'] as String? ?? '').trim(),
      externalId: (json['external_id'] as String? ?? '').trim(),
      notes: (json['notes'] as String? ?? '').trim(),
      metadata: ((json['metadata'] as Map?) ?? const <String, dynamic>{})
          .cast<String, dynamic>(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
      confirmedAt: (json['confirmed_at'] as String? ?? '').trim(),
    );
  }
}

class AdminServer {
  const AdminServer({
    required this.id,
    required this.name,
    required this.ipAddress,
    required this.sshPort,
    required this.sshUser,
    required this.sshPassword,
    required this.countryCode,
    required this.status,
    required this.loadMetrics,
    required this.groupIds,
    required this.updatedAt,
  });

  final int id;
  final String name;
  final String ipAddress;
  final int sshPort;
  final String sshUser;
  final String sshPassword;
  final String countryCode;
  final String status;
  final Map<String, dynamic> loadMetrics;
  final List<int> groupIds;
  final String updatedAt;

  factory AdminServer.fromJson(Map<String, dynamic> json) {
    return AdminServer(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] as String? ?? '').trim(),
      ipAddress: (json['ip_address'] as String? ?? '').trim(),
      sshPort: (json['ssh_port'] as num?)?.toInt() ?? 22,
      sshUser: (json['ssh_user'] as String? ?? '').trim(),
      sshPassword: (json['ssh_password'] as String? ?? '').trim(),
      countryCode: (json['country_code'] as String? ?? '').trim(),
      status: (json['status'] as String? ?? '').trim(),
      loadMetrics: ((json['load_metrics'] as Map?) ?? const <String, dynamic>{})
          .cast<String, dynamic>(),
      groupIds: ((json['group_ids'] as List?) ?? const <dynamic>[])
          .whereType<num>()
          .map((item) => item.toInt())
          .toList(growable: false),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class ServerPayload {
  const ServerPayload({
    required this.name,
    required this.ipAddress,
    required this.sshPort,
    required this.sshUser,
    required this.sshPassword,
    required this.countryCode,
    required this.status,
    required this.groupIds,
  });

  final String name;
  final String ipAddress;
  final int sshPort;
  final String sshUser;
  final String sshPassword;
  final String countryCode;
  final String status;
  final List<int> groupIds;

  Map<String, dynamic> toJson() {
    return <String, dynamic>{
      'name': name,
      'ip_address': ipAddress,
      'ssh_port': sshPort,
      'ssh_user': sshUser,
      'ssh_password': sshPassword,
      'country_code': countryCode,
      'status': status,
      'group_ids': groupIds,
    };
  }
}

class AppRelease {
  const AppRelease({
    required this.id,
    required this.platform,
    required this.version,
    required this.releaseNotes,
    required this.mandatory,
    required this.minSupportedVersion,
    required this.fileName,
    required this.publicPath,
    required this.contentType,
    required this.fileSize,
    required this.isActive,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String platform;
  final String version;
  final String releaseNotes;
  final bool mandatory;
  final String minSupportedVersion;
  final String fileName;
  final String publicPath;
  final String contentType;
  final int fileSize;
  final bool isActive;
  final String createdAt;
  final String updatedAt;

  factory AppRelease.fromJson(Map<String, dynamic> json) {
    return AppRelease(
      id: (json['id'] as num?)?.toInt() ?? 0,
      platform: (json['platform'] as String? ?? '').trim(),
      version: (json['version'] as String? ?? '').trim(),
      releaseNotes: (json['release_notes'] as String? ?? '').trim(),
      mandatory: json['mandatory'] as bool? ?? false,
      minSupportedVersion:
          (json['min_supported_version'] as String? ?? '').trim(),
      fileName: (json['file_name'] as String? ?? '').trim(),
      publicPath: (json['public_path'] as String? ?? '').trim(),
      contentType: (json['content_type'] as String? ?? '').trim(),
      fileSize: (json['file_size'] as num?)?.toInt() ?? 0,
      isActive: json['is_active'] as bool? ?? false,
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}
