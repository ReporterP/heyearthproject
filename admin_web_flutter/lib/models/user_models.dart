class ManagedUser {
  const ManagedUser({
    required this.id,
    required this.userId,
    required this.telegramId,
    required this.username,
    required this.role,
    required this.isBlocked,
    required this.preferredPlatform,
    required this.currentPlan,
    required this.validUntil,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String userId;
  final int? telegramId;
  final String username;
  final String role;
  final bool isBlocked;
  final String preferredPlatform;
  final String currentPlan;
  final String validUntil;
  final String createdAt;
  final String updatedAt;

  factory ManagedUser.fromJson(Map<String, dynamic> json) {
    return ManagedUser(
      id: (json['id'] as num?)?.toInt() ?? 0,
      userId: (json['user_id'] as String? ?? '').trim(),
      telegramId: (json['telegram_id'] as num?)?.toInt(),
      username: (json['username'] as String? ?? '').trim(),
      role: (json['role'] as String? ?? '').trim(),
      isBlocked: json['is_blocked'] as bool? ?? false,
      preferredPlatform: (json['preferred_platform'] as String? ?? '').trim(),
      currentPlan: (json['current_plan'] as String? ?? '').trim(),
      validUntil: (json['valid_until'] as String? ?? '').trim(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class UserDevice {
  const UserDevice({
    required this.id,
    required this.platform,
    required this.deviceId,
    required this.deviceLabel,
    required this.linkedVia,
    required this.createdAt,
    required this.lastSeenAt,
  });

  final int id;
  final String platform;
  final String deviceId;
  final String deviceLabel;
  final String linkedVia;
  final String createdAt;
  final String lastSeenAt;

  factory UserDevice.fromJson(Map<String, dynamic> json) {
    return UserDevice(
      id: (json['id'] as num?)?.toInt() ?? 0,
      platform: (json['platform'] as String? ?? '').trim(),
      deviceId: (json['device_id'] as String? ?? '').trim(),
      deviceLabel: (json['device_label'] as String? ?? '').trim(),
      linkedVia: (json['linked_via'] as String? ?? '').trim(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      lastSeenAt: (json['last_seen_at'] as String? ?? '').trim(),
    );
  }
}
