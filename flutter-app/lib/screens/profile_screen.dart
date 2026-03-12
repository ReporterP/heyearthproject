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

  void _showAddAccountDialog(BuildContext context, AppState state) {
    final l10n = L10n.of(context);
    final controller = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.addAccount),
        content: TextField(
          controller: controller,
          textCapitalization: TextCapitalization.characters,
          decoration: InputDecoration(
            hintText: l10n.addAccountHint,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.cancel),
          ),
          FilledButton(
            onPressed: () async {
              final input = controller.text.trim();
              if (input.isEmpty) return;
              Navigator.of(ctx).pop();
              final ok = input.length <= 12 &&
                      RegExp(r'^[A-Za-z0-9]+$').hasMatch(input)
                  ? await state.claimByCode(input)
                  : await state.restoreUserId(input);
              if (!context.mounted) return;
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                  content: Text(
                    ok
                        ? l10n.accountRestored
                        : l10n.translateError(state.error).isNotEmpty
                            ? l10n.translateError(state.error)
                            : l10n.loginFailed,
                  ),
                ),
              );
            },
            child: Text(l10n.login),
          ),
        ],
      ),
    );
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
                _AccountsSection(
                  savedAccounts: state.savedAccounts,
                  currentUserId: state.userId,
                  onSwitchAccount: state.switchAccount,
                  onRemoveAccount: state.removeAccount,
                  onAddAccount: () => _showAddAccountDialog(context, state),
                ),
                const SizedBox(height: 12),
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

class _AccountsSection extends StatelessWidget {
  const _AccountsSection({
    required this.savedAccounts,
    required this.currentUserId,
    required this.onSwitchAccount,
    required this.onRemoveAccount,
    required this.onAddAccount,
  });

  final List<String> savedAccounts;
  final String? currentUserId;
  final void Function(String) onSwitchAccount;
  final void Function(String) onRemoveAccount;
  final VoidCallback onAddAccount;

  @override
  Widget build(BuildContext context) {
    final l10n = L10n.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  l10n.accountsSection,
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                FilledButton.icon(
                  onPressed: onAddAccount,
                  icon: const Icon(Icons.add, size: 20),
                  label: Text(l10n.addAccount),
                ),
              ],
            ),
            if (savedAccounts.isEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 12),
                child: Text(
                  l10n.isEn
                      ? 'No saved accounts. Add one above.'
                      : 'Нет сохранённых аккаунтов. Добавьте выше.',
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
              )
            else
              ...savedAccounts.map((id) {
                final isCurrent = id == currentUserId;
                return ListTile(
                  leading: Icon(
                    isCurrent ? Icons.person : Icons.person_outline,
                    color: isCurrent
                        ? Theme.of(context).colorScheme.primary
                        : null,
                  ),
                  title: Text(
                    id,
                    style: TextStyle(
                      fontWeight:
                          isCurrent ? FontWeight.w600 : FontWeight.normal,
                    color: isCurrent
                        ? Theme.of(context).colorScheme.primary
                        : null,
                    overflow: TextOverflow.ellipsis,
                    maxLines: 1,
                  ),
                  subtitle: isCurrent ? Text(l10n.currentAccount) : null,
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (!isCurrent)
                        TextButton(
                          onPressed: () => onSwitchAccount(id),
                          child: Text(l10n.switchAccount),
                        ),
                      IconButton(
                        icon: const Icon(Icons.delete_outline, size: 20),
                        onPressed: () => onRemoveAccount(id),
                        tooltip: l10n.removeAccount,
                      ),
                    ],
                  ),
                );
              }),
          ],
        ),
      ),
    );
  }
}
