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
