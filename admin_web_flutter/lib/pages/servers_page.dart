import 'dart:convert';

import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';
import '../widgets/search_pagination_bar.dart';

class ServersPage extends StatefulWidget {
  const ServersPage({super.key, required this.token});

  final String token;

  @override
  State<ServersPage> createState() => _ServersPageState();
}

class _ServersPageState extends State<ServersPage> {
  static const _pageSize = 20;

  final _searchController = TextEditingController();
  String _statusFilter = '';
  List<AdminServer> _servers = const [];
  List<GroupModel> _groups = const [];
  List<SshKey> _sshKeys = const [];
  int _page = 1;
  int _total = 0;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  int get _totalPages => (_total / _pageSize).ceil().clamp(1, 999999);

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      final client = const AdminApiClient();
      final results = await Future.wait([
        client.fetchServersPaged(
          widget.token,
          page: _page,
          pageSize: _pageSize,
          search: _searchController.text.trim(),
          status: _statusFilter,
        ),
        client.fetchGroups(widget.token, pageSize: 100),
        client.fetchSshKeys(widget.token),
      ]);
      final paged = results[0] as PagedResult<AdminServer>;
      _servers = paged.items;
      _total = paged.total;
      _groups = results[1] as List<GroupModel>;
      _sshKeys = results[2] as List<SshKey>;
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Widget _buildMetricsCard(AdminServer item) {
    Map<String, dynamic> metrics = {};
    try {
      if (item.loadMetrics.isNotEmpty) {
        final decoded = item.loadMetrics is String
            ? jsonDecode(item.loadMetrics as String)
            : item.loadMetrics;
        if (decoded is Map) metrics = decoded.cast<String, dynamic>();
      }
    } catch (_) {}

    final cpu = (metrics['cpu_usage'] as num?)?.toDouble();
    final mem = (metrics['memory_usage'] as num?)?.toDouble();
    final conn = (metrics['active_connections'] as num?)?.toInt();
    final syncedAt = metrics['synced_at'] as String?;

    if (cpu == null && mem == null && conn == null) {
      return const Text('No metrics', style: TextStyle(color: Colors.grey));
    }

    return Row(
      children: [
        if (cpu != null) _metricChip(Icons.memory, 'CPU', '${cpu.toStringAsFixed(1)}%', _cpuColor(cpu)),
        if (mem != null) _metricChip(Icons.storage, 'RAM', '${mem.toStringAsFixed(1)}%', _memColor(mem)),
        if (conn != null) _metricChip(Icons.people, 'Conn', '$conn', Colors.blueAccent),
        if (syncedAt != null)
          Padding(
            padding: const EdgeInsets.only(left: 8),
            child: Text(
              'Synced: ${_fmtDate(syncedAt)}',
              style: const TextStyle(fontSize: 11, color: Colors.grey),
            ),
          ),
      ],
    );
  }

