import 'dart:convert';

import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../models.dart';

class PaymentsPage extends StatefulWidget {
  const PaymentsPage({super.key, required this.token});

  final String token;

  @override
  State<PaymentsPage> createState() => _PaymentsPageState();
}

class _PaymentsPageState extends State<PaymentsPage> {
  List<PaymentModel> _payments = const [];
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
      final client = const AdminApiClient();
      _payments = await client.fetchPayments(widget.token);
      _users = await client.fetchUsers(widget.token);
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
                child: const Text('Cancel')),
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
                  child: Text('Payments',
                      style: Theme.of(context).textTheme.headlineMedium)),
              FilledButton.icon(
                  onPressed: () => _editPayment(),
                  icon: const Icon(Icons.add),
                  label: const Text('Create')),
            ],
          ),
          const SizedBox(height: 16),
          if (_payments.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No payments found'),
              ),
            ),
          for (final item in _payments)
            Card(
              child: ListTile(
                title: Text('${item.amount} ${item.currency} • ${item.status}'),
                subtitle: Text(
                    '${item.userId}\nProvider: ${item.provider.isEmpty ? 'manual' : item.provider}${item.externalId.isEmpty ? '' : '\nExternal ID: ${item.externalId}'}'),
                isThreeLine: true,
                trailing: OutlinedButton(
                  onPressed: () => _editPayment(existing: item),
                  child: const Text('Edit'),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
