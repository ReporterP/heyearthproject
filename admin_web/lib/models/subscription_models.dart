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
