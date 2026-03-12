class AppRelease {
  const AppRelease({
    required this.id,
    required this.platform,
    required this.version,
    required this.releaseNotes,
    required this.mandatory,
    required this.minSupportedVersion,
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
  final bool mandatory;
  final String minSupportedVersion;
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
      mandatory: json['mandatory'] as bool? ?? false,
      minSupportedVersion:
          (json['min_supported_version'] as String? ?? '').trim(),
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
