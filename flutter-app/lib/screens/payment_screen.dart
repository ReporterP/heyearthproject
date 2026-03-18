import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../l10n.dart';
import '../payment_api.dart';

class PaymentScreen extends StatefulWidget {
  const PaymentScreen({
    super.key,
    required this.userId,
    required this.apiBase,
    required this.onPaymentComplete,
    this.onApplyPromocode,
    this.jwtToken,
  });

  final String userId;
  final String apiBase;
  final VoidCallback onPaymentComplete;
  final Future<bool> Function(String code)? onApplyPromocode;
  final String? jwtToken;

  @override
  State<PaymentScreen> createState() => _PaymentScreenState();
}

class _PaymentScreenState extends State<PaymentScreen> {
  bool _loading = false;
  String? _error;
  String _selectedPlan = 'standard';
  final _codeController = TextEditingController();
  bool _showCodeInput = false;

  @override
  void dispose() {
    _codeController.dispose();
    super.dispose();
  }

  Future<void> _startPayment() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final api = PaymentApi(widget.apiBase, jwtToken: widget.jwtToken);
      final result = await api.createPayment(
        userId: widget.userId,
        plan: _selectedPlan,
      );
      final url = result['confirmation_url'] as String? ?? '';
      if (url.isNotEmpty) {
        final uri = Uri.parse(url);
        if (await canLaunchUrl(uri)) {
          await launchUrl(uri, mode: LaunchMode.externalApplication);
        }
      }
    } catch (e) {
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _redeemCode() async {
    final code = _codeController.text.trim();
    if (code.isEmpty) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      if (widget.onApplyPromocode != null) {
        final ok = await widget.onApplyPromocode!(code);
        if (ok) {
          if (mounted) widget.onPaymentComplete();
          return;
        }
      }
      final api = PaymentApi(widget.apiBase, jwtToken: widget.jwtToken);
      await api.redeemCode(userId: widget.userId, code: code);
      if (mounted) widget.onPaymentComplete();
    } catch (e) {
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = L10n.of(context);
    return Scaffold(
      appBar: AppBar(title: Text(l10n.isEn ? 'Subscription' : 'Подписка')),
      body: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Text(
            l10n.isEn
                ? 'Choose a plan to start using HeYearth VPN'
                : 'Выберите тариф для использования HeYearth VPN',
            style: Theme.of(context).textTheme.titleMedium,
          ),
          const SizedBox(height: 24),
          _PlanCard(
            title: l10n.isEn ? 'Standard' : 'Стандарт',
            price: '200 ₽/мес',
            features: [
              l10n.isEn ? 'Automatic server selection' : 'Автоматический выбор сервера',
              l10n.isEn ? 'Smart protocol switching' : 'Умное переключение протоколов',
              l10n.isEn ? 'Unlimited traffic' : 'Без ограничений по трафику',
            ],
            selected: _selectedPlan == 'standard',
            onTap: () => setState(() => _selectedPlan = 'standard'),
          ),
          const SizedBox(height: 12),
          _PlanCard(
            title: l10n.isEn ? 'Premium' : 'Премиум',
            price: '250 ₽/мес',
            features: [
              l10n.isEn ? 'Everything in Standard' : 'Всё из «Стандарт»',
              l10n.isEn ? 'Manual protocol & server choice' : 'Ручной выбор протокола и сервера',
              l10n.isEn ? 'Multi-hop (chain of servers)' : 'Мульти-хоп (цепочка серверов)',
              l10n.isEn ? 'Priority support' : 'Приоритетная поддержка',
            ],
            selected: _selectedPlan == 'premium',
            highlighted: true,
            onTap: () => setState(() => _selectedPlan = 'premium'),
          ),
          const SizedBox(height: 24),
          FilledButton(
            onPressed: _loading ? null : _startPayment,
            child: _loading
                ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                : Text(l10n.isEn ? 'Pay' : 'Оплатить'),
          ),
          const SizedBox(height: 12),
          OutlinedButton(
            onPressed: () => setState(() => _showCodeInput = !_showCodeInput),
            child: Text(l10n.isEn ? 'Enter promocode' : 'Ввести промокод'),
          ),
          if (_showCodeInput) ...[
            const SizedBox(height: 12),
            TextField(
              controller: _codeController,
              decoration: InputDecoration(
                hintText: l10n.isEn ? 'Promocode or company code' : 'Промокод или код компании',
                border: const OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 8),
            FilledButton(
              onPressed: _loading ? null : _redeemCode,
              child: Text(l10n.isEn ? 'Apply' : 'Применить'),
            ),
          ],
          if (_error != null) ...[
            const SizedBox(height: 12),
            Card(
              color: Theme.of(context).colorScheme.errorContainer,
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.onErrorContainer)),
              ),
            ),
          ],
          const SizedBox(height: 24),
          TextButton(
            onPressed: widget.onPaymentComplete,
            child: Text(l10n.isEn ? 'I already paid, check access' : 'Я уже оплатил, проверить доступ'),
          ),
        ],
      ),
    );
  }
}

class _PlanCard extends StatelessWidget {
  const _PlanCard({
    required this.title,
    required this.price,
    required this.features,
    required this.selected,
    required this.onTap,
    this.highlighted = false,
  });

  final String title;
  final String price;
  final List<String> features;
  final bool selected;
  final bool highlighted;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: selected
            ? BorderSide(color: Theme.of(context).colorScheme.primary, width: 2)
            : BorderSide.none,
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(child: Text(title, style: Theme.of(context).textTheme.titleLarge)),
                  Text(price, style: Theme.of(context).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
                ],
              ),
              const SizedBox(height: 8),
              ...features.map((f) => Padding(
                    padding: const EdgeInsets.only(bottom: 4),
                    child: Row(
                      children: [
                        Icon(Icons.check, size: 16, color: Theme.of(context).colorScheme.primary),
                        const SizedBox(width: 8),
                        Expanded(child: Text(f, style: Theme.of(context).textTheme.bodySmall)),
                      ],
                    ),
                  )),
              if (highlighted)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Chip(
                    label: Text(L10n.of(context).isEn ? 'Recommended' : 'Рекомендуем'),
                    backgroundColor: Theme.of(context).colorScheme.primaryContainer,
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
