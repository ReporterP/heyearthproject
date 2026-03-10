import 'package:flutter/material.dart';

import '../admin_api_client.dart';
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
      _servers = await client.fetchServers(widget.token);
      _groups = await client.fetchGroups(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
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

    final payload = await showDialog<ServerPayload>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create server' : 'Edit server'),
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
                      decoration: const InputDecoration(labelText: 'SSH port')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: userController,
                      decoration: const InputDecoration(labelText: 'SSH user')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: passwordController,
                      decoration:
                          const InputDecoration(labelText: 'SSH password')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: countryController,
                      decoration:
                          const InputDecoration(labelText: 'Country code')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: statusController,
                      decoration: const InputDecoration(labelText: 'Status')),
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
                child: const Text('Cancel')),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(
                ServerPayload(
                  name: nameController.text.trim(),
                  ipAddress: ipController.text.trim(),
                  sshPort: int.tryParse(portController.text.trim()) ?? 22,
                  sshUser: userController.text.trim(),
                  sshPassword: passwordController.text.trim(),
                  countryCode: countryController.text.trim(),
                  status: statusController.text.trim(),
                  groupIds: selectedGroupIds.toList()..sort(),
                ),
              ),
              child: const Text('Save'),
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
          Row(
            children: [
              Expanded(
                  child: Text('Servers',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                  onPressed: () => _editServer(),
                  icon: const Icon(Icons.add),
                  label: const Text('Create')),
            ],
          ),
          const SizedBox(height: 16),
          if (_servers.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No servers found'),
              ),
            ),
          for (final item in _servers)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(item.name,
                        style: Theme.of(context).textTheme.titleLarge),
                    const SizedBox(height: 4),
                    Text(
                        '${item.ipAddress}:${item.sshPort} • ${item.countryCode} • ${item.status}'),
                    const SizedBox(height: 8),
                    Text('Groups: ${item.groupIds.join(', ')}'),
                    if (item.loadMetrics.isNotEmpty)
                      Text('Metrics: ${item.loadMetrics}'),
                    const SizedBox(height: 12),
                    Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: [
                        OutlinedButton(
                            onPressed: () => _editServer(existing: item),
                            child: const Text('Edit')),
                        OutlinedButton(
                            onPressed: () => _deleteServer(item),
                            child: const Text('Delete')),
                        OutlinedButton(
                            onPressed: () => _runAction(item, 'sync_metrics'),
                            child: const Text('Sync metrics')),
                        OutlinedButton(
                            onPressed: () => _runAction(item, 'restart_agent'),
                            child: const Text('Restart agent')),
                        OutlinedButton(
                            onPressed: () => _runAction(item, 'reprovision'),
                            child: const Text('Reprovision')),
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
