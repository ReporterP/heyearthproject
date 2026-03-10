import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';

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
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
          content: Text(ok
              ? 'Промокод применён.'
              : (state.error ?? 'Не удалось применить промокод.'))),
    );
    if (ok) {
      _promoController.clear();
    }
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<AppState>(
      builder: (context, state, _) {
        final profile = state.profile;
        final access = state.accessState;
        return Scaffold(
          appBar: AppBar(title: const Text('Профиль и доступ')),
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
                      title: const Text('Профиль'),
                      subtitle: Text(
                        'Платформа: ${profile.preferredPlatform.isEmpty ? 'не выбрана' : profile.preferredPlatform}\n'
                        'Telegram: ${profile.telegramLinked ? 'привязан' : 'не привязан'}\n'
                        'Статус: ${profile.accessStatus.isEmpty ? 'active' : profile.accessStatus}',
                      ),
                    ),
                  ),
                ],
                if (access != null) ...[
                  const SizedBox(height: 12),
                  Card(
                    child: ListTile(
                      leading: const Icon(Icons.workspace_premium_outlined),
                      title: const Text('Доступ'),
                      subtitle: Text(
                        'План: ${access.currentPlan.isEmpty ? 'Free' : access.currentPlan}\n'
                        'Действует до: ${access.validUntil.isEmpty ? 'не ограничен' : access.validUntil}\n'
                        'Подключение: ${access.canConnect ? 'разрешено' : 'недоступно'}',
                      ),
                    ),
                  ),
                ],
                const SizedBox(height: 16),
                Text('Подписки',
                    style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                if (state.subscriptions.isEmpty)
                  const Card(
                    child: Padding(
                      padding: EdgeInsets.all(16),
                      child: Text('Подписки пока не найдены.'),
                    ),
                  ),
                for (final item in state.subscriptions)
                  Card(
                    child: ListTile(
                      title: Text(item.groupName),
                      subtitle: Text(
                        'До: ${item.validUntil.isEmpty ? 'не ограничен' : item.validUntil}\n'
                        'Auto renew: ${item.autoRenew ? 'yes' : 'no'}\n'
                        'Active: ${item.active ? 'yes' : 'no'}',
                      ),
                      isThreeLine: true,
                    ),
                  ),
                const SizedBox(height: 16),
                Text('Платежи', style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                if (state.payments.isEmpty)
                  const Card(
                    child: Padding(
                      padding: EdgeInsets.all(16),
                      child: Text('Платежей пока нет.'),
                    ),
                  ),
                for (final item in state.payments)
                  Card(
                    child: ListTile(
                      title: Text('${item.amount} ${item.currency}'),
                      subtitle: Text(
                        'Статус: ${item.status}\n'
                        'Provider: ${item.provider}\n'
                        'Дата: ${item.createdAt}',
                      ),
                      isThreeLine: true,
                    ),
                  ),
                const SizedBox(height: 16),
                Text('Промокод',
                    style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        controller: _promoController,
                        decoration: const InputDecoration(
                          hintText: 'Введите код',
                          border: OutlineInputBorder(),
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
                          : const Text('Применить'),
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
                        state.error!,
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
