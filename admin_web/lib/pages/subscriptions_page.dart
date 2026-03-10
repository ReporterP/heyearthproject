import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class SubscriptionsPage extends StatefulWidget {
  const SubscriptionsPage({super.key, required this.token});

  final String token;

  @override
  State<SubscriptionsPage> createState() => _SubscriptionsPageState();
}

class _SubscriptionsPageState extends State<SubscriptionsPage> {
  List<SubscriptionModel> _subscriptions = const [];
  List<ManagedUser> _users = const [];
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
      _subscriptions = await client.fetchSubscriptions(widget.token);
      _users = await client.fetchUsers(widget.token);
      _groups = await client.fetchGroups(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _saveSubscription({SubscriptionModel? existing}) async {
    int? userDbId = existing?.userDbId;
    int? groupId = existing?.groupId;
    final validUntilController = TextEditingController(
      text: existing?.validUntil.isNotEmpty == true
          ? existing!.validUntil
          : DateTime.now().add(const Duration(days: 30)).toIso8601String(),
    );
    bool autoRenew = existing?.autoRenew ?? false;

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(
              existing == null ? 'Save subscription' : 'Edit subscription'),
          content: SizedBox(
            width: 460,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                DropdownButtonFormField<int>(
                  initialValue: userDbId,
                  items: _users
                      .map((user) => DropdownMenuItem<int>(
                          value: user.id,
                          child: Text('${user.userId} (${user.id})')))
                      .toList(growable: false),
                  onChanged: (value) => setStateDialog(() => userDbId = value),
                  decoration: const InputDecoration(labelText: 'User'),
                ),
                const SizedBox(height: 12),
                DropdownButtonFormField<int>(
                  initialValue: groupId,
                  items: _groups
                      .map((group) => DropdownMenuItem<int>(
                          value: group.id, child: Text(group.name)))
                      .toList(growable: false),
                  onChanged: (value) => setStateDialog(() => groupId = value),
                  decoration: const InputDecoration(labelText: 'Group'),
                ),
                const SizedBox(height: 12),
                TextField(
                    controller: validUntilController,
                    decoration: const InputDecoration(
                        labelText: 'Valid until (RFC3339)')),
                CheckboxListTile(
                  value: autoRenew,
                  onChanged: (value) =>
                      setStateDialog(() => autoRenew = value ?? false),
                  title: const Text('Auto renew'),
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
              onPressed: userDbId == null || groupId == null
                  ? null
                  : () => Navigator.of(context).pop(<String, dynamic>{
                        'user_db_id': userDbId,
                        'group_id': groupId,
                        'valid_until': validUntilController.text.trim(),
                        'auto_renew': autoRenew,
                      }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    validUntilController.dispose();
    if (payload == null) {
      return;
    }
    try {
      await const AdminApiClient().saveSubscription(widget.token, payload);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteSubscription(SubscriptionModel item) async {
    try {
      await const AdminApiClient()
          .deleteSubscription(widget.token, item.userDbId, item.groupId);
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
                  child: Text('Subscriptions',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: () => _saveSubscription(),
                icon: const Icon(Icons.add),
                label: const Text('Save'),
              ),
            ],
          ),
          const SizedBox(height: 16),
          if (_subscriptions.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No subscriptions found'),
              ),
            ),
          for (final item in _subscriptions)
            Card(
              child: ListTile(
                title: Text('${item.userId} -> ${item.groupName}'),
                subtitle: Text(
                    'Valid until: ${item.validUntil}\nAuto renew: ${item.autoRenew ? 'Yes' : 'No'}'),
                isThreeLine: true,
                trailing: Wrap(
                  spacing: 8,
                  children: [
                    OutlinedButton(
                        onPressed: () => _saveSubscription(existing: item),
                        child: const Text('Edit')),
                    OutlinedButton(
                        onPressed: () => _deleteSubscription(item),
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
