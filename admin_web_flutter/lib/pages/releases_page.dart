import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
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
    final minSupportedController = TextEditingController();
    String platform = _platforms.first;
    bool activate = true;
    bool mandatory = false;

    final form = await showDialog<_ReleaseUploadData>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(AdminL10n.of(context).uploadRelease),
          content: SizedBox(
            width: 480,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('${AdminL10n.of(context).fieldApkFile}: ${file.name}'),
                const SizedBox(height: 12),
                DropdownButtonFormField<String>(
                  initialValue: platform,
                  items: _platforms
                      .map((value) => DropdownMenuItem<String>(
                          value: value, child: Text(value)))
                      .toList(growable: false),
                  onChanged: (value) =>
                      setStateDialog(() => platform = value ?? platform),
                  decoration: InputDecoration(labelText: AdminL10n.of(context).fieldPlatform),
                ),
                const SizedBox(height: 12),
                TextField(
                    controller: versionController,
                    decoration: InputDecoration(labelText: AdminL10n.of(context).fieldVersion)),
                const SizedBox(height: 12),
                TextField(
                    controller: notesController,
                    maxLines: 4,
                    decoration: InputDecoration(labelText: AdminL10n.of(context).fieldReleaseNotes)),
                const SizedBox(height: 12),
                TextField(
                  controller: minSupportedController,
                  decoration: InputDecoration(
                    labelText: AdminL10n.of(context).fieldMinSupportedVersion,
                    hintText: 'Optional, e.g. 1.0.5',
                  ),
                ),
                CheckboxListTile(
                  value: mandatory,
                  onChanged: (value) =>
                      setStateDialog(() => mandatory = value ?? false),
                  title: Text(AdminL10n.of(context).fieldMandatory),
                  contentPadding: EdgeInsets.zero,
                ),
                CheckboxListTile(
                  value: activate,
                  onChanged: (value) =>
                      setStateDialog(() => activate = value ?? false),
                  title: Text(AdminL10n.of(context).isEn ? 'Activate immediately' : 'Активировать сразу'),
                  contentPadding: EdgeInsets.zero,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: Text(AdminL10n.of(context).cancel)),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(
                _ReleaseUploadData(
                  platform: platform,
                  version: versionController.text.trim(),
                  releaseNotes: notesController.text.trim(),
                  mandatory: mandatory,
                  minSupportedVersion: minSupportedController.text.trim(),
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
    minSupportedController.dispose();
    if (form == null) {
      return;
    }
    try {
      await const AdminApiClient().uploadRelease(
        token: widget.token,
        platform: form.platform,
        version: form.version,
        releaseNotes: form.releaseNotes,
        mandatory: form.mandatory,
        minSupportedVersion: form.minSupportedVersion,
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
          Builder(builder: (context) {
            final l10n = AdminL10n.of(context);
            return Row(children: [
              Expanded(child: Text(l10n.releasesTitle, style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(onPressed: _upload, icon: const Icon(Icons.upload_file), label: Text(l10n.upload)),
            ]);
          }),
          const SizedBox(height: 16),
          Builder(builder: (context) {
            final l10n = AdminL10n.of(context);
            if (_releases.isEmpty) {
              return Card(child: Padding(padding: const EdgeInsets.all(24), child: Text(l10n.noReleases)));
            }
            return Column(children: [
              for (final release in _releases)
                Card(child: ListTile(
                  leading: Icon(release.isActive ? Icons.verified : Icons.inventory_2_outlined),
                  title: Text('${release.platform} ${release.version}'),
                  subtitle: Text('${release.fileName}\n${release.publicPath}'
                      '${release.mandatory ? '\n${l10n.fieldMandatory}' : ''}'
                      '${release.minSupportedVersion.isEmpty ? '' : '\n${l10n.fieldMinSupportedVersion}: ${release.minSupportedVersion}'}'
                      '${release.releaseNotes.isEmpty ? '' : '\n${release.releaseNotes}'}'),
                  isThreeLine: true,
                  trailing: release.isActive
                      ? Chip(label: Text(l10n.releaseActive))
                      : OutlinedButton(onPressed: () => _activate(release), child: Text(l10n.activate)),
                )),
            ]);
          }),
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
    required this.mandatory,
    required this.minSupportedVersion,
    required this.activate,
    required this.fileName,
    required this.fileBytes,
  });

  final String platform;
  final String version;
  final String releaseNotes;
  final bool mandatory;
  final String minSupportedVersion;
  final bool activate;
  final String fileName;
  final Uint8List fileBytes;
}
