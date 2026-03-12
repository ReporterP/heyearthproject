class SshKey {
  const SshKey({
    required this.id,
    required this.name,
    required this.publicKey,
    required this.fingerprint,
    required this.createdAt,
    required this.updatedAt,
  });

  final int id;
  final String name;
  final String publicKey;
  final String fingerprint;
  final String createdAt;
  final String updatedAt;

  factory SshKey.fromJson(Map<String, dynamic> json) {
    return SshKey(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] as String? ?? '').trim(),
      publicKey: (json['public_key'] as String? ?? '').trim(),
      fingerprint: (json['fingerprint'] as String? ?? '').trim(),
      createdAt: (json['created_at'] as String? ?? '').trim(),
      updatedAt: (json['updated_at'] as String? ?? '').trim(),
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
    this.sshKeyId,
    required this.sshKeyName,
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
  final int? sshKeyId;
  final String sshKeyName;
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
      sshKeyId: (json['ssh_key_id'] as num?)?.toInt(),
      sshKeyName: (json['ssh_key_name'] as String? ?? '').trim(),
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
    this.sshKeyId,
    required this.countryCode,
    required this.status,
    required this.groupIds,
  });

  final String name;
  final String ipAddress;
  final int sshPort;
  final String sshUser;
  final String sshPassword;
  final int? sshKeyId;
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
      if (sshKeyId != null) 'ssh_key_id': sshKeyId,
      'country_code': countryCode,
      'status': status,
      'group_ids': groupIds,
    };
  }
}
