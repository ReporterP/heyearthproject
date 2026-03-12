import 'dart:convert';

import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';
import '../widgets/search_pagination_bar.dart';

class PaymentsPage extends StatefulWidget {
  const PaymentsPage({super.key, required this.token});

  final String token;

  @override
  State<PaymentsPage> createState() => _PaymentsPageState();
}

class _PaymentsPageState extends State<PaymentsPage> {
  static const _pageSize = 20;

  final _searchController = TextEditingController();
  String _statusFilter = '';
  List<PaymentModel> _payments = const [];
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
      final client = const AdminApiClient();
      final results = await Future.wait([
        client.fetchPaymentsPaged(
          widget.token,
          page: _page,
          pageSize: _pageSize,
          search: _searchController.text.trim(),
          status: _statusFilter,
        ),
        client.fetchUsers(widget.token, pageSize: 100),
      ]);
      final paged = results[0] as PagedResult<PaymentModel>;
      _payments = paged.items;
      _total = paged.total;
      _users = results[1] as List<ManagedUser>;
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _editPayment({PaymentModel? existing}) async {
    int? userDbId = existing?.userDbId;
    final amountController =
        TextEditingController(text: '${existing?.amount ?? 0}');
    final currencyController =
        TextEditingController(text: existing?.currency ?? 'USD');
    final statusController =
        TextEditingController(text: existing?.status ?? 'pending');
    final providerController =
        TextEditingController(text: existing?.provider ?? '');
    final externalIdController =
        TextEditingController(text: existing?.externalId ?? '');
    final notesController = TextEditingController(text: existing?.notes ?? '');
    final metadataController = TextEditingController(
      text: existing == null ? '{}' : jsonEncode(existing.metadata),
    );

    final payload = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null ? 'Create payment' : 'Edit payment'),
          content: SizedBox(
            width: 520,
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
                TextField(
                    controller: amountController,
                    decoration: const InputDecoration(labelText: 'Amount')),
                const SizedBox(height: 12),
                TextField(
                    controller: currencyController,
                    decoration: const InputDecoration(labelText: 'Currency')),
                const SizedBox(height: 12),
                TextField(
                    controller: statusController,
                    decoration: const InputDecoration(labelText: 'Status')),
                const SizedBox(height: 12),
                TextField(
                    controller: providerController,
                    decoration: const InputDecoration(labelText: 'Provider')),
                const SizedBox(height: 12),
                TextField(
                    controller: externalIdController,
                    decoration:
                        const InputDecoration(labelText: 'External ID')),
                const SizedBox(height: 12),
                TextField(
                    controller: notesController,
                    decoration: const InputDecoration(labelText: 'Notes')),
                const SizedBox(height: 12),
                TextField(
                    controller: metadataController,
                    maxLines: 3,
                    decoration:
                        const InputDecoration(labelText: 'Metadata JSON')),
              ],
            ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: Text(AdminL10n.of(context).cancel)),
            FilledButton(
              onPressed: userDbId == null
                  ? null
                  : () => Navigator.of(context).pop(<String, dynamic>{
                        'user_db_id': userDbId,
                        'amount':
                            double.tryParse(amountController.text.trim()) ?? 0,
                        'currency': currencyController.text.trim(),
                        'status': statusController.text.trim(),
                        'provider': providerController.text.trim(),
                        'external_id': externalIdController.text.trim(),
                        'notes': notesController.text.trim(),
                        'metadata': jsonDecode(
                            metadataController.text.trim().isEmpty
                                ? '{}'
                                : metadataController.text.trim()),
                      }),
              child: const Text('Save'),
            ),
          ],
        ),
      ),
    );

    amountController.dispose();
    currencyController.dispose();
    statusController.dispose();
    providerController.dispose();
    externalIdController.dispose();
    notesController.dispose();
    metadataController.dispose();
    if (payload == null) {
      return;
    }
    try {
      if (existing == null) {
        await const AdminApiClient().createPayment(widget.token, payload);
      } else {
        await const AdminApiClient()
            .updatePayment(widget.token, existing.id, payload);
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
    final l10n = AdminL10n.of(context);
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(children: [
            Expanded(child: Text(l10n.paymentsTitle, style: Theme.of(context).textTheme.headlineMedium)),
            FilledButton.icon(onPressed: () => _editPayment(), icon: const Icon(Icons.add), label: Text(l10n.create)),
          ]),
          const SizedBox(height: 16),
          SearchPaginationBar(
            searchController: _searchController,
            onSearch: () { _page = 1; _load(); },
            page: _page,
            totalPages: _totalPages,
            total: _total,
            onPageChanged: (p) { _page = p; _load(); },
            searchHint: l10n.isEn ? 'Search by user_id, external_id…' : 'Поиск по user_id, external_id…',
            trailing: DropdownButton<String>(
              value: _statusFilter,
              underline: const SizedBox.shrink(),
              hint: Text(l10n.isEn ? 'Status' : 'Статус'),
              items: [
                DropdownMenuItem(value: '', child: Text(l10n.isEn ? 'All' : 'Все')),
                const DropdownMenuItem(value: 'pending', child: Text('Pending')),
                const DropdownMenuItem(value: 'completed', child: Text('Completed')),
                const DropdownMenuItem(value: 'failed', child: Text('Failed')),
                const DropdownMenuItem(value: 'refunded', child: Text('Refunded')),
              ],
              onChanged: (v) { _statusFilter = v ?? ''; _page = 1; _load(); },
            ),
          ),
          const SizedBox(height: 8),
          if (_loading)
            const Expanded(child: Center(child: CircularProgressIndicator()))
          else if (_payments.isEmpty)
            Expanded(child: Center(child: Text(l10n.noPayments, style: Theme.of(context).textTheme.bodyLarge)))
          else
            Expanded(
              child: ListView.builder(
                itemCount: _payments.length,
                itemBuilder: (context, index) {
                  final item = _payments[index];
                  return Card(
                    child: ListTile(
                      title: Text('${item.amount} ${item.currency} • ${item.status}'),
                      subtitle: Text('${item.userId}\nProvider: ${item.provider.isEmpty ? 'manual' : item.provider}${item.externalId.isEmpty ? '' : '\nExternal ID: ${item.externalId}'}'),
                      isThreeLine: true,
                      trailing: OutlinedButton(onPressed: () => _editPayment(existing: item), child: Text(l10n.edit)),
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
