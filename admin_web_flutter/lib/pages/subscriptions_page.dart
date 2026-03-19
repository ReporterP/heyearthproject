import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';
import '../widgets/search_pagination_bar.dart';

class SubscriptionsPage extends StatefulWidget {
  const SubscriptionsPage({super.key, required this.token});

  final String token;

  @override
  State<SubscriptionsPage> createState() => _SubscriptionsPageState();
}

class _SubscriptionsPageState extends State<SubscriptionsPage> {
  static const _pageSize = 20;

  final _searchController = TextEditingController();
  List<SubscriptionModel> _subscriptions = const [];
  List<ManagedUser> _users = const [];
  List<GroupModel> _groups = const [];
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
        client.fetchSubscriptionsPaged(
          widget.token,
          page: _page,
          pageSize: _pageSize,
          search: _searchController.text.trim(),
          status: '',
        ),
        client.fetchUsers(widget.token, pageSize: 100),
        client.fetchGroups(widget.token, pageSize: 100),
      ]);
      final paged = results[0] as PagedResult<SubscriptionModel>;
      _subscriptions = paged.items;
      _total = paged.total;
      _users = results[1] as List<ManagedUser>;
      _groups = results[2] as List<GroupModel>;
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
    final l10n = AdminL10n.of(context);
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(child: Text(l10n.subscriptionsTitle,
                  style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: () => _saveSubscription(),
                icon: const Icon(Icons.add),
                label: Text(l10n.save),
              ),
            ],
          ),
          const SizedBox(height: 16),
          SearchPaginationBar(
            searchController: _searchController,
            onSearch: () { _page = 1; _load(); },
            page: _page,
            totalPages: _totalPages,
            total: _total,
            onPageChanged: (p) { _page = p; _load(); },
            searchHint: l10n.isEn ? 'Search by user_id…' : 'Поиск по user_id…',
          ),
          const SizedBox(height: 8),
          if (_loading)
            const Expanded(child: Center(child: CircularProgressIndicator()))
          else if (_subscriptions.isEmpty)
            Expanded(child: Center(child: Text(l10n.noSubscriptions, style: Theme.of(context).textTheme.bodyLarge)))
          else
            Expanded(
              child: ListView.builder(
                itemCount: _subscriptions.length,
                itemBuilder: (context, index) {
                  final item = _subscriptions[index];
                  return Card(
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
                  );
                },
              ),
            ),
        ],
      ),
    );
  }
}