  Widget _metricChip(IconData icon, String label, String value, Color color) {
    return Container(
      margin: const EdgeInsets.only(right: 8),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withAlpha(25),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withAlpha(80)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 14, color: color),
          const SizedBox(width: 4),
          Text('$label: ', style: TextStyle(fontSize: 11, color: color)),
          Text(value, style: TextStyle(fontSize: 12, fontWeight: FontWeight.bold, color: color)),
        ],
      ),
    );
  }

  Color _cpuColor(double v) => v > 80 ? Colors.red : v > 50 ? Colors.orange : Colors.green;
  Color _memColor(double v) => v > 85 ? Colors.red : v > 60 ? Colors.orange : Colors.green;

  Widget _statusBadge(String status) {
    Color color;
    switch (status) {
      case 'online':
        color = Colors.green;
      case 'offline':
        color = Colors.red;
      case 'provisioning':
        color = Colors.orange;
      default:
        color = Colors.grey;
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withAlpha(25),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: color),
      ),
      child: Text(status, style: TextStyle(color: color, fontSize: 12, fontWeight: FontWeight.w600)),
    );
  }

  Widget _tunnelStatusBadge(String tunnelStatus) {
    Color color;
    switch (tunnelStatus) {
      case 'healthy':
        color = Colors.green;
      case 'configuring':
        color = Colors.orange;
      case 'unknown':
      default:
        color = Colors.red;
    }
    final l10n = AdminL10n.of(context);
    final label = switch (tunnelStatus) {
      'healthy' => l10n.tunnelStatusHealthy,
      'configuring' => l10n.tunnelStatusConfiguring,
      _ => l10n.tunnelStatusUnknown,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withAlpha(25),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withAlpha(120)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.vpn_lock, size: 12, color: color),
          const SizedBox(width: 4),
          Text(label, style: TextStyle(color: color, fontSize: 11, fontWeight: FontWeight.w600)),
        ],
      ),
    );
  }

  Future<void> _editServer({AdminServer? existing}) async {
    AdminServer? detail = existing;
    if (existing != null) {
      try {
        detail = await const AdminApiClient().fetchServerDetail(widget.token, existing.id);
      } on AdminApiException catch (e) {
        _showMessage(e.message);
        return;
      }
    }

    final nameController = TextEditingController(text: detail?.name ?? '');
    final ipController = TextEditingController(text: detail?.ipAddress ?? '');
    final portController = TextEditingController(text: '${detail?.sshPort ?? 22}');
    final userController = TextEditingController(text: detail?.sshUser ?? 'root');
    final passwordController = TextEditingController(text: detail?.sshPassword ?? '');
    final countryController = TextEditingController(text: detail?.countryCode ?? '');
    final statusController = TextEditingController(text: detail?.status ?? 'provisioning');
    final selectedGroupIds = <int>{...(detail?.groupIds ?? const <int>[])};
    int? selectedSshKeyId = detail?.sshKeyId;
    bool isExitNode = detail?.isExitNode ?? false;

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
                  TextField(controller: nameController, decoration: const InputDecoration(labelText: 'Name')),
                  const SizedBox(height: 12),
                  TextField(controller: ipController, decoration: const InputDecoration(labelText: 'IP address')),
                  const SizedBox(height: 12),
                  TextField(controller: portController, decoration: const InputDecoration(labelText: 'SSH port')),
                  const SizedBox(height: 12),
                  TextField(controller: userController, decoration: const InputDecoration(labelText: 'SSH user')),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<int?>(
                    initialValue: selectedSshKeyId,
                    decoration: const InputDecoration(labelText: 'SSH key (preferred over password)'),
                    items: [
                      const DropdownMenuItem(value: null, child: Text('— use password —')),
                      ..._sshKeys.map((k) => DropdownMenuItem(
                            value: k.id,
                            child: Text('${k.name}  (${k.fingerprint.isNotEmpty ? k.fingerprint.substring(0, 20) : ""}…)'),
                          )),
                    ],
                    onChanged: (v) => setStateDialog(() => selectedSshKeyId = v),
                  ),
                  const SizedBox(height: 12),
                  TextField(controller: passwordController, decoration: InputDecoration(labelText: 'SSH password', helperText: selectedSshKeyId != null ? 'Ignored when key is selected' : null)),
                  const SizedBox(height: 12),
                  TextField(controller: countryController, decoration: const InputDecoration(labelText: 'Country code')),
                  const SizedBox(height: 12),
                  TextField(controller: statusController, decoration: const InputDecoration(labelText: 'Status')),
                  const SizedBox(height: 12),
                  SwitchListTile(
                    value: isExitNode,
                    onChanged: (v) => setStateDialog(() => isExitNode = v),
                    title: Text(AdminL10n.of(context).serverExitNode),
                    contentPadding: EdgeInsets.zero,
                  ),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: _groups.map((group) => FilterChip(
                      label: Text(group.name),
                      selected: selectedGroupIds.contains(group.id),
                      onSelected: (selected) {
                        setStateDialog(() {
                          if (selected) { selectedGroupIds.add(group.id); } else { selectedGroupIds.remove(group.id); }
                        });
                      },
                    )).toList(growable: false),
                  ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(AdminL10n.of(context).cancel)),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(ServerPayload(
                name: nameController.text.trim(),
                ipAddress: ipController.text.trim(),
                sshPort: int.tryParse(portController.text.trim()) ?? 22,
                sshUser: userController.text.trim(),
                sshPassword: passwordController.text.trim(),
                sshKeyId: selectedSshKeyId,
                countryCode: countryController.text.trim(),
                status: statusController.text.trim(),
                groupIds: selectedGroupIds.toList()..sort(),
                isExitNode: isExitNode,
              )),
              child: Text(AdminL10n.of(context).save),
            ),
          ],
        ),
      ),
    );

    nameController.dispose(); ipController.dispose(); portController.dispose();
    userController.dispose(); passwordController.dispose(); countryController.dispose();
    statusController.dispose();
    if (payload == null) return;

    try {
      if (existing == null) {
        await const AdminApiClient().createServer(widget.token, payload);
      } else {
        await const AdminApiClient().updateServer(widget.token, existing.id, payload);
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
      await const AdminApiClient().runServerAction(widget.token, item.id, action);
      await _load();
      _showMessage('Action completed: $action');
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _showLogsDialog(AdminServer item) async {
    await showDialog<void>(
      context: context,
      builder: (context) => _ServerLogsDialog(
        serverName: item.name,
        token: widget.token,
        serverId: item.id,
        onClose: () => Navigator.of(context).pop(),
      ),
    );
  }

  Future<void> _showFilesDialog(AdminServer item) async {
    await showDialog<void>(
      context: context,
      builder: (context) => _ServerFilesDialog(
        serverName: item.name,
        token: widget.token,
        serverId: item.id,
        onClose: () => Navigator.of(context).pop(),
      ),
    );
  }

  String _fmtDate(String iso) {
    try {
      final dt = DateTime.parse(iso).toLocal();
      return '${dt.year}-${_p(dt.month)}-${_p(dt.day)} ${_p(dt.hour)}:${_p(dt.minute)}';
    } catch (_) {
      return iso;
    }
  }

  String _p(int v) => v.toString().padLeft(2, '0');

  void _showMessage(String message) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(children: [
            Expanded(child: Text(l10n.serversTitle, style: Theme.of(context).textTheme.headlineMedium)),
            FilledButton.icon(onPressed: () => _editServer(), icon: const Icon(Icons.add), label: Text(l10n.create)),
          ]),
          const SizedBox(height: 16),
          SearchPaginationBar(
            searchController: _searchController,
            onSearch: () { _page = 1; _load(); },
            page: _page,
            totalPages: _totalPages,
            total: _total,
            onPageChanged: (p) { _page = p; _load(); },
            searchHint: l10n.isEn ? 'Search by name, IP…' : 'Поиск по имени, IP…',
            trailing: DropdownButton<String>(
              value: _statusFilter,
              underline: const SizedBox.shrink(),
              hint: Text(l10n.isEn ? 'Status' : 'Статус'),
              items: [
                DropdownMenuItem(value: '', child: Text(l10n.isEn ? 'All' : 'Все')),
                const DropdownMenuItem(value: 'online', child: Text('Online')),
                const DropdownMenuItem(value: 'offline', child: Text('Offline')),
                const DropdownMenuItem(value: 'provisioning', child: Text('Provisioning')),
              ],
              onChanged: (v) { _statusFilter = v ?? ''; _page = 1; _load(); },
            ),
          ),
          const SizedBox(height: 8),
          if (_loading)
            const Expanded(child: Center(child: CircularProgressIndicator()))
          else if (_servers.isEmpty)
            Expanded(child: Center(child: Text(l10n.noServers, style: Theme.of(context).textTheme.bodyLarge)))
          else
            Expanded(
              child: ListView.builder(
                itemCount: _servers.length,
                itemBuilder: (context, index) {
                  final item = _servers[index];
                  return Card(
                    margin: const EdgeInsets.only(bottom: 12),
                    child: Padding(
                      padding: const EdgeInsets.all(16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(children: [
                            Expanded(
                              child: Row(
                                children: [
                                  Text(item.name, style: Theme.of(context).textTheme.titleLarge),
                                  const SizedBox(width: 8),
                                  _tunnelStatusBadge(item.tunnelStatus),
                                  if (item.isExitNode) ...[
                                    const SizedBox(width: 8),
                                    Chip(
                                      label: Text(AdminL10n.of(context).serverExitNode, style: const TextStyle(fontSize: 11)),
                                      visualDensity: VisualDensity.compact,
                                      padding: EdgeInsets.zero,
                                    ),
                                  ],
                                ],
                              ),
                            ),
                            _statusBadge(item.status),
                          ]),
                          const SizedBox(height: 8),
                          Row(children: [
                            const Icon(Icons.language, size: 14),
                            const SizedBox(width: 4),
                            Text('${item.ipAddress}:${item.sshPort}'),
                            const SizedBox(width: 16),
                            const Icon(Icons.flag, size: 14),
                            const SizedBox(width: 4),
                            Text(item.countryCode),
                          ]),
                          if (item.sshKeyName.isNotEmpty) ...[
                            const SizedBox(height: 4),
                            Row(children: [
                              const Icon(Icons.key, size: 14),
                              const SizedBox(width: 4),
                              Text('SSH Key: ${item.sshKeyName}', style: Theme.of(context).textTheme.bodySmall),
                            ]),
                          ],
                          if (item.groupIds.isNotEmpty) ...[
                            const SizedBox(height: 4),
                            Text('Groups: ${item.groupIds.join(', ')}', style: Theme.of(context).textTheme.bodySmall),
                          ],
                          const SizedBox(height: 8),
                          _buildMetricsCard(item),
                          const SizedBox(height: 12),
                          Wrap(spacing: 8, runSpacing: 8, children: [
                            OutlinedButton.icon(icon: const Icon(Icons.edit, size: 16), onPressed: () => _editServer(existing: item), label: Text(l10n.edit)),
                            OutlinedButton.icon(icon: const Icon(Icons.sync, size: 16), onPressed: () => _runAction(item, 'sync_metrics'), label: Text(l10n.serverSyncMetrics)),
                            OutlinedButton.icon(icon: const Icon(Icons.restart_alt, size: 16), onPressed: () => _runAction(item, 'restart_agent'), label: Text(l10n.serverRestartAgent)),
                            OutlinedButton.icon(icon: const Icon(Icons.build, size: 16), onPressed: () => _runAction(item, 'reprovision'), label: Text(l10n.serverReprovision)),
                            OutlinedButton.icon(icon: const Icon(Icons.vpn_lock, size: 16), onPressed: () => _runAction(item, 'sync_tunnel'), label: Text(l10n.serverSyncTunnel)),
                            OutlinedButton.icon(icon: const Icon(Icons.description, size: 16), onPressed: () => _showLogsDialog(item), label: Text(l10n.serverLogs)),
                            OutlinedButton.icon(icon: const Icon(Icons.folder_open, size: 16), onPressed: () => _showFilesDialog(item), label: Text(l10n.serverFiles)),
                            OutlinedButton.icon(
                              icon: const Icon(Icons.delete, size: 16),
                              style: OutlinedButton.styleFrom(foregroundColor: Colors.red),
                              onPressed: () => _deleteServer(item),
                              label: Text(l10n.delete),
                            ),
                          ]),
                        ],
                      ),
                    ),
                  );
                },
              ),
            ),
        ],
      ),
    );
  }
}

