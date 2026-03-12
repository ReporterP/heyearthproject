import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';
import '../widgets/search_pagination_bar.dart';

class UsersPage extends StatefulWidget {
  const UsersPage({super.key, required this.token});

  final String token;

  @override
  State<UsersPage> createState() => _UsersPageState();
}

class _UsersPageState extends State<UsersPage> {
  static const _platformOptions = <String>[
    '',
    'android',
    'windows',
    'ios',
    'macos',
    'linux',
  ];
  static const _pageSize = 20;

  final _searchController = TextEditingController();
  List<ManagedUser> _users = const [];
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
      final result = await const AdminApiClient().fetchUsersPaged(
        widget.token,
        page: _page,
        pageSize: _pageSize,
        search: _searchController.text.trim(),
        status: '',
      );
      _users = result.items;
      _total = result.total;
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _editUser({ManagedUser? existing}) async {
    final userIdController =
        TextEditingController(text: existing?.userId ?? '');
    final telegramController =
        TextEditingController(text: existing?.telegramId?.toString() ?? '');
    final roleController =
        TextEditingController(text: existing?.role ?? 'user');
    bool isBlocked = existing?.isBlocked ?? false;
    String selectedPlatform = existing?.preferredPlatform ?? '';

    List<UserDevice> devices = const [];
    if (existing != null) {
      try {
        devices = await const AdminApiClient()
            .fetchUserDevices(widget.token, existing.id);
      } catch (_) {}
    }

    final l10n = AdminL10n.of(context);
    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? l10n.createUser : l10n.editUser),
          content: SizedBox(
            width: 480,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  TextField(
                      controller: userIdController,
                      decoration:
                          InputDecoration(labelText: l10n.fieldUserId)),
                  const SizedBox(height: 12),
                  TextField(
                      controller: telegramController,
                      decoration:
                          InputDecoration(labelText: l10n.fieldTelegramId)),
                  const SizedBox(height: 12),
                  TextField(
                      controller: roleController,
                      decoration: InputDecoration(labelText: l10n.fieldRole)),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    initialValue: _platformOptions.contains(selectedPlatform)
                        ? selectedPlatform
                        : '',
                    decoration: InputDecoration(
                        labelText: l10n.fieldPreferredPlatform),
                    items: _platformOptions
                        .map((p) => DropdownMenuItem(
                              value: p,
                              child: Text(p.isEmpty
                                  ? l10n.fieldPlatformNotSet
                                  : p),
                            ))
                        .toList(),
                    onChanged: (v) =>
                        setStateDialog(() => selectedPlatform = v ?? ''),
                  ),
                  CheckboxListTile(
                    value: isBlocked,
                    onChanged: (value) =>
                        setStateDialog(() => isBlocked = value ?? false),
                    title: Text(l10n.fieldBlocked),
                    contentPadding: EdgeInsets.zero,
                  ),
                  if (devices.isNotEmpty) ...[
                    const Divider(height: 24),
                    Text(l10n.registeredDevicesLabel(devices.length),
                        style: Theme.of(context).textTheme.titleSmall),
                    const SizedBox(height: 8),
                    for (final d in devices)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 6),
                        child: Row(
                          children: [
                            _platformIcon(d.platform),
                            const SizedBox(width: 8),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(
                                    '${d.platform}${d.deviceLabel.isNotEmpty ? " · ${d.deviceLabel}" : ""}',
                                    style:
                                        Theme.of(context).textTheme.bodyMedium,
                                  ),
                                  Text(
                                    'ID: ${d.deviceId}${d.linkedVia.isNotEmpty ? " · via ${d.linkedVia}" : ""}',
                                    style:
                                        Theme.of(context).textTheme.bodySmall,
                                  ),
                                  Text(
                                    '${l10n.deviceLastSeen}: ${_fmtDate(d.lastSeenAt)}',
                                    style:
                                        Theme.of(context).textTheme.bodySmall,
                                  ),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),
                  ],
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: Text(l10n.cancel)),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(<String, dynamic>{
                'user_id': userIdController.text.trim(),
                'telegram_id': int.tryParse(telegramController.text.trim()),
                'role': roleController.text.trim(),
                'is_blocked': isBlocked,
                'preferred_platform': selectedPlatform,
              }),
              child: Text(l10n.save),
            ),
          ],
        ),
      ),
    );

    userIdController.dispose();
    telegramController.dispose();
    roleController.dispose();
    if (payload == null) return;

    try {
      if (existing == null) {
        await const AdminApiClient().createUser(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updateUser(widget.token, existing.id, payload);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteUser(ManagedUser user) async {
    final l10n = AdminL10n.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.deleteUser),
        content: Text(l10n.deleteUserConfirm(user.userId)),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(false),
              child: Text(l10n.cancel)),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(l10n.delete),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await const AdminApiClient().deleteUser(widget.token, user.id);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Widget _platformIcon(String platform) {
    IconData icon;
    switch (platform) {
      case 'android':
        icon = Icons.android;
      case 'windows':
        icon = Icons.laptop_windows;
      case 'ios':
      case 'macos':
        icon = Icons.apple;
      case 'linux':
        icon = Icons.computer;
      default:
        icon = Icons.devices_other;
    }
    return Icon(icon, size: 18);
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
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                  child: Text(l10n.usersTitle,
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: () => _editUser(),
                icon: const Icon(Icons.person_add_alt_1),
                label: Text(l10n.create),
              ),
            ],
          ),
          const SizedBox(height: 16),
          SearchPaginationBar(
            searchController: _searchController,
            onSearch: () {
              _page = 1;
              _load();
            },
            page: _page,
            totalPages: _totalPages,
            total: _total,
            onPageChanged: (p) {
              _page = p;
              _load();
            },
            searchHint: isEn(l10n) ? 'Search by ID, role…' : 'Поиск по ID, роли…',
          ),
          const SizedBox(height: 8),
          if (_loading)
            const Expanded(child: Center(child: CircularProgressIndicator()))
          else if (_users.isEmpty)
            Expanded(
              child: Center(
                child: Text(l10n.noUsers,
                    style: Theme.of(context).textTheme.bodyLarge),
              ),
            )
          else
            Expanded(
              child: ListView.builder(
                itemCount: _users.length,
                itemBuilder: (context, index) {
                  final user = _users[index];
                  return Card(
                    child: ListTile(
                      title: Text(user.userId),
                      subtitle: Text(
                        '${l10n.userRolePlan}: ${user.role} | ${l10n.userPlan}: ${user.currentPlan}'
                        '${user.telegramId == null ? '' : '\nTelegram: ${user.telegramId}'}'
                        '${user.preferredPlatform.isEmpty ? '' : '\nPlatform: ${user.preferredPlatform}'}',
                      ),
                      isThreeLine: user.telegramId != null ||
                          user.preferredPlatform.isNotEmpty,
                      trailing: Wrap(
                        spacing: 8,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        children: [
                          if (user.isBlocked)
                            Chip(label: Text(l10n.blockedChip)),
                          OutlinedButton(
                            onPressed: () => _editUser(existing: user),
                            child: Text(l10n.edit),
                          ),
                          OutlinedButton(
                            style: OutlinedButton.styleFrom(
                                foregroundColor: Colors.red),
                            onPressed: () => _deleteUser(user),
                            child: Text(l10n.delete),
                          ),
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

bool isEn(AdminL10n l10n) => l10n.isEn;
