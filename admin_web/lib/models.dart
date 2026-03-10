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

class AppRelease {
  const AppRelease({
    required this.id,
    required this.platform,
    required this.version,
    required this.releaseNotes,
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
