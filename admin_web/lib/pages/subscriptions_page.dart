import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
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

    final l10n = AdminL10n.of(context);
    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? l10n.saveSubscription : l10n.editSubscription),
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
                  decoration: InputDecoration(labelText: l10n.fieldUserDbId),
                ),
                const SizedBox(height: 12),
                DropdownButtonFormField<int>(
                  initialValue: groupId,
                  items: _groups
                      .map((group) => DropdownMenuItem<int>(
                          value: group.id, child: Text(group.name)))
                      .toList(growable: false),
                  onChanged: (value) => setStateDialog(() => groupId = value),
                  decoration: InputDecoration(labelText: l10n.fieldGroupId),
                ),
                const SizedBox(height: 12),
                TextField(
                    controller: validUntilController,
                    decoration: InputDecoration(labelText: l10n.fieldValidUntil)),
                CheckboxListTile(
                  value: autoRenew,
                  onChanged: (value) =>
                      setStateDialog(() => autoRenew = value ?? false),
                  title: Text(l10n.fieldAutoRenew),
                  contentPadding: EdgeInsets.zero,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: Text(l10n.cancel)),
            FilledButton(
              onPressed: userDbId == null || groupId == null
                  ? null
                  : () => Navigator.of(context).pop(<String, dynamic>{
                        'user_db_id': userDbId,
                        'group_id': groupId,
                        'valid_until': validUntilController.text.trim(),
                        'auto_renew': autoRenew,
                      }),
              child: Text(l10n.save),
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
          Builder(builder: (context) {
            final l10n = AdminL10n.of(context);
            return Row(
              children: [
                Expanded(child: Text(l10n.subscriptionsTitle,
                    style: Theme.of(context).textTheme.headlineMedium)),
                FilledButton.icon(
                  onPressed: () => _saveSubscription(),
                  icon: const Icon(Icons.add),
                  label: Text(l10n.save),
                ),
              ],
            );
          }),
          const SizedBox(height: 16),
          Builder(builder: (context) {
            final l10n = AdminL10n.of(context);
            if (_subscriptions.isEmpty) {
              return Card(child: Padding(padding: const EdgeInsets.all(24), child: Text(l10n.noSubscriptions)));
            }
            return Column(children: [
              for (final item in _subscriptions)
                Card(
                  child: ListTile(
                    title: Text('${item.userId} → ${item.groupName}'),
                    subtitle: Text(
                        '${l10n.fieldValidUntil}: ${item.validUntil}\n${l10n.fieldAutoRenew}: ${item.autoRenew ? (l10n.isEn ? 'Yes' : 'Да') : (l10n.isEn ? 'No' : 'Нет')}'),
                    isThreeLine: true,
                    trailing: Wrap(
                      spacing: 8,
                      children: [
                        OutlinedButton(
                            onPressed: () => _saveSubscription(existing: item),
                            child: Text(l10n.edit)),
                        OutlinedButton(
                            onPressed: () => _deleteSubscription(item),
                            child: Text(l10n.delete)),
                      ],
                    ),
                  ),
                ),
            ]);
          }),
        ],
      ),
    );
  }
}
