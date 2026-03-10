import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class UsersPage extends StatefulWidget {
  const UsersPage({super.key, required this.token});

  final String token;

  @override
  State<UsersPage> createState() => _UsersPageState();
}

class _UsersPageState extends State<UsersPage> {
  List<ManagedUser> _users = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      _users = await const AdminApiClient().fetchUsers(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
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

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create user' : 'Edit user'),
          content: SizedBox(
            width: 420,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                    controller: userIdController,
                    decoration: const InputDecoration(labelText: 'User ID')),
                const SizedBox(height: 12),
                TextField(
                    controller: telegramController,
                    decoration:
                        const InputDecoration(labelText: 'Telegram ID')),
                const SizedBox(height: 12),
                TextField(
                    controller: roleController,
                    decoration: const InputDecoration(labelText: 'Role')),
                CheckboxListTile(
                  value: isBlocked,
                  onChanged: (value) =>
                      setStateDialog(() => isBlocked = value ?? false),
                  title: const Text('Blocked'),
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
              onPressed: () => Navigator.of(context).pop(<String, dynamic>{
                'user_id': userIdController.text.trim(),
                'telegram_id': int.tryParse(telegramController.text.trim()),
                'role': roleController.text.trim(),
                'is_blocked': isBlocked,
              }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    userIdController.dispose();
    telegramController.dispose();
    roleController.dispose();
    if (payload == null) {
      return;
    }

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
                  child: Text('Users',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: () => _editUser(),
                icon: const Icon(Icons.person_add_alt_1),
                label: const Text('Create'),
              ),
            ],
          ),
          const SizedBox(height: 16),
          if (_users.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No users found'),
              ),
            ),
          for (final user in _users)
            Card(
              child: ListTile(
                title: Text(user.userId),
                subtitle: Text(
                    'Role: ${user.role} | Plan: ${user.currentPlan}${user.telegramId == null ? '' : '\nTelegram: ${user.telegramId}'}'),
                isThreeLine: user.telegramId != null,
                trailing: Wrap(
                  spacing: 8,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    if (user.isBlocked) const Chip(label: Text('Blocked')),
                    OutlinedButton(
                      onPressed: () => _editUser(existing: user),
                      child: const Text('Edit'),
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
