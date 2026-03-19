import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';

class TrafficMaskingPage extends StatefulWidget {
  const TrafficMaskingPage({super.key, required this.token});

  final String token;

  @override
  State<TrafficMaskingPage> createState() => _TrafficMaskingPageState();
}

class _TrafficMaskingPageState extends State<TrafficMaskingPage> {
  List<TrafficMaskingRule> _rules = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      _rules = await const AdminApiClient()
          .fetchTrafficMaskingRules(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _editRule({TrafficMaskingRule? existing}) async {
    final sourceDomainController =
        TextEditingController(text: existing?.sourceDomain ?? '');
    final maskDomainController =
        TextEditingController(text: existing?.maskDomain ?? '');
    bool isActive = existing?.isActive ?? true;

    final l10n = AdminL10n.of(context);
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setStateDialog) => AlertDialog(
          title: Text(existing == null
              ? l10n.trafficMaskingCreate
              : l10n.trafficMaskingEdit),
          content: SizedBox(
            width: 480,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: sourceDomainController,
                  decoration: InputDecoration(
                      labelText: l10n.trafficMaskingSourceDomain),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: maskDomainController,
                  decoration: InputDecoration(
                      labelText: l10n.trafficMaskingMaskDomain),
                ),
                const SizedBox(height: 12),
                SwitchListTile(
                  value: isActive,
                  onChanged: (v) => setStateDialog(() => isActive = v),
                  title: Text(l10n.trafficMaskingActive),
                  contentPadding: EdgeInsets.zero,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: Text(l10n.cancel),
            ),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(<String, dynamic>{
                'source_domain': sourceDomainController.text.trim(),
                'mask_domain': maskDomainController.text.trim(),
                'is_active': isActive,
              }),
              child: Text(l10n.save),
            ),
          ],
        ),
      ),
    );

    sourceDomainController.dispose();
    maskDomainController.dispose();
    if (result == null) return;

    try {
      if (existing == null) {
        await const AdminApiClient().createTrafficMaskingRule(
          widget.token,
          sourceDomain: result['source_domain'] as String,
          maskDomain: result['mask_domain'] as String,
          isActive: result['is_active'] as bool,
        );
      } else {
        await const AdminApiClient().updateTrafficMaskingRule(
          widget.token,
          existing.id,
          sourceDomain: result['source_domain'] as String,
          maskDomain: result['mask_domain'] as String,
          isActive: result['is_active'] as bool,
        );
      }
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _deleteRule(TrafficMaskingRule rule) async {
    final l10n = AdminL10n.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.trafficMaskingDelete),
        content: Text(l10n.trafficMaskingDeleteConfirm(rule.sourceDomain)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l10n.cancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(l10n.delete),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    try {
      await const AdminApiClient()
          .deleteTrafficMaskingRule(widget.token, rule.id);
      await _load();
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    }
  }

  Future<void> _toggleActive(TrafficMaskingRule rule) async {
    try {
      await const AdminApiClient().updateTrafficMaskingRule(
        widget.token,
        rule.id,
        isActive: !rule.isActive,
      );
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
            Expanded(
              child: Text(l10n.trafficMaskingTitle,
                  style: Theme.of(context).textTheme.headlineMedium),
            ),
            FilledButton.icon(
              onPressed: () => _editRule(),
              icon: const Icon(Icons.add),
              label: Text(l10n.create),
            ),
          ]),
          const SizedBox(height: 16),
          if (_loading)
            const Expanded(
                child: Center(child: CircularProgressIndicator()))
          else if (_rules.isEmpty)
            Expanded(
              child: Center(
                child: Text(l10n.trafficMaskingNoRules,
                    style: Theme.of(context).textTheme.bodyLarge),
              ),
            )
          else
            Expanded(
              child: SingleChildScrollView(
                child: DataTable(
                  columns: [
                    DataColumn(
                        label: Text(l10n.trafficMaskingSourceDomain)),
                    DataColumn(
                        label: Text(l10n.trafficMaskingMaskDomain)),
                    DataColumn(
                        label: Text(l10n.trafficMaskingActive)),
                    DataColumn(label: Text(l10n.isEn ? 'Actions' : 'Действия')),
                  ],
                  rows: _rules.map((rule) {
                    return DataRow(cells: [
                      DataCell(Text(rule.sourceDomain)),
                      DataCell(Text(rule.maskDomain)),
                      DataCell(
                        Switch(
                          value: rule.isActive,
                          onChanged: (_) => _toggleActive(rule),
                        ),
                      ),
                      DataCell(
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            IconButton(
                              icon: const Icon(Icons.edit, size: 18),
                              tooltip: l10n.edit,
                              onPressed: () =>
                                  _editRule(existing: rule),
                            ),
                            IconButton(
                              icon: const Icon(Icons.delete, size: 18),
                              tooltip: l10n.delete,
                              color: Colors.red,
                              onPressed: () => _deleteRule(rule),
                            ),
                          ],
                        ),
                      ),
                    ]);
                  }).toList(growable: false),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
