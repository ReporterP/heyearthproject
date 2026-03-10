import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class GroupsPage extends StatefulWidget {
  const GroupsPage({super.key, required this.token});

  final String token;

  @override
  State<GroupsPage> createState() => _GroupsPageState();
}

class _GroupsPageState extends State<GroupsPage> {
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
      _groups = await const AdminApiClient().fetchGroups(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _editGroup({GroupModel? existing}) async {
    final nameController = TextEditingController(text: existing?.name ?? '');
    final descriptionController =
        TextEditingController(text: existing?.description ?? '');
    final companyController =
        TextEditingController(text: existing?.companyId?.toString() ?? '');
    bool isPremium = existing?.isPremium ?? false;

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create group' : 'Edit group'),
          content: SizedBox(
            width: 420,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                    controller: nameController,
                    decoration: const InputDecoration(labelText: 'Name')),
                const SizedBox(height: 12),
                TextField(
                    controller: descriptionController,
                    decoration:
                        const InputDecoration(labelText: 'Description')),
                const SizedBox(height: 12),
                TextField(
                    controller: companyController,
                    decoration: const InputDecoration(labelText: 'Company ID')),
                CheckboxListTile(
                  value: isPremium,
                  onChanged: (value) =>
                      setStateDialog(() => isPremium = value ?? false),
                  title: const Text('Premium'),
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
                'name': nameController.text.trim(),
                'description': descriptionController.text.trim(),
                'company_id': int.tryParse(companyController.text.trim()),
                'is_premium': isPremium,
              }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    nameController.dispose();
    descriptionController.dispose();
    companyController.dispose();
    if (payload == null) {
      return;
    }

    try {
      if (existing == null) {
        await const AdminApiClient().createGroup(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updateGroup(widget.token, existing.id, payload);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteGroup(GroupModel group) async {
    try {
      await const AdminApiClient().deleteGroup(widget.token, group.id);
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
                  child: Text('Groups',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: () => _editGroup(),
                icon: const Icon(Icons.add),
                label: const Text('Create'),
              ),
            ],
          ),
          const SizedBox(height: 16),
          if (_groups.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No groups found'),
              ),
            ),
          for (final group in _groups)
            Card(
              child: ListTile(
                title: Text(group.name),
                subtitle: Text(group.description.isEmpty
                    ? 'No description'
                    : group.description),
                trailing: Wrap(
                  spacing: 8,
                  children: [
                    if (group.isPremium) const Chip(label: Text('Premium')),
                    OutlinedButton(
                        onPressed: () => _editGroup(existing: group),
                        child: const Text('Edit')),
                    OutlinedButton(
                        onPressed: () => _deleteGroup(group),
                        child: const Text('Delete')),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}