class _ServerLogsDialog extends StatefulWidget {
  const _ServerLogsDialog({
    required this.serverName,
    required this.token,
    required this.serverId,
    required this.onClose,
  });

  final String serverName;
  final String token;
  final int serverId;
  final VoidCallback onClose;

  @override
  State<_ServerLogsDialog> createState() => _ServerLogsDialogState();
}

class _ServerLogsDialogState extends State<_ServerLogsDialog> {
  static const _services = ['node-agent', 'xray', 'shadowsocks'];

  String _service = 'node-agent';
  String _logs = '';
  bool _loading = false;
  String? _error;

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final logs = await const AdminApiClient().fetchServerLogs(
        widget.token,
        widget.serverId,
        service: _service,
        tail: 100,
      );
      if (mounted) setState(() => _logs = logs);
    } on AdminApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    return AlertDialog(
      title: Text('${l10n.serverLogsTitle} — ${widget.serverName}'),
      content: SizedBox(
        width: 640,
        height: 480,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                Text('${l10n.serverLogsService}: '),
                const SizedBox(width: 8),
                DropdownButton<String>(
                  value: _service,
                  items: _services.map((s) => DropdownMenuItem(value: s, child: Text(s))).toList(),
                  onChanged: _loading ? null : (v) {
                    if (v != null) setState(() => _service = v);
                    _load();
                  },
                ),
                const Spacer(),
                FilledButton.icon(
                  icon: const Icon(Icons.refresh, size: 16),
                  onPressed: _loading ? null : _load,
                  label: Text(l10n.serverLogsRefresh),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Expanded(
              child: Container(
                decoration: BoxDecoration(
                  color: Colors.grey.shade900,
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(color: Colors.grey.shade700),
                ),
                padding: const EdgeInsets.all(8),
                child: SingleChildScrollView(
                  child: _loading && _logs.isEmpty
                      ? const Center(child: CircularProgressIndicator())
                      : _error != null
                          ? Text(_error!, style: const TextStyle(color: Colors.red))
                          : SelectableText(
                              _logs.isEmpty ? '— empty —' : _logs,
                              style: const TextStyle(
                                fontFamily: 'monospace',
                                fontSize: 12,
                              ),
                            ),
                ),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: widget.onClose, child: Text(l10n.close)),
      ],
    );
  }
}

