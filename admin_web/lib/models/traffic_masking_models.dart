class TrafficMaskingRule {
  const TrafficMaskingRule({
    required this.id,
    required this.sourceDomain,
    required this.maskDomain,
    required this.isActive,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String sourceDomain;
  final String maskDomain;
  final bool isActive;
  final String createdAt;
  final String updatedAt;

  factory TrafficMaskingRule.fromJson(Map<String, dynamic> json) {
    return TrafficMaskingRule(
      id: (json['id'] as num?)?.toInt() ?? 0,
      sourceDomain: (json['source_domain'] as String? ?? '').trim(),
      maskDomain: (json['mask_domain'] as String? ?? '').trim(),
      isActive: json['is_active'] as bool? ?? false,
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }

  Map<String, dynamic> toJson() {
    return <String, dynamic>{
      'id': id,
      'source_domain': sourceDomain,
      'mask_domain': maskDomain,
      'is_active': isActive,
      'created_at': createdAt,
      'updated_at': updatedAt,
    };
  }
}
