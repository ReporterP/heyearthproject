import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';

class SshKeysPage extends StatefulWidget {
  const SshKeysPage({super.key, required this.token});

  final String token;

  @override
  State<SshKeysPage> createState() => _SshKeysPageState();
}

class _SshKeysPageState extends State<SshKeysPage> {
  List<SshKey> _keys = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      _keys = await const AdminApiClient().fetchSshKeys(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _showKeyDialog({SshKey? existing}) async {
    final nameController =
        TextEditingController(text: existing?.name ?? '');
    final privateKeyController = TextEditingController();

    final result = await showDialog<Map<String, String>>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(existing == null ? AdminL10n.of(context).addSshKey : AdminL10n.of(context).editSshKey),
        content: SizedBox(
          width: 520,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextField(
                controller: nameController,
                decoration: InputDecoration(labelText: AdminL10n.of(context).fieldKeyName),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: privateKeyController,
                decoration: InputDecoration(
                  labelText: existing != null
                      ? AdminL10n.of(context).fieldPrivateKeyOptional
                      : AdminL10n.of(context).fieldPrivateKey,
                  hintText: '-----BEGIN OPENSSH PRIVATE KEY-----\n...',
                ),
                maxLines: 8,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: Text(AdminL10n.of(context).cancel)),
          FilledButton(
            onPressed: () => Navigator.of(context).pop({
              'name': nameController.text.trim(),
              'private_key': privateKeyController.text.trim(),
            }),
            child: Text(AdminL10n.of(context).save),
          ),
        ],
      ),
    );

    nameController.dispose();
    privateKeyController.dispose();

    if (result == null) return;
    if (result['name']!.isEmpty) {
      _showMessage('Name is required');
      return;
    }
    if (existing == null && result['private_key']!.isEmpty) {
      _showMessage('Private key is required');
      return;
    }
    if (existing != null && result['private_key']!.isEmpty) {
      _showMessage('Private key is required to update');
      return;
    }

    try {
      if (existing == null) {
        await const AdminApiClient()
            .createSshKey(widget.token, result['name']!, result['private_key']!);
      } else {
        await const AdminApiClient().updateSshKey(
            widget.token, existing.id, result['name']!, result['private_key']!);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteKey(SshKey key) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(AdminL10n.of(context).deleteSshKey),
        content: Text(AdminL10n.of(context).deleteSshKeyConfirm(key.name)),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: Text(AdminL10n.of(context).cancel)),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(AdminL10n.of(context).delete),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await const AdminApiClient().deleteSshKey(widget.token, key.id);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  void _copyToClipboard(String text) {
    Clipboard.setData(ClipboardData(text: text));
    _showMessage('Copied to clipboard');
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  String _fmtDate(String iso) {
    try {
      final dt = DateTime.parse(iso).toLocal();
      return '${dt.year}-${_p(dt.month)}-${_p(dt.day)}';
    } catch (_) {
      return iso;
    }
  }

  String _p(int v) => v.toString().padLeft(2, '0');

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
              Expanded(child: Text(l10n.sshKeysTitle, style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(onPressed: () => _showKeyDialog(), icon: const Icon(Icons.add), label: Text(l10n.addSshKey)),
            ]);
          }),
          const SizedBox(height: 8),
          Text(
            AdminL10n.of(context).isEn
                ? 'SSH keys are used for server operations (restart agent, reprovision). Add a key here, then assign it to a server.'
                : 'SSH ключи используются для операций с серверами (перезапуск агента, перенастройка). Добавьте ключ и назначьте его серверу.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 16),
          if (_keys.isEmpty)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(AdminL10n.of(context).noSshKeys),
              ),
            ),
          for (final key in _keys)
            Card(
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Padding(
                      padding: EdgeInsets.only(top: 2, right: 12),
                      child: Icon(Icons.key, size: 22),
                    ),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(key.name,
                              style: Theme.of(context)
                                  .textTheme
                                  .titleMedium
                                  ?.copyWith(fontWeight: FontWeight.w600)),
                          const SizedBox(height: 4),
                          Row(
                            children: [
                              Expanded(
                                child: Text(
                                  key.fingerprint.isNotEmpty
                                      ? key.fingerprint
                                      : 'No fingerprint',
                                  style: Theme.of(context)
                                      .textTheme
                                      .bodySmall
                                      ?.copyWith(fontFamily: 'monospace'),
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                              if (key.fingerprint.isNotEmpty)
                                InkWell(
                                  onTap: () =>
                                      _copyToClipboard(key.fingerprint),
                                  child: const Padding(
                                    padding:
                                        EdgeInsets.symmetric(horizontal: 4),
                                    child:
                                        Icon(Icons.copy, size: 14),
                                  ),
                                ),
                            ],
                          ),
                          if (key.publicKey.isNotEmpty) ...[
                            const SizedBox(height: 4),
                            Row(
                              children: [
                                Expanded(
                                  child: Text(
                                    key.publicKey.length > 60
                                        ? '${key.publicKey.substring(0, 60)}…'
                                        : key.publicKey,
                                    style: Theme.of(context)
                                        .textTheme
                                        .bodySmall
                                        ?.copyWith(fontFamily: 'monospace'),
                                  ),
                                ),
                                InkWell(
                                  onTap: () =>
                                      _copyToClipboard(key.publicKey),
                                  child: const Padding(
                                    padding:
                                        EdgeInsets.symmetric(horizontal: 4),
                                    child: Icon(Icons.copy, size: 14),
                                  ),
                                ),
                              ],
                            ),
                          ],
                          const SizedBox(height: 4),
                          Text(
                            'Added: ${_fmtDate(key.createdAt)}',
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 8),
                    Column(
                      children: [
                        OutlinedButton(
                          onPressed: () => _showKeyDialog(existing: key),
                          child: Text(AdminL10n.of(context).edit),
                        ),
                        const SizedBox(height: 6),
                        OutlinedButton(
                          onPressed: () => _deleteKey(key),
                          child: Text(AdminL10n.of(context).delete),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}
