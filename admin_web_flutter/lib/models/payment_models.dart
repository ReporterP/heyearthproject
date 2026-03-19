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
