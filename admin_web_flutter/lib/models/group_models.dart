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
