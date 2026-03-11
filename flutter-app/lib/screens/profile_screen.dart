import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../l10n.dart';

class ProfileScreen extends StatefulWidget {
  const ProfileScreen({super.key});

  @override
  State<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends State<ProfileScreen> {
  final TextEditingController _promoController = TextEditingController();
  bool _promoLoading = false;

  @override
  void dispose() {
    _promoController.dispose();
    super.dispose();
  }

  Future<void> _applyPromo(AppState state) async {
    final code = _promoController.text.trim();
    if (code.isEmpty) {
      return;
    }
    setState(() => _promoLoading = true);
    final ok = await state.applyPromocode(code);
    if (!mounted) {
      return;
    }
    setState(() => _promoLoading = false);
    final l10n = L10n.of(context);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
          content: Text(ok
              ? l10n.promoApplied
              : l10n.translateError(state.error).isNotEmpty
                  ? l10n.translateError(state.error)
                  : l10n.promoFailed)),
    );
    if (ok) {
      _promoController.clear();
    }
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<AppState>(
      builder: (context, state, _) {
        final l10n = L10n.of(context);
        final profile = state.profile;
        final access = state.accessState;

        String _linked(bool v) =>
            v ? (l10n.isEn ? 'linked' : 'привязан') : (l10n.isEn ? 'not linked' : 'не привязан');
        String _notSet() => l10n.isEn ? 'not set' : 'не выбрана';
        String _notLimited() => l10n.isEn ? 'unlimited' : 'не ограничен';
        String _allowed(bool v) =>
            v ? (l10n.isEn ? 'allowed' : 'разрешено') : (l10n.isEn ? 'unavailable' : 'недоступно');

        return Scaffold(
          appBar: AppBar(title: Text(l10n.profileScreenTitle)),
          body: RefreshIndicator(
            onRefresh: () => state.refreshAccountData(),
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Card(
                  child: ListTile(
                    leading: const Icon(Icons.badge_outlined),
                    title: const Text('HeYearth ID'),
                    subtitle: Text(state.userId ?? 'Not set'),
                  ),
                ),
                if (profile != null) ...[
                  const SizedBox(height: 12),
                  Card(
                    child: ListTile(
                      leading: const Icon(Icons.person_outline),
                      title: Text(l10n.profileSection),
                      subtitle: Text(
                        '${l10n.platformLabel}: ${profile.preferredPlatform.isEmpty ? _notSet() : profile.preferredPlatform}\n'
                        '${l10n.telegramLabel}: ${_linked(profile.telegramLinked)}\n'
                        '${l10n.statusLabel}: ${profile.accessStatus.isEmpty ? (l10n.isEn ? 'active' : 'активен') : profile.accessStatus}',
                      ),
                    ),
                  ),
                ],
                if (access != null) ...[
                  const SizedBox(height: 12),
                  Card(
                    child: ListTile(
                      leading: const Icon(Icons.workspace_premium_outlined),
                      title: Text(l10n.accessSection),
                      subtitle: Text(
                        '${l10n.accessPlan}: ${access.currentPlan.isEmpty ? 'Free' : access.currentPlan}\n'
                        '${l10n.accessValidUntil}: ${access.validUntil.isEmpty ? _notLimited() : access.validUntil}\n'
                        '${l10n.accessConnection}: ${_allowed(access.canConnect)}',
                      ),
                    ),
                  ),
                ],
                const SizedBox(height: 16),
                Text(l10n.subscriptionsSection,
                    style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                if (state.subscriptions.isEmpty)
                  Card(
                    child: Padding(
                      padding: const EdgeInsets.all(16),
                      child: Text(l10n.noSubscriptions),
                    ),
                  ),
                for (final item in state.subscriptions)
                  Card(
                    child: ListTile(
                      title: Text(item.groupName),
                      subtitle: Text(
                        '${l10n.untilLabel}: ${item.validUntil.isEmpty ? _notLimited() : item.validUntil}\n'
                        'Auto renew: ${item.autoRenew ? 'yes' : 'no'}\n'
                        'Active: ${item.active ? 'yes' : 'no'}',
                      ),
                      isThreeLine: true,
                    ),
                  ),
                const SizedBox(height: 16),
                Text(l10n.paymentsSection,
                    style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                if (state.payments.isEmpty)
                  Card(
                    child: Padding(
                      padding: const EdgeInsets.all(16),
                      child: Text(l10n.noPayments),
                    ),
                  ),
                for (final item in state.payments)
                  Card(
                    child: ListTile(
                      title: Text('${item.amount} ${item.currency}'),
                      subtitle: Text(
                        '${l10n.paymentStatus}: ${item.status}\n'
                        'Provider: ${item.provider}\n'
                        '${l10n.paymentDate}: ${item.createdAt}',
                      ),
                      isThreeLine: true,
                    ),
                  ),
                const SizedBox(height: 16),
                Text(l10n.promoCodeSection,
                    style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        controller: _promoController,
                        decoration: InputDecoration(
                          hintText: l10n.promoCodeHint,
                          border: const OutlineInputBorder(),
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    FilledButton(
                      onPressed:
                          _promoLoading ? null : () => _applyPromo(state),
                      child: _promoLoading
                          ? const SizedBox(
                              width: 20,
                              height: 20,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : Text(l10n.applyButton),
                    ),
                  ],
                ),
                const SizedBox(height: 16),
                if (state.error != null)
                  Card(
                    color: Theme.of(context).colorScheme.errorContainer,
                    child: Padding(
                      padding: const EdgeInsets.all(16),
                      child: Text(
                        l10n.translateError(state.error),
                        style: TextStyle(
                            color:
                                Theme.of(context).colorScheme.onErrorContainer),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        );
      },
    );
  }
}
