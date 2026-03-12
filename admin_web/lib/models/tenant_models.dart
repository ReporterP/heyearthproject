class TenantModel {
  const TenantModel({
    required this.id,
    required this.name,
    required this.slug,
    required this.status,
    required this.plan,
    required this.contactEmail,
    required this.billingNotes,
    required this.maxUsers,
    required this.maxServers,
    required this.useProviderServers,
    required this.balance,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String name;
  final String slug;
  final String status;
  final String plan;
  final String contactEmail;
  final String billingNotes;
  final int maxUsers;
  final int maxServers;
  final bool useProviderServers;
  final double balance;
  final String createdAt;
  final String updatedAt;

  factory TenantModel.fromJson(Map<String, dynamic> json) {
    return TenantModel(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] as String? ?? '').trim(),
      slug: (json['slug'] as String? ?? '').trim(),
      status: (json['status'] as String? ?? 'inactive').trim(),
      plan: (json['plan'] as String? ?? 'basic').trim(),
      contactEmail: (json['contact_email'] as String? ?? '').trim(),
      billingNotes: (json['billing_notes'] as String? ?? '').trim(),
      maxUsers: (json['max_users'] as num?)?.toInt() ?? 0,
      maxServers: (json['max_servers'] as num?)?.toInt() ?? 5,
      useProviderServers: json['use_provider_servers'] as bool? ?? false,
      balance: (json['balance'] as num?)?.toDouble() ?? 0,
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
    );
  }
}

class TenantClaimCode {
  const TenantClaimCode({
    required this.id,
    required this.tenantId,
    required this.code,
    required this.createdBy,
    required this.maxUses,
    required this.usedCount,
    required this.expiresAt,
    required this.createdAt,
  });

  final int id;
  final int tenantId;
  final String code;
  final int? createdBy;
  final int maxUses;
  final int usedCount;
  final String expiresAt;
  final String createdAt;

  factory TenantClaimCode.fromJson(Map<String, dynamic> json) {
    return TenantClaimCode(
      id: (json['id'] as num?)?.toInt() ?? 0,
      tenantId: (json['tenant_id'] as num?)?.toInt() ?? 0,
      code: (json['code'] as String? ?? '').trim(),
      createdBy: (json['created_by'] as num?)?.toInt(),
      maxUses: (json['max_uses'] as num?)?.toInt() ?? 0,
      usedCount: (json['used_count'] as num?)?.toInt() ?? 0,
      expiresAt: (json['expires_at'] as String? ?? '').trim(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
    );
  }
}
