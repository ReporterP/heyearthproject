import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';

class ServersPage extends StatefulWidget {
  const ServersPage({super.key, required this.token});

  final String token;

  @override
  State<ServersPage> createState() => _ServersPageState();
}

class _ServersPageState extends State<ServersPage> {
  List<AdminServer> _servers = const [];
  List<GroupModel> _groups = const [];
  List<SshKey> _sshKeys = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      final client = const AdminApiClient();
      final results = await Future.wait([
        client.fetchServers(widget.token),
        client.fetchGroups(widget.token),
        client.fetchSshKeys(widget.token),
      ]);
      _servers = results[0] as List<AdminServer>;
      _groups = results[1] as List<GroupModel>;
      _sshKeys = results[2] as List<SshKey>;
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _editServer({AdminServer? existing}) async {
    AdminServer? detail = existing;
    if (existing != null) {
      try {
        detail = await const AdminApiClient()
            .fetchServerDetail(widget.token, existing.id);
      } on AdminApiException catch (e) {
        _showMessage(e.message);
        return;
      }
    }

    final nameController = TextEditingController(text: detail?.name ?? '');
    final ipController = TextEditingController(text: detail?.ipAddress ?? '');
    final portController =
        TextEditingController(text: '${detail?.sshPort ?? 22}');
    final userController =
        TextEditingController(text: detail?.sshUser ?? 'root');
    final passwordController =
        TextEditingController(text: detail?.sshPassword ?? '');
    final countryController =
        TextEditingController(text: detail?.countryCode ?? '');
    final statusController =
        TextEditingController(text: detail?.status ?? 'provisioning');
    final selectedGroupIds = <int>{...(detail?.groupIds ?? const <int>[])};
    int? selectedSshKeyId = detail?.sshKeyId;

    final payload = await showDialog<ServerPayload>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? AdminL10n.of(context).createServer : AdminL10n.of(context).editServer),
          content: SizedBox(
            width: 560,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextField(
                      controller: nameController,
                      decoration: const InputDecoration(labelText: 'Name')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: ipController,
                      decoration:
                          const InputDecoration(labelText: 'IP address')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: portController,
                      decoration:
                          const InputDecoration(labelText: 'SSH port')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: userController,
                      decoration:
                          const InputDecoration(labelText: 'SSH user')),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<int?>(
                    initialValue: selectedSshKeyId,
                    decoration: const InputDecoration(
                      labelText: 'SSH key (preferred over password)',
                    ),
                    items: [
                      const DropdownMenuItem(
                        value: null,
                        child: Text('— use password —'),
                      ),
                      ..._sshKeys.map((k) => DropdownMenuItem(
                            value: k.id,
                            child: Text('${k.name}  (${k.fingerprint.isNotEmpty ? k.fingerprint.substring(0, 20) : ""}…)'),
                          )),
                    ],
                    onChanged: (v) =>
                        setStateDialog(() => selectedSshKeyId = v),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                      controller: passwordController,
                      decoration: InputDecoration(
                        labelText: 'SSH password',
                        helperText: selectedSshKeyId != null
                            ? 'Password ignored when a key is selected'
                            : null,
                      )),
                  const SizedBox(height: 12),
                  TextField(
                      controller: countryController,
                      decoration:
                          const InputDecoration(labelText: 'Country code')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: statusController,
                      decoration:
                          const InputDecoration(labelText: 'Status')),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: _groups
                        .map(
                          (group) => FilterChip(
                            label: Text(group.name),
                            selected: selectedGroupIds.contains(group.id),
                            onSelected: (selected) {
                              setStateDialog(() {
                                if (selected) {
                                  selectedGroupIds.add(group.id);
                                } else {
                                  selectedGroupIds.remove(group.id);
                                }
                              });
                            },
                          ),
                        )
                        .toList(growable: false),
                  ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: Text(AdminL10n.of(context).cancel)),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(
                ServerPayload(
                  name: nameController.text.trim(),
                  ipAddress: ipController.text.trim(),
                  sshPort: int.tryParse(portController.text.trim()) ?? 22,
                  sshUser: userController.text.trim(),
                  sshPassword: passwordController.text.trim(),
                  sshKeyId: selectedSshKeyId,
                  countryCode: countryController.text.trim(),
                  status: statusController.text.trim(),
                  groupIds: selectedGroupIds.toList()..sort(),
                ),
              ),
              child: Text(AdminL10n.of(context).save),
            ),
          ],
        ),
      ),
    );

    nameController.dispose();
    ipController.dispose();
    portController.dispose();
    userController.dispose();
    passwordController.dispose();
    countryController.dispose();
    statusController.dispose();
    if (payload == null) {
      return;
    }

    try {
      if (existing == null) {
        await const AdminApiClient().createServer(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updateServer(widget.token, existing.id, payload);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteServer(AdminServer item) async {
    try {
      await const AdminApiClient().deleteServer(widget.token, item.id);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _runAction(AdminServer item, String action) async {
    try {
      await const AdminApiClient()
          .runServerAction(widget.token, item.id, action);
      await _load();
      _showMessage('Action completed: $action');
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
              Expanded(child: Text(l10n.serversTitle, style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(onPressed: () => _editServer(), icon: const Icon(Icons.add), label: Text(l10n.create)),
            ]);
          }),
          const SizedBox(height: 16),
          Builder(builder: (context) {
            final l10n = AdminL10n.of(context);
            if (_servers.isEmpty) {
              return Card(child: Padding(padding: const EdgeInsets.all(24), child: Text(l10n.noServers)));
            }
            return Column(children: [
              for (final item in _servers)
                Card(child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    Text(item.name, style: Theme.of(context).textTheme.titleLarge),
                    const SizedBox(height: 4),
                    Text('${item.ipAddress}:${item.sshPort} • ${item.countryCode} • ${item.status}'),
                    const SizedBox(height: 4),
                    if (item.sshKeyName.isNotEmpty)
                      Row(children: [
                        const Icon(Icons.key, size: 14),
                        const SizedBox(width: 4),
                        Text('${l10n.fieldSshKey}: ${item.sshKeyName}',
                            style: Theme.of(context).textTheme.bodySmall),
                      ])
                    else
                      Text('${l10n.isEn ? 'Auth' : 'Авторизация'}: ${l10n.serverAuthPassword}',
                          style: Theme.of(context).textTheme.bodySmall),
                    const SizedBox(height: 4),
                    Text('${l10n.navGroups}: ${item.groupIds.join(', ')}'),
                    if (item.loadMetrics.isNotEmpty)
                      Text('Metrics: ${item.loadMetrics}'),
                    const SizedBox(height: 12),
                    Wrap(spacing: 8, runSpacing: 8, children: [
                      OutlinedButton(onPressed: () => _editServer(existing: item), child: Text(l10n.edit)),
                      OutlinedButton(onPressed: () => _deleteServer(item), child: Text(l10n.delete)),
                      OutlinedButton(onPressed: () => _runAction(item, 'sync_metrics'), child: Text(l10n.serverSyncMetrics)),
                      OutlinedButton(onPressed: () => _runAction(item, 'restart_agent'), child: Text(l10n.serverRestartAgent)),
                      OutlinedButton(onPressed: () => _runAction(item, 'reprovision'), child: Text(l10n.serverReprovision)),
                    ]),
                  ]),
                )),
            ]);
          }),
        ],
      ),
    );
  }
}
