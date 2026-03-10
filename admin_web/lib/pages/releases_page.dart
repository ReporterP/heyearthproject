import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class ReleasesPage extends StatefulWidget {
  const ReleasesPage({super.key, required this.token});

  final String token;

  @override
  State<ReleasesPage> createState() => _ReleasesPageState();
}

class _ReleasesPageState extends State<ReleasesPage> {
  static const _platforms = <String>[
    'android',
    'windows',
    'macos',
    'linux',
    'ios'
  ];

  List<AppRelease> _releases = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      _releases = await const AdminApiClient().fetchReleases(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _upload() async {
    final picked = await FilePicker.platform.pickFiles(withData: true);
    if (picked == null || picked.files.isEmpty) {
      return;
    }
    final file = picked.files.single;
    final bytes = file.bytes;
    if (bytes == null || bytes.isEmpty) {
      _showMessage('Failed to read selected file');
      return;
    }
    final versionController = TextEditingController();
    final notesController = TextEditingController();
    String platform = _platforms.first;
    bool activate = true;

    final form = await showDialog<_ReleaseUploadData>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: const Text('Upload release'),
          content: SizedBox(
            width: 480,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('File: ${file.name}'),
                const SizedBox(height: 12),
                DropdownButtonFormField<String>(
                  initialValue: platform,
                  items: _platforms
                      .map((value) => DropdownMenuItem<String>(
                          value: value, child: Text(value)))
                      .toList(growable: false),
                  onChanged: (value) =>
                      setStateDialog(() => platform = value ?? platform),
                  decoration: const InputDecoration(labelText: 'Platform'),
                ),
                const SizedBox(height: 12),
                TextField(
                    controller: versionController,
                    decoration: const InputDecoration(labelText: 'Version')),
                const SizedBox(height: 12),
                TextField(
                    controller: notesController,
                    maxLines: 4,
                    decoration:
                        const InputDecoration(labelText: 'Release notes')),
                CheckboxListTile(
                  value: activate,
                  onChanged: (value) =>
                      setStateDialog(() => activate = value ?? false),
                  title: const Text('Activate immediately'),
                  contentPadding: EdgeInsets.zero,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Cancel')),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(
                _ReleaseUploadData(
                  platform: platform,
                  version: versionController.text.trim(),
                  releaseNotes: notesController.text.trim(),
                  activate: activate,
                  fileName: file.name,
                  fileBytes: bytes,
                ),
              ),
              child: const Text('Upload'),
            ),
          ],
        ),
      ),
    );
    versionController.dispose();
    notesController.dispose();
    if (form == null) {
      return;
    }
    try {
      await const AdminApiClient().uploadRelease(
        token: widget.token,
        platform: form.platform,
        version: form.version,
        releaseNotes: form.releaseNotes,
        fileName: form.fileName,
        fileBytes: form.fileBytes,
        activate: form.activate,
      );
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _activate(AppRelease release) async {
    try {
      await const AdminApiClient().activateRelease(widget.token, release.id);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Row(
            children: [
              Expanded(
                  child: Text('Releases',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: _upload,
                icon: const Icon(Icons.upload_file),
                label: const Text('Upload'),
              ),
            ],
          ),
          const SizedBox(height: 16),
          if (_releases.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No releases uploaded'),
              ),
            ),
          for (final release in _releases)
            Card(
              child: ListTile(
                leading: Icon(release.isActive
                    ? Icons.verified
                    : Icons.inventory_2_outlined),
                title: Text('${release.platform} ${release.version}'),
                subtitle: Text(
                    '${release.fileName}\n${release.publicPath}${release.releaseNotes.isEmpty ? '' : '\n${release.releaseNotes}'}'),
                isThreeLine: true,
                trailing: release.isActive
                    ? const Chip(label: Text('Active'))
                    : OutlinedButton(
                        onPressed: () => _activate(release),
                        child: const Text('Activate'),
                      ),
              ),
            ),
        ],
      ),
    );
  }
}

class _ReleaseUploadData {
  const _ReleaseUploadData({
    required this.platform,
    required this.version,
    required this.releaseNotes,
    required this.activate,
    required this.fileName,
    required this.fileBytes,
  });

  final String platform;
  final String version;
  final String releaseNotes;
  final bool activate;
  final String fileName;
  final Uint8List fileBytes;
}
