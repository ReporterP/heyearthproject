import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key, required this.token});

  final String token;

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  DashboardOverview? _overview;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      _overview = await const AdminApiClient().fetchOverview(widget.token);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    final overview = _overview;
    if (overview == null) {
      return Center(
        child: FilledButton(
          onPressed: _load,
          child: Text(l10n.retry),
        ),
      );
    }
    final items = <({String label, int value})>[
      (label: l10n.dashUsers, value: overview.usersCount),
      (label: l10n.dashActiveUsers, value: overview.activeUsersCount),
      (label: l10n.dashServers, value: overview.serversCount),
      (label: l10n.dashOnlineServers, value: overview.onlineServersCount),
      (label: l10n.dashGroups, value: overview.groupsCount),
      (label: l10n.dashPromocodes, value: overview.promocodesCount),
      (label: l10n.dashPayments, value: overview.paymentsCount),
      (label: l10n.dashActiveReleases, value: overview.activeReleases),
      (label: l10n.dashAdmins, value: overview.adminsCount),
    ];

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Text(l10n.dashboardTitle,
              style: Theme.of(context).textTheme.headlineMedium),
          const SizedBox(height: 16),
          Wrap(
            spacing: 16,
            runSpacing: 16,
            children: items
                .map(
                  (item) => SizedBox(
                    width: 240,
                    child: Card(
                      child: Padding(
                        padding: const EdgeInsets.all(20),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(item.label,
                                style: Theme.of(context).textTheme.titleMedium),
                            const SizedBox(height: 8),
                            Text('${item.value}',
                                style:
                                    Theme.of(context).textTheme.displaySmall),
                          ],
                        ),
                      ),
                    ),
                  ),
                )
                .toList(growable: false),
          ),
        ],
      ),
    );
  }
}
