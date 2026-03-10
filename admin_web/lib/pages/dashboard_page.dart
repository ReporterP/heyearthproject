import 'package:flutter/material.dart';

import '../admin_api_client.dart';
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
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    final overview = _overview;
    if (overview == null) {
      return Center(
        child: FilledButton(
          onPressed: _load,
          child: const Text('Retry'),
        ),
      );
    }

    final items = <({String label, int value})>[
      (label: 'Users', value: overview.usersCount),
      (label: 'Active users', value: overview.activeUsersCount),
      (label: 'Servers', value: overview.serversCount),
      (label: 'Online servers', value: overview.onlineServersCount),
      (label: 'Groups', value: overview.groupsCount),
      (label: 'Promocodes', value: overview.promocodesCount),
      (label: 'Payments', value: overview.paymentsCount),
      (label: 'Active releases', value: overview.activeReleases),
      (label: 'Admins', value: overview.adminsCount),
    ];

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Text('Dashboard', style: Theme.of(context).textTheme.headlineMedium),
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
