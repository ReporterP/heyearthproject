class AdminServer {
  const AdminServer({
    required this.id,
    required this.name,
    required this.ipAddress,
    required this.sshPort,
    required this.countryCode,
    required this.status,
  });

  final int id;
  final String name;
  final String ipAddress;
  final int sshPort;
  final String countryCode;
  final String status;

  factory AdminServer.fromJson(Map<String, dynamic> json) {
    return AdminServer(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] as String? ?? '').trim(),
      ipAddress: (json['ip_address'] as String? ?? '').trim(),
      sshPort: (json['ssh_port'] as num?)?.toInt() ?? 22,
      countryCode: (json['country_code'] as String? ?? '').trim(),
      status: (json['status'] as String? ?? 'unknown').trim(),
    );
  }
}

class AddServerPayload {
  const AddServerPayload({
    required this.name,
    required this.ipAddress,
    required this.countryCode,
    required this.sshPort,
    required this.sshUser,
  });

  final String name;
  final String ipAddress;
  final String countryCode;
  final int sshPort;
  final String sshUser;

  Map<String, dynamic> toJson() {
    return <String, dynamic>{
      'name': name,
      'ip_address': ipAddress,
      'country_code': countryCode,
      'ssh_port': sshPort,
      'ssh_user': sshUser,
    };
  }
}