class _ServerFilesDialog extends StatefulWidget {
  const _ServerFilesDialog({
    required this.serverName,
    required this.token,
    required this.serverId,
    required this.onClose,
  });

  final String serverName;
  final String token;
  final int serverId;
  final VoidCallback onClose;

  @override
  State<_ServerFilesDialog> createState() => _ServerFilesDialogState();
}

class _ServerFilesDialogState extends State<_ServerFilesDialog> {
  String _path = '/opt/heyearth';
  String _listing = '';
  bool _loading = false;
  String? _error;
  final _pathController = TextEditingController();

  @override
  void initState() {
    super.initState();
    _pathController.text = _path;
    _load();
  }

  @override
  void dispose() {
    _pathController.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final path = _pathController.text.trim().isNotEmpty
        ? _pathController.text.trim()
        : '/opt/heyearth';
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await const AdminApiClient().fetchServerFiles(
        widget.token,
        widget.serverId,
        path,
      );
      if (mounted) {
        setState(() {
          _listing = result['listing'] ?? '';
          _path = result['path'] ?? path;
          _pathController.text = _path;
        });
      }
    } on AdminApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    return AlertDialog(
      title: Text('${l10n.serverFilesTitle} — ${widget.serverName}'),
      content: SizedBox(
        width: 640,
        height: 420,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                Text('${l10n.serverFilesPath}: ', style: Theme.of(context).textTheme.labelMedium),
                Expanded(
                  child: Text(_path, style: const TextStyle(fontFamily: 'monospace', fontSize: 12)),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _pathController,
                    decoration: const InputDecoration(
                      hintText: '/opt/heyearth',
                      isDense: true,
                    ),
                    style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton.icon(
                  icon: const Icon(Icons.folder_open, size: 16),
                  onPressed: _loading ? null : _load,
                  label: Text(l10n.serverFilesBrowse),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Expanded(
              child: Container(
                decoration: BoxDecoration(
                  color: Colors.grey.shade900,
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(color: Colors.grey.shade700),
                ),
                padding: const EdgeInsets.all(8),
                child: SingleChildScrollView(
                  child: _loading && _listing.isEmpty
                      ? const Center(child: CircularProgressIndicator())
                      : _error != null
                          ? Text(_error!, style: const TextStyle(color: Colors.red))
                          : SelectableText(
                              _listing.isEmpty ? '— empty —' : _listing,
                              style: const TextStyle(
                                fontFamily: 'monospace',
                                fontSize: 12,
                              ),
                            ),
                ),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: widget.onClose, child: Text(l10n.close)),
      ],
    );
  }
}
