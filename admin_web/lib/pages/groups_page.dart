import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';
import '../widgets/search_pagination_bar.dart';

class GroupsPage extends StatefulWidget {
  const GroupsPage({super.key, required this.token});

  final String token;

  @override
  State<GroupsPage> createState() => _GroupsPageState();
}

class _GroupsPageState extends State<GroupsPage> {
  static const _pageSize = 20;

  final _searchController = TextEditingController();
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
      final result = await const AdminApiClient().fetchGroupsPaged(
        widget.token,
        page: _page,
        pageSize: _pageSize,
        search: _searchController.text.trim(),
        status: '',
      );
      _groups = result.items;
      _total = result.total;
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

    final l10n = AdminL10n.of(context);
    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? l10n.createGroup : l10n.editGroup),
          content: SizedBox(
            width: 420,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                    controller: nameController,
                    decoration: InputDecoration(labelText: l10n.fieldGroupName)),
                const SizedBox(height: 12),
                TextField(
                    controller: descriptionController,
                    decoration:
                        InputDecoration(labelText: l10n.fieldDescription)),
                const SizedBox(height: 12),
                TextField(
                    controller: companyController,
                    decoration: InputDecoration(labelText: l10n.fieldCompanyId)),
                CheckboxListTile(
                  value: isPremium,
                  onChanged: (value) =>
                      setStateDialog(() => isPremium = value ?? false),
                  title: Text(l10n.fieldIsPremium),
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
              onPressed: () => Navigator.of(context).pop(<String, dynamic>{
                'name': nameController.text.trim(),
                'description': descriptionController.text.trim(),
                'company_id': int.tryParse(companyController.text.trim()),
                'is_premium': isPremium,
              }),
              child: Text(l10n.save),
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
    final l10n = AdminL10n.of(context);
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                  child: Text(l10n.groupsTitle,
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                onPressed: () => _editGroup(),
                icon: const Icon(Icons.add),
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
            searchHint: l10n.isEn ? 'Search by group name…' : 'Поиск по названию…',
          ),
          const SizedBox(height: 8),
          if (_loading)
            const Expanded(child: Center(child: CircularProgressIndicator()))
          else if (_groups.isEmpty)
            Expanded(
              child: Center(
                child: Text(l10n.noGroups,
                    style: Theme.of(context).textTheme.bodyLarge),
              ),
            )
          else
            Expanded(
              child: ListView.builder(
                itemCount: _groups.length,
                itemBuilder: (context, index) {
                  final group = _groups[index];
                  return Card(
                    child: ListTile(
                      title: Text(group.name),
                      subtitle: Text(group.description.isEmpty
                          ? (l10n.isEn ? 'No description' : 'Без описания')
                          : group.description),
                      trailing: Wrap(
                        spacing: 8,
                        children: [
                          if (group.isPremium)
                            Chip(label: Text(l10n.fieldIsPremium)),
                          OutlinedButton(
                              onPressed: () => _editGroup(existing: group),
                              child: Text(l10n.edit)),
                          OutlinedButton(
                              onPressed: () => _deleteGroup(group),
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
