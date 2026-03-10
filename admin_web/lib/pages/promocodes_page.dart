import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class PromocodesPage extends StatefulWidget {
  const PromocodesPage({super.key, required this.token});

  final String token;

  @override
  State<PromocodesPage> createState() => _PromocodesPageState();
}

class _PromocodesPageState extends State<PromocodesPage> {
  List<PromocodeModel> _promocodes = const [];
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
      _promocodes = await client.fetchPromocodes(widget.token);
      _groups = await client.fetchGroups(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _editPromocode({PromocodeModel? existing}) async {
    final codeController = TextEditingController(text: existing?.code ?? '');
    int? groupId = existing?.groupIdGranted;
    final durationController =
        TextEditingController(text: '${existing?.durationDays ?? 30}');
    final maxUsesController =
        TextEditingController(text: '${existing?.maxUses ?? 1}');
    final expiresAtController =
        TextEditingController(text: existing?.expiresAt ?? '');

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create promocode' : 'Edit promocode'),
          content: SizedBox(
            width: 460,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                    controller: codeController,
                    enabled: existing == null,
                    decoration: const InputDecoration(labelText: 'Code')),
                const SizedBox(height: 12),
                DropdownButtonFormField<int>(
                  initialValue: groupId,
                  items: _groups
                      .map((group) => DropdownMenuItem<int>(
                          value: group.id, child: Text(group.name)))
                      .toList(growable: false),
                  onChanged: (value) => setStateDialog(() => groupId = value),
                  decoration: const InputDecoration(labelText: 'Granted group'),
                ),
                const SizedBox(height: 12),
                TextField(
                    controller: durationController,
                    decoration:
                        const InputDecoration(labelText: 'Duration days')),
                const SizedBox(height: 12),
                TextField(
                    controller: maxUsesController,
                    decoration: const InputDecoration(labelText: 'Max uses')),
                const SizedBox(height: 12),
                TextField(
                    controller: expiresAtController,
                    decoration: const InputDecoration(
                        labelText: 'Expires at (RFC3339)')),
              ],
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Cancel')),
            FilledButton(
              onPressed: groupId == null
                  ? null
                  : () => Navigator.of(context).pop(<String, dynamic>{
                        'code': codeController.text.trim(),
                        'group_id_granted': groupId,
                        'duration_days':
                            int.tryParse(durationController.text.trim()) ?? 30,
                        'max_uses':
                            int.tryParse(maxUsesController.text.trim()) ?? 1,
                        'expires_at': expiresAtController.text.trim(),
                      }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    codeController.dispose();
    durationController.dispose();
    maxUsesController.dispose();
    expiresAtController.dispose();
    if (payload == null) {
      return;
    }
    try {
      if (existing == null) {
        await const AdminApiClient().createPromocode(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updatePromocode(widget.token, existing.code, payload);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deletePromocode(PromocodeModel item) async {
    try {
      await const AdminApiClient().deletePromocode(widget.token, item.code);
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
                  child: Text('Promocodes',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                  onPressed: () => _editPromocode(),
                  icon: const Icon(Icons.add),
                  label: const Text('Create')),
            ],
          ),
          const SizedBox(height: 16),
          if (_promocodes.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No promocodes found'),
              ),
            ),
          for (final item in _promocodes)
            Card(
              child: ListTile(
                title: Text(item.code),
                subtitle: Text(
                    'Group: ${item.groupName} | ${item.currentUses}/${item.maxUses} uses\nDuration: ${item.durationDays} days'),
                isThreeLine: true,
                trailing: Wrap(
                  spacing: 8,
                  children: [
                    OutlinedButton(
                        onPressed: () => _editPromocode(existing: item),
                        child: const Text('Edit')),
                    OutlinedButton(
                        onPressed: () => _deletePromocode(item),
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
