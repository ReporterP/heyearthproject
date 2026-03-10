import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class AdminsPage extends StatefulWidget {
  const AdminsPage({super.key, required this.token});

  final String token;

  @override
  State<AdminsPage> createState() => _AdminsPageState();
}

class _AdminsPageState extends State<AdminsPage> {
  List<AdminProfile> _admins = const [];
  List<AdminRoleModel> _roles = const [];
  List<AdminPermission> _permissions = const [];
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
      _admins = await client.fetchAdminUsers(widget.token);
      _roles = await client.fetchAdminRoles(widget.token);
      _permissions = await client.fetchAdminPermissions(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _editAdmin({AdminProfile? existing}) async {
    final usernameController =
        TextEditingController(text: existing?.username ?? '');
    final displayNameController =
        TextEditingController(text: existing?.displayName ?? '');
    final passwordController = TextEditingController();
    bool isActive = existing?.isActive ?? true;
    final selectedRoles = <String>{...(existing?.roles ?? const <String>[])};

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create admin' : 'Edit admin'),
          content: SizedBox(
            width: 560,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextField(
                      controller: usernameController,
                      decoration: const InputDecoration(labelText: 'Username')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: displayNameController,
                      decoration:
                          const InputDecoration(labelText: 'Display name')),
                  const SizedBox(height: 12),
                  TextField(
                    controller: passwordController,
                    obscureText: true,
                    decoration: InputDecoration(
                        labelText: existing == null
                            ? 'Password'
                            : 'New password (optional)'),
                  ),
                  CheckboxListTile(
                    value: isActive,
                    onChanged: (value) =>
                        setStateDialog(() => isActive = value ?? true),
                    title: const Text('Active'),
                    contentPadding: EdgeInsets.zero,
                  ),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: _roles
                        .map(
                          (role) => FilterChip(
                            label: Text(role.roleKey),
                            selected: selectedRoles.contains(role.roleKey),
                            onSelected: (selected) {
                              setStateDialog(() {
                                if (selected) {
                                  selectedRoles.add(role.roleKey);
                                } else {
                                  selectedRoles.remove(role.roleKey);
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
              onPressed: () => Navigator.of(context).pop(<String, dynamic>{
                'username': usernameController.text.trim(),
                'display_name': displayNameController.text.trim(),
                'password': passwordController.text.trim(),
                'is_active': isActive,
                'role_keys': selectedRoles.toList()..sort(),
              }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    usernameController.dispose();
    displayNameController.dispose();
    passwordController.dispose();
    if (payload == null) {
      return;
    }
    try {
      if (existing == null) {
        await const AdminApiClient().createAdminUser(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updateAdminUser(widget.token, existing.id, payload);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _editRole({AdminRoleModel? existing}) async {
    final roleKeyController =
        TextEditingController(text: existing?.roleKey ?? '');
    final nameController = TextEditingController(text: existing?.name ?? '');
    final descriptionController =
        TextEditingController(text: existing?.description ?? '');
    bool isSystem = existing?.isSystem ?? false;
    final selectedPermissions = <String>{
      ...(existing?.permissions ?? const <String>[])
    };

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create role' : 'Edit role'),
          content: SizedBox(
            width: 620,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextField(
                      controller: roleKeyController,
                      enabled: existing == null,
                      decoration: const InputDecoration(labelText: 'Role key')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: nameController,
                      decoration: const InputDecoration(labelText: 'Name')),
                  const SizedBox(height: 12),
                  TextField(
                      controller: descriptionController,
                      decoration:
                          const InputDecoration(labelText: 'Description')),
                  CheckboxListTile(
                    value: isSystem,
                    onChanged: (value) =>
                        setStateDialog(() => isSystem = value ?? false),
                    title: const Text('System role'),
                    contentPadding: EdgeInsets.zero,
                  ),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: _permissions
                        .map(
                          (permission) => FilterChip(
                            label: Text(permission.key),
                            selected:
                                selectedPermissions.contains(permission.key),
                            onSelected: (selected) {
                              setStateDialog(() {
                                if (selected) {
                                  selectedPermissions.add(permission.key);
                                } else {
                                  selectedPermissions.remove(permission.key);
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
              onPressed: () => Navigator.of(context).pop(<String, dynamic>{
                'role_key': roleKeyController.text.trim(),
                'name': nameController.text.trim(),
                'description': descriptionController.text.trim(),
                'is_system': isSystem,
                'permission_keys': selectedPermissions.toList()..sort(),
              }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    roleKeyController.dispose();
    nameController.dispose();
    descriptionController.dispose();
    if (payload == null) {
      return;
    }
    try {
      if (existing == null) {
        await const AdminApiClient().createAdminRole(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updateAdminRole(widget.token, existing.id, payload);
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
          Text('Admins', style: Theme.of(context).textTheme.headlineMedium),
          const SizedBox(height: 16),
          Row(
            children: [
              FilledButton.icon(
                  onPressed: () => _editAdmin(),
                  icon: const Icon(Icons.person_add),
                  label: const Text('Create admin')),
              const SizedBox(width: 12),
              FilledButton.icon(
                  onPressed: () => _editRole(),
                  icon: const Icon(Icons.security),
                  label: const Text('Create role')),
            ],
          ),
          const SizedBox(height: 16),
          Text('Admin users', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 8),
          for (final admin in _admins)
            Card(
              child: ListTile(
                title: Text(admin.username),
                subtitle: Text(
                    '${admin.displayName}\nRoles: ${admin.roles.join(', ')}'),
                isThreeLine: true,
                trailing: OutlinedButton(
                  onPressed: () => _editAdmin(existing: admin),
                  child: const Text('Edit'),
                ),
              ),
            ),
          const SizedBox(height: 24),
          Text('Roles', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 8),
          for (final role in _roles)
            Card(
              child: ListTile(
                title: Text(role.roleKey),
                subtitle: Text(
                    '${role.name}\nPermissions: ${role.permissions.join(', ')}'),
                isThreeLine: true,
                trailing: OutlinedButton(
                  onPressed: () => _editRole(existing: role),
                  child: const Text('Edit'),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
