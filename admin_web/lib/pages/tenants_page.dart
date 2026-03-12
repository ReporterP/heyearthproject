import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';

class TenantsPage extends StatefulWidget {
  const TenantsPage({super.key, required this.token});

  final String token;

  @override
  State<TenantsPage> createState() => _TenantsPageState();
}

class _TenantsPageState extends State<TenantsPage> {
  List<TenantModel> _tenants = const [];
  bool _loading = true;
  int _page = 1;
  int _totalPages = 1;
  String _search = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      final result = await const AdminApiClient().fetchTenants(
        widget.token,
        page: _page,
        search: _search,
      );
      _tenants = result.items;
      _totalPages = result.totalPages;
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _editTenant({TenantModel? existing}) async {
    final nameC = TextEditingController(text: existing?.name ?? '');
    final slugC = TextEditingController(text: existing?.slug ?? '');
    final emailC = TextEditingController(text: existing?.contactEmail ?? '');
    final maxUsersC = TextEditingController(text: '${existing?.maxUsers ?? 10}');
    final maxServersC = TextEditingController(text: '${existing?.maxServers ?? 5}');
    final notesC = TextEditingController(text: existing?.billingNotes ?? '');
    bool useOurServers = existing?.useProviderServers ?? false;
    String status = existing?.status ?? 'inactive';
    String plan = existing?.plan ?? 'basic';

    final l10n = AdminL10n.of(context);
    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setD) => AlertDialog(
          title: Text(existing == null
              ? (l10n.isEn ? 'Create tenant' : 'Создать тенанта')
              : (l10n.isEn ? 'Edit tenant' : 'Редактировать тенанта')),
          content: SizedBox(
            width: 480,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextField(controller: nameC, decoration: InputDecoration(labelText: l10n.isEn ? 'Name' : 'Название')),
                  const SizedBox(height: 12),
                  TextField(controller: slugC, decoration: InputDecoration(labelText: 'Slug')),
                  const SizedBox(height: 12),
                  TextField(controller: emailC, decoration: InputDecoration(labelText: 'Email')),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    value: status,
                    decoration: InputDecoration(labelText: l10n.fieldStatus),
                    items: ['inactive', 'active', 'suspended']
                        .map((s) => DropdownMenuItem(value: s, child: Text(s)))
                        .toList(),
                    onChanged: (v) => setD(() => status = v ?? 'inactive'),
                  ),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    value: plan,
                    decoration: InputDecoration(labelText: l10n.isEn ? 'Plan' : 'Тариф'),
                    items: ['basic', 'premium', 'super']
                        .map((s) => DropdownMenuItem(value: s, child: Text(s)))
                        .toList(),
                    onChanged: (v) => setD(() => plan = v ?? 'basic'),
                  ),
                  const SizedBox(height: 12),
                  TextField(controller: maxUsersC, decoration: InputDecoration(labelText: l10n.isEn ? 'Max users' : 'Макс. пользователей'), keyboardType: TextInputType.number),
                  const SizedBox(height: 12),
                  TextField(controller: maxServersC, decoration: InputDecoration(labelText: l10n.isEn ? 'Max servers' : 'Макс. серверов'), keyboardType: TextInputType.number),
                  CheckboxListTile(
                    value: useOurServers,
                    onChanged: (v) => setD(() => useOurServers = v ?? false),
                    title: Text(l10n.isEn ? 'Use provider servers' : 'Наши серверы'),
                    contentPadding: EdgeInsets.zero,
                  ),
                  const SizedBox(height: 12),
                  TextField(controller: notesC, decoration: InputDecoration(labelText: l10n.isEn ? 'Billing notes' : 'Заметки'), maxLines: 2),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.of(ctx).pop(), child: Text(l10n.cancel)),
            FilledButton(
              onPressed: () => Navigator.of(ctx).pop(<String, dynamic>{
                'name': nameC.text.trim(),
                'slug': slugC.text.trim(),
                'contact_email': emailC.text.trim(),
                'status': status,
                'plan': plan,
                'max_users': int.tryParse(maxUsersC.text.trim()) ?? 10,
                'max_servers': int.tryParse(maxServersC.text.trim()) ?? 5,
                'use_provider_servers': useOurServers,
                'billing_notes': notesC.text.trim(),
              }),
              child: Text(l10n.save),
            ),
          ],
        ),
      ),
    );

    nameC.dispose();
    slugC.dispose();
    emailC.dispose();
    maxUsersC.dispose();
    maxServersC.dispose();
    notesC.dispose();
    if (payload == null) return;

    try {
      if (existing == null) {
        await const AdminApiClient().createTenant(widget.token, payload);
      } else {
        await const AdminApiClient().updateTenant(widget.token, existing.id, payload);
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteTenant(TenantModel t) async {
    final l10n = AdminL10n.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.delete),
        content: Text(l10n.isEn
            ? 'Delete tenant "${t.name}"? This cannot be undone.'
            : 'Удалить тенанта "${t.name}"? Действие необратимо.'),
        actions: [
          TextButton(onPressed: () => Navigator.of(ctx).pop(false), child: Text(l10n.cancel)),
          FilledButton(onPressed: () => Navigator.of(ctx).pop(true), child: Text(l10n.delete)),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await const AdminApiClient().deleteTenant(widget.token, t.id);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  void _showMessage(String msg) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Row(children: [
          Expanded(child: Text(l10n.isEn ? 'Tenants' : 'Тенанты', style: Theme.of(context).textTheme.headlineMedium)),
          SizedBox(
            width: 260,
            child: TextField(
              decoration: InputDecoration(
                hintText: l10n.isEn ? 'Search…' : 'Поиск…',
                prefixIcon: const Icon(Icons.search),
                isDense: true,
              ),
              onChanged: (v) {
                _search = v;
                _page = 1;
                _load();
              },
            ),
          ),
          const SizedBox(width: 12),
          FilledButton.icon(
            onPressed: () => _editTenant(),
            icon: const Icon(Icons.add),
            label: Text(l10n.create),
          ),
        ]),
        const SizedBox(height: 16),
        if (_loading)
          const Center(child: Padding(padding: EdgeInsets.all(32), child: CircularProgressIndicator()))
        else if (_tenants.isEmpty)
          Card(child: Padding(padding: const EdgeInsets.all(24), child: Text(l10n.isEn ? 'No tenants found' : 'Тенанты не найдены')))
        else
          ..._tenants.map((t) => Card(
                child: ListTile(
                  title: Text('${t.name} (${t.slug})'),
                  subtitle: Text('${t.plan} · ${t.status} · ${l10n.isEn ? "users" : "пользователей"}: ${t.maxUsers} · ${l10n.isEn ? "balance" : "баланс"}: ${t.balance.toStringAsFixed(2)} ₽'),
                  trailing: Wrap(
                    spacing: 8,
                    children: [
                      Chip(
                        label: Text(t.status),
                        backgroundColor: t.status == 'active' ? Colors.green.shade100 : null,
                      ),
                      OutlinedButton(onPressed: () => _editTenant(existing: t), child: Text(l10n.edit)),
                      OutlinedButton(onPressed: () => _deleteTenant(t), child: Text(l10n.delete)),
                    ],
                  ),
                ),
              )),
        if (!_loading && _totalPages > 1)
          Padding(
            padding: const EdgeInsets.only(top: 16),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                IconButton(onPressed: _page > 1 ? () { _page--; _load(); } : null, icon: const Icon(Icons.chevron_left)),
                Text('$_page / $_totalPages'),
                IconButton(onPressed: _page < _totalPages ? () { _page++; _load(); } : null, icon: const Icon(Icons.chevron_right)),
              ],
            ),
          ),
      ],
    );
  }
}
