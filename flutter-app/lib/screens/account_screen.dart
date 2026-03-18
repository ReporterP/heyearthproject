import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../l10n.dart';
import 'payment_screen.dart';

class AccountScreen extends StatefulWidget {
  const AccountScreen({super.key});

  @override
  State<AccountScreen> createState() => _AccountScreenState();
}

class _AccountScreenState extends State<AccountScreen> {
  final _usernameController = TextEditingController();
  final _newPasswordController = TextEditingController();
  final _confirmPasswordController = TextEditingController();
  final _currentPasswordController = TextEditingController();
  bool _savingUsername = false;
  bool _savingPassword = false;

  @override
  void dispose() {
    _usernameController.dispose();
    _newPasswordController.dispose();
    _confirmPasswordController.dispose();
    _currentPasswordController.dispose();
    super.dispose();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final state = context.read<AppState>();
    if (_usernameController.text.isEmpty && state.username != null) {
      _usernameController.text = state.username!;
    }
  }

  Future<void> _saveUsername(AppState state) async {
    final username = _usernameController.text.trim();
    if (username.isEmpty) return;
    setState(() => _savingUsername = true);
    final ok = await state.setUsername(username);
    if (!mounted) return;
    setState(() => _savingUsername = false);
    final l10n = L10n.of(context);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(ok
            ? l10n.usernameSaved
            : l10n.translateError(state.error).isNotEmpty
                ? l10n.translateError(state.error)
                : l10n.error),
      ),
    );
  }

  Future<void> _savePassword(AppState state) async {
    final l10n = L10n.of(context);
    final newPw = _newPasswordController.text;
    final confirmPw = _confirmPasswordController.text;
    final currentPw = _currentPasswordController.text.trim();

    if (newPw.isEmpty) return;
    if (newPw != confirmPw) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.passwordMismatch)),
      );
      return;
    }

    setState(() => _savingPassword = true);
    final ok = await state.setCloudPassword(
      newPw,
      currentPw.isNotEmpty ? currentPw : null,
    );
    if (!mounted) return;
    setState(() => _savingPassword = false);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(ok
            ? l10n.passwordSaved
            : l10n.translateError(state.error).isNotEmpty
                ? l10n.translateError(state.error)
                : l10n.error),
      ),
    );
    if (ok) {
      _newPasswordController.clear();
      _confirmPasswordController.clear();
      _currentPasswordController.clear();
    }
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<AppState>(
      builder: (context, state, _) {
        final l10n = L10n.of(context);
        final access = state.accessState;

        return Scaffold(
          appBar: AppBar(title: Text(l10n.accountScreenTitle)),
          body: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              // ── HeYearth ID ────────────────────────────────────────
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l10n.yourHeyearthId,
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 8),
                      Row(
                        children: [
                          Expanded(
                            child: SelectableText(
                              state.userId ?? '',
                              style: Theme.of(context)
                                  .textTheme
                                  .titleLarge
                                  ?.copyWith(fontFamily: 'monospace'),
                            ),
                          ),
                          IconButton(
                            onPressed: () {
                              Clipboard.setData(
                                  ClipboardData(text: state.userId ?? ''));
                              ScaffoldMessenger.of(context).showSnackBar(
                                SnackBar(content: Text(l10n.idCopied)),
                              );
                            },
                            icon: const Icon(Icons.copy_outlined),
                          ),
                        ],
                      ),
                      const SizedBox(height: 8),
                      Row(
                        children: [
                          Icon(Icons.warning_amber_rounded,
                              size: 18,
                              color: Theme.of(context).colorScheme.error),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Text(
                              l10n.saveYourIdWarning,
                              style: Theme.of(context)
                                  .textTheme
                                  .bodySmall
                                  ?.copyWith(
                                      color:
                                          Theme.of(context).colorScheme.error),
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ── Username ───────────────────────────────────────────
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l10n.usernameLabel,
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 8),
                      Row(
                        children: [
                          Expanded(
                            child: TextField(
                              controller: _usernameController,
                              decoration: InputDecoration(
                                hintText: l10n.usernameHint,
                                border: const OutlineInputBorder(),
                              ),
                            ),
                          ),
                          const SizedBox(width: 12),
                          FilledButton(
                            onPressed: _savingUsername
                                ? null
                                : () => _saveUsername(state),
                            child: _savingUsername
                                ? const SizedBox(
                                    width: 20,
                                    height: 20,
                                    child: CircularProgressIndicator(
                                        strokeWidth: 2),
                                  )
                                : Text(l10n.saveButton),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ── Cloud Password ─────────────────────────────────────
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l10n.cloudPasswordLabel,
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 8),
                      if (state.hasPassword) ...[
                        TextField(
                          controller: _currentPasswordController,
                          obscureText: true,
                          decoration: InputDecoration(
                            hintText: l10n.currentPasswordHint,
                            border: const OutlineInputBorder(),
                          ),
                        ),
                        const SizedBox(height: 8),
                      ],
                      TextField(
                        controller: _newPasswordController,
                        obscureText: true,
                        decoration: InputDecoration(
                          hintText: l10n.newPasswordHint,
                          border: const OutlineInputBorder(),
                        ),
                      ),
                      const SizedBox(height: 8),
                      TextField(
                        controller: _confirmPasswordController,
                        obscureText: true,
                        decoration: InputDecoration(
                          hintText: l10n.confirmPasswordHint,
                          border: const OutlineInputBorder(),
                        ),
                      ),
                      const SizedBox(height: 12),
                      FilledButton(
                        onPressed: _savingPassword
                            ? null
                            : () => _savePassword(state),
                        child: _savingPassword
                            ? const SizedBox(
                                width: 20,
                                height: 20,
                                child:
                                    CircularProgressIndicator(strokeWidth: 2),
                              )
                            : Text(l10n.saveButton),
                      ),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ── Subscription status ────────────────────────────────
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l10n.subscriptionSection,
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 8),
                      if (access != null) ...[
                        Text(
                          '${l10n.planLabel}: ${access.currentPlan.isEmpty ? 'Free' : access.currentPlan}',
                          style: Theme.of(context).textTheme.bodyLarge,
                        ),
                        const SizedBox(height: 4),
                        Text(
                          '${l10n.validUntilLabel}: ${access.validUntil.isEmpty ? (l10n.isEn ? 'unlimited' : 'не ограничен') : access.validUntil}',
                        ),
                        if (state.isPremium)
                          Padding(
                            padding: const EdgeInsets.only(top: 4),
                            child: Chip(
                              label: Text('Premium'),
                              backgroundColor: Theme.of(context)
                                  .colorScheme
                                  .primaryContainer,
                            ),
                          ),
                      ] else
                        Text(l10n.noActiveSubscription),
                      const SizedBox(height: 12),
                      FilledButton.icon(
                        onPressed: () {
                          Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => PaymentScreen(
                                userId: state.userId!,
                                apiBase: state.apiBase,
                                jwtToken: state.jwtToken,
                                onPaymentComplete: () =>
                                    state.refreshAccountData(),
                                onApplyPromocode: (code) async {
                                  try {
                                    await state.applyPromocode(code);
                                    return true;
                                  } catch (_) {
                                    return false;
                                  }
                                },
                              ),
                            ),
                          );
                        },
                        icon: const Icon(Icons.payment),
                        label: Text(l10n.payButton),
                      ),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ── Payment history ────────────────────────────────────
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l10n.paymentsSection,
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 8),
                      if (state.payments.isEmpty)
                        Text(l10n.noPayments)
                      else
                        ...state.payments.map((item) => Padding(
                              padding: const EdgeInsets.only(bottom: 8),
                              child: Row(
                                mainAxisAlignment:
                                    MainAxisAlignment.spaceBetween,
                                children: [
                                  Expanded(
                                    child: Text(
                                      '${item.amount} ${item.currency}',
                                      style: Theme.of(context)
                                          .textTheme
                                          .bodyMedium,
                                    ),
                                  ),
                                  Text(
                                    item.status,
                                    style:
                                        Theme.of(context).textTheme.bodySmall,
                                  ),
                                  const SizedBox(width: 8),
                                  Text(
                                    item.createdAt,
                                    style:
                                        Theme.of(context).textTheme.bodySmall,
                                  ),
                                ],
                              ),
                            )),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ── Connection modes (VPN / SOCKS5 / HTTP) ─────────────
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l10n.connectionModesSection,
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 8),
                      if (state.connectionOptions.isEmpty)
                        Text(l10n.isEn
                            ? 'No connection options available. Enable VPN to load them.'
                            : 'Нет доступных режимов подключения. Включите VPN, чтобы загрузить их.')
                      else
                        ...state.connectionOptions.map((opt) {
                          // label field will be available after proto regeneration
                          String optLabel;
                          try {
                            final l = (opt as dynamic).label as String?;
                            optLabel = (l != null && l.isNotEmpty) ? l : '';
                          } catch (_) {
                            optLabel = '';
                          }
                          final label = optLabel.isNotEmpty
                              ? optLabel
                              : opt.protocol.isNotEmpty
                                  ? opt.protocol
                                  : 'Connection';
                          return Padding(
                            padding: const EdgeInsets.only(bottom: 8),
                            child: Card(
                              child: ListTile(
                                leading:
                                    const Icon(Icons.lan_outlined),
                                title: Text(label),
                                subtitle: Text(
                                  opt.uri.length > 40
                                      ? '${opt.uri.substring(0, 37)}...'
                                      : opt.uri,
                                  style: const TextStyle(
                                      fontFamily: 'monospace', fontSize: 12),
                                ),
                                trailing: IconButton(
                                  icon: const Icon(Icons.copy_outlined),
                                  onPressed: () {
                                    Clipboard.setData(
                                        ClipboardData(text: opt.uri));
                                    ScaffoldMessenger.of(context).showSnackBar(
                                      SnackBar(
                                          content: Text(l10n.uriCopied)),
                                    );
                                  },
                                  tooltip: l10n.copyUri,
                                ),
                              ),
                            ),
                          );
                        }),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ── Error ──────────────────────────────────────────────
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
        );
      },
    );
  }
}
