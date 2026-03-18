import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:heyearth_vpn/generated/heyearth/v1/config.pb.dart' as pb;
import 'package:package_info_plus/package_info_plus.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../l10n.dart';
import '../update_service.dart';
import 'account_screen.dart';
import 'payment_screen.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  int _navIndex = 0;

  @override
  Widget build(BuildContext context) {
    return Consumer<AppState>(
      builder: (context, state, _) {
        final showNav = !state.loading && state.vpnSupported;
        final l10n = L10n.of(context);
        return Stack(
          children: [
            Scaffold(
              body: state.loading
                  ? const Center(child: CircularProgressIndicator())
                  : !state.vpnSupported
                      ? _UnsupportedPlatform()
                      : _navIndex == 0
                          ? _HomeTab(state: state)
                          : _SettingsTab(
                              state: state,
                              onCheckUpdate: () =>
                                  _checkForUpdate(context, state),
                              onLoginById: () =>
                                  _showLoginDialog(context, state),
                            ),
              bottomNavigationBar: showNav
                  ? NavigationBar(
                      selectedIndex: _navIndex,
                      onDestinationSelected: (value) {
                        setState(() => _navIndex = value);
                      },
                      destinations: [
                        NavigationDestination(
                          icon: const Icon(Icons.home_outlined),
                          selectedIcon: const Icon(Icons.home_rounded),
                          label: l10n.navHome,
                        ),
                        NavigationDestination(
                          icon: const Icon(Icons.settings_outlined),
                          selectedIcon: const Icon(Icons.settings_rounded),
                          label: l10n.navSettings,
                        ),
                      ],
                    )
                  : null,
            ),
            if (state.vpnConnecting) const _ConnectingOverlay(),
            if (state.hasUserId && !state.loading)
              _UpdateChecker(apiBase: state.apiBase),
          ],
        );
      },
    );
  }

  Future<void> _checkForUpdate(BuildContext context, AppState state) async {
    final l10n = L10n(Locale(state.locale));
    final messenger = ScaffoldMessenger.of(context);
    messenger.showSnackBar(
      SnackBar(
          content: Text(l10n.checkingUpdates),
          duration: const Duration(seconds: 30)),
    );
    final result = await checkForUpdate(state.apiBase);
    if (!context.mounted) {
      return;
    }
    messenger.clearSnackBars();
    final update = result.update;
    if (update == null) {
      final msg =
          result.serverVersion != null && result.serverVersion!.isNotEmpty
              ? l10n.latestOnServer(result.serverVersion!)
              : l10n.youHaveLatestVersion;
      messenger.showSnackBar(SnackBar(content: Text(msg)));
      return;
    }
    if (!update.hasUrl) {
      messenger.showSnackBar(
          SnackBar(content: Text(l10n.versionNoLink(update.latestVersion))));
      return;
    }
    showDialog<void>(
      context: context,
      barrierDismissible: !update.mandatory,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.updateAvailable),
        content: Text(
          [
            if (update.mandatory) l10n.updateRequired,
            if (update.minSupportedVersion.isNotEmpty)
              l10n.minSupportedVersionLabel(update.minSupportedVersion),
            update.releaseNotes?.isNotEmpty == true
                ? '${l10n.versionAvailable(update.latestVersion)}\n\n${update.releaseNotes}'
                : l10n.versionAvailable(update.latestVersion),
          ].join('\n\n'),
        ),
        actions: [
          if (!update.mandatory)
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.later),
            ),
          FilledButton(
            onPressed: () async {
              Navigator.of(ctx).pop();
              await openDownloadUrl(update.downloadUrl);
            },
            child: Text(l10n.download),
          ),
        ],
      ),
    );
  }

  void _showLoginDialog(BuildContext context, AppState state) {
    final l10n = L10n(Locale(state.locale));
    final idController = TextEditingController();
    final pwController = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.signInWithHeyearthId),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: idController,
              decoration: InputDecoration(
                hintText: l10n.idOrUsername,
                border: const OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: pwController,
              obscureText: true,
              decoration: InputDecoration(
                hintText: l10n.passwordOptional,
                border: const OutlineInputBorder(),
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.cancel)),
          FilledButton(
            onPressed: () async {
              final pw = pwController.text.trim();
              final ok = await state.login(
                idController.text,
                pw.isNotEmpty ? pw : null,
              );
              if (!ctx.mounted) {
                return;
              }
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(
                    content: Text(ok
                        ? l10n.accountRestored
                        : l10n.translateError(state.error).isNotEmpty
                            ? l10n.translateError(state.error)
                            : l10n.loginFailed)),
              );
            },
            child: Text(l10n.login),
          ),
        ],
      ),
    );
  }
}

class _HomeTab extends StatelessWidget {
  const _HomeTab({required this.state});

  final AppState state;

  @override
  Widget build(BuildContext context) {
    if (!state.hasUserId) {
      return _WelcomeScreen(state: state);
    }

    final access = state.accessState;
    final needsPayment = access != null &&
        !access.canConnect &&
        (access.reason == 'no_active_subscription' ||
            access.reason == 'subscription_required' ||
            access.reason == 'payment_overdue');
    if (needsPayment) {
      return PaymentScreen(
        userId: state.userId!,
        apiBase: state.apiBase,
        jwtToken: state.jwtToken,
        onPaymentComplete: () => state.refreshAccountData(),
        onApplyPromocode: (code) async {
          try {
            await state.applyPromocode(code);
            return true;
          } catch (_) {
            return false;
          }
        },
      );
    }

    final l10n = L10n.of(context);
    final profile = state.profile;

    String _notSet() => l10n.isEn ? 'not set' : 'не выбрана';
    String _notLimited() => l10n.isEn ? 'unlimited' : 'не ограничен';

    return RefreshIndicator(
      onRefresh: () => state.refreshAccountData(),
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Text(
            l10n.appTitle,
            style: Theme.of(context)
                .textTheme
                .headlineMedium
                ?.copyWith(fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 8),
          Text(
            l10n.accountManagedByServer,
            style: Theme.of(context).textTheme.bodyMedium,
          ),
          const SizedBox(height: 16),
          _InfoCard(
            title: 'HeYearth ID',
            subtitle: state.userId ?? '',
            trailing: IconButton(
              onPressed: () {
                Clipboard.setData(ClipboardData(text: state.userId ?? ''));
                ScaffoldMessenger.of(context)
                    .showSnackBar(SnackBar(content: Text(l10n.idCopied)));
              },
              icon: const Icon(Icons.copy_outlined),
            ),
          ),
          if (profile != null) ...[
            const SizedBox(height: 12),
            _InfoCard(
              title: l10n.profileLabel,
              subtitle:
                  '${l10n.planLabel}: ${profile.currentPlan.isEmpty ? 'Free' : profile.currentPlan}'
                  '\n${l10n.platformLabel}: ${profile.preferredPlatform.isEmpty ? _notSet() : profile.preferredPlatform}'
                  '\n${l10n.usernameLabel}: ${state.username?.isNotEmpty == true ? state.username : _notSet()}',
            ),
          ],
          if (access != null) ...[
            const SizedBox(height: 12),
            _InfoCard(
              title: l10n.accessSection,
              subtitle:
                  '${l10n.statusLabel}: ${access.isBlocked ? (l10n.isEn ? 'blocked' : 'заблокирован') : access.canConnect ? (l10n.isEn ? 'active' : 'активен') : access.reason}'
                  '\n${l10n.validUntilLabel}: ${access.validUntil.isEmpty ? _notLimited() : access.validUntil}',
            ),
          ],
          const SizedBox(height: 12),
          if (state.error != null) _ErrorBanner(message: state.error!),
          if (state.error != null) const SizedBox(height: 12),
          _MainVpnButton(
            vpnOn: state.vpnOn,
            connecting: state.vpnConnecting,
            enabled: state.canConnect && !state.isBlocked,
            onToggle: () => state.setVpnOn(!state.vpnOn),
          ),
          _ConnectionModeCards(
            activeOption: state.activeOption,
            connectionOptions: state.connectionOptions,
            vpnOn: state.vpnOn,
            onSelectOption: state.selectConnectionOption,
          ),
          const SizedBox(height: 12),
          Text(
            state.vpnConnecting
                ? l10n.connecting
                : state.vpnOn
                    ? l10n.connectedTraffic
                    : state.canConnect
                        ? l10n.tapToEnable
                        : l10n.connectionUnavailable,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: state.accountLoading
                      ? null
                      : () => state.refreshAccountData(),
                  icon: const Icon(Icons.refresh),
                  label: Text(state.accountLoading
                      ? l10n.updatingAccount
                      : l10n.updateAccount),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton.icon(
                  onPressed: () {
                    Navigator.of(context).push(
                      MaterialPageRoute(builder: (_) => const AccountScreen()),
                    );
                  },
                  icon: const Icon(Icons.person_outline),
                  label: Text(l10n.accountScreenTitle),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _SettingsTab extends StatelessWidget {
  const _SettingsTab({
    required this.state,
    required this.onCheckUpdate,
    required this.onLoginById,
  });

  final AppState state;
  final VoidCallback onCheckUpdate;
  final VoidCallback onLoginById;

  @override
  Widget build(BuildContext context) {
    final l10n = L10n.of(context);
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text(
          l10n.navSettings,
          style: Theme.of(context)
              .textTheme
              .headlineSmall
              ?.copyWith(fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 16),
        _ThemeModeCard(
            themeMode: state.themeMode, onChanged: state.setThemeMode),
        const SizedBox(height: 16),
        _LanguageCard(locale: state.locale, onLocaleChanged: state.setLocale),
        const SizedBox(height: 16),
        OutlinedButton.icon(
          onPressed: onLoginById,
          icon: const Icon(Icons.badge_outlined),
          label: Text(l10n.signInWithHeyearthId),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: onCheckUpdate,
          icon: const Icon(Icons.system_update_alt),
          label: Text(l10n.checkForUpdates),
        ),
        if (state.hasUserId) ...[
          const SizedBox(height: 12),
          OutlinedButton.icon(
            onPressed: state.signOut,
            icon: const Icon(Icons.logout),
            label: Text(l10n.signOut),
          ),
        ],
        const SizedBox(height: 24),
        const _AppVersionLabel(),
      ],
    );
  }
}

class _WelcomeScreen extends StatefulWidget {
  const _WelcomeScreen({required this.state});

  final AppState state;

  @override
  State<_WelcomeScreen> createState() => _WelcomeScreenState();
}

class _WelcomeScreenState extends State<_WelcomeScreen> {
  bool _registering = false;

  Future<void> _doRegister() async {
    setState(() => _registering = true);
    final ok = await widget.state.register();
    if (!mounted) return;
    setState(() => _registering = false);
    if (ok) {
      final l10n = L10n.of(context);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.accountRestored)),
      );
    }
  }

  void _showLoginDialog() {
    final l10n = L10n(Locale(widget.state.locale));
    final idController = TextEditingController();
    final pwController = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.signInWithHeyearthId),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: idController,
              decoration: InputDecoration(
                hintText: l10n.idOrUsername,
                border: const OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: pwController,
              obscureText: true,
              decoration: InputDecoration(
                hintText: l10n.passwordOptional,
                border: const OutlineInputBorder(),
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.cancel)),
          FilledButton(
            onPressed: () async {
              final pw = pwController.text.trim();
              final ok = await widget.state.login(
                idController.text,
                pw.isNotEmpty ? pw : null,
              );
              if (!ctx.mounted) return;
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(
                    content: Text(ok
                        ? l10n.accountRestored
                        : l10n.translateError(widget.state.error).isNotEmpty
                            ? l10n.translateError(widget.state.error)
                            : l10n.loginFailed)),
              );
            },
            child: Text(l10n.login),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = L10n.of(context);
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        const SizedBox(height: 48),
        Text(
          l10n.welcomeTitle,
          style: Theme.of(context)
              .textTheme
              .displaySmall
              ?.copyWith(fontWeight: FontWeight.w700),
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: 12),
        Text(
          l10n.welcomeDesc,
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.bodyLarge,
        ),
        const SizedBox(height: 32),
        FilledButton.icon(
          onPressed: _registering ? null : _doRegister,
          icon: _registering
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.person_add_outlined),
          label: Text(_registering ? l10n.registering : l10n.getHeyearthId),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: _showLoginDialog,
          icon: const Icon(Icons.badge_outlined),
          label: Text(l10n.alreadyHaveId),
        ),
        const SizedBox(height: 24),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(Icons.warning_amber_rounded,
                    color: Theme.of(context).colorScheme.error),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    l10n.saveYourIdWarning,
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                ),
              ],
            ),
          ),
        ),
        if (widget.state.error != null) ...[
          const SizedBox(height: 12),
          _ErrorBanner(message: widget.state.error!),
        ],
      ],
    );
  }
}

class _InfoCard extends StatelessWidget {
  const _InfoCard({
    required this.title,
    required this.subtitle,
    this.trailing,
  });

  final String title;
  final String subtitle;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: ListTile(
        title: Text(title),
        subtitle: Text(subtitle),
        trailing: trailing,
      ),
    );
  }
}

class _MainVpnButton extends StatelessWidget {
  const _MainVpnButton({
    required this.vpnOn,
    required this.connecting,
    required this.enabled,
    required this.onToggle,
  });

  final bool vpnOn;
  final bool connecting;
  final bool enabled;
  final VoidCallback onToggle;

  @override
  Widget build(BuildContext context) {
    final l10n = L10n.of(context);
    return Card(
      child: InkWell(
        onTap: connecting || !enabled ? null : onToggle,
        borderRadius: BorderRadius.circular(20),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 28, horizontal: 24),
          child: Column(
            children: [
              if (connecting)
                const SizedBox(
                  width: 48,
                  height: 48,
                  child: CircularProgressIndicator(strokeWidth: 3),
                )
              else
                Icon(
                  vpnOn ? Icons.vpn_lock_rounded : Icons.vpn_lock_outlined,
                  size: 56,
                  color: enabled
                      ? Theme.of(context).colorScheme.primary
                      : Theme.of(context).disabledColor,
                ),
              const SizedBox(height: 12),
              Text(
                connecting
                    ? l10n.connecting
                    : vpnOn
                        ? l10n.turnOffHeyearth
                        : enabled
                            ? l10n.turnOnHeyearth
                            : l10n.connectionUnavailable,
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Displays connection mode cards (VPN / SOCKS5 / HTTP).
class _ConnectionModeCards extends StatelessWidget {
  const _ConnectionModeCards({
    required this.activeOption,
    required this.connectionOptions,
    required this.vpnOn,
    required this.onSelectOption,
  });

  final pb.ConnectionOption? activeOption;
  final List<pb.ConnectionOption> connectionOptions;
  final bool vpnOn;
  final void Function(pb.ConnectionOption) onSelectOption;

  @override
  Widget build(BuildContext context) {
    if (connectionOptions.isEmpty) return const SizedBox.shrink();

    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: connectionOptions.map((opt) {
          final isActive = activeOption != null && opt.uri == activeOption!.uri;
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
          return ChoiceChip(
            label: Text(label),
            selected: isActive,
            onSelected: (_) => onSelectOption(opt),
          );
        }).toList(),
      ),
    );
  }
}

class _ThemeModeCard extends StatelessWidget {
  const _ThemeModeCard({required this.themeMode, required this.onChanged});

  final ThemeMode themeMode;
  final ValueChanged<ThemeMode> onChanged;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(L10n.of(context).theme,
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 12),
            SegmentedButton<ThemeMode>(
              segments: [
                ButtonSegment(
                    value: ThemeMode.system,
                    label: Text(L10n.of(context).system)),
                ButtonSegment(
                    value: ThemeMode.light,
                    label: Text(L10n.of(context).light)),
                ButtonSegment(
                    value: ThemeMode.dark, label: Text(L10n.of(context).dark)),
              ],
              selected: {themeMode},
              onSelectionChanged: (selection) => onChanged(selection.first),
            ),
          ],
        ),
      ),
    );
  }
}

class _LanguageCard extends StatelessWidget {
  const _LanguageCard({required this.locale, required this.onLocaleChanged});

  final String locale;
  final ValueChanged<String> onLocaleChanged;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(L10n.of(context).language,
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 12),
            SegmentedButton<String>(
              segments: [
                ButtonSegment(
                    value: 'ru', label: Text(L10n.of(context).langRu)),
                ButtonSegment(
                    value: 'en', label: Text(L10n.of(context).langEn)),
              ],
              selected: {locale},
              onSelectionChanged: (selection) =>
                  onLocaleChanged(selection.first),
            ),
          ],
        ),
      ),
    );
  }
}

class _UnsupportedPlatform extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          L10n.of(context).unsupportedTitle,
          textAlign: TextAlign.center,
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    final translated = L10n.of(context).translateError(message);
    return Material(
      color: Theme.of(context).colorScheme.errorContainer,
      borderRadius: BorderRadius.circular(12),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Row(
          children: [
            Icon(Icons.warning_amber_rounded,
                color: Theme.of(context).colorScheme.onErrorContainer),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                translated.isNotEmpty ? translated : message,
                style: TextStyle(
                    color: Theme.of(context).colorScheme.onErrorContainer),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ConnectingOverlay extends StatelessWidget {
  const _ConnectingOverlay();

  @override
  Widget build(BuildContext context) {
    return Positioned.fill(
      child: Material(
        color: Colors.black.withValues(alpha: 0.7),
        child: const Center(
          child: CircularProgressIndicator(),
        ),
      ),
    );
  }
}

class _UpdateChecker extends StatefulWidget {
  const _UpdateChecker({required this.apiBase});

  final String apiBase;

  @override
  State<_UpdateChecker> createState() => _UpdateCheckerState();
}

class _UpdateCheckerState extends State<_UpdateChecker> {
  bool _done = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _runCheck());
  }

  Future<void> _runCheck() async {
    if (_done) {
      return;
    }
    await Future.delayed(const Duration(seconds: 3));
    if (!mounted) {
      return;
    }
    final result = await checkForUpdate(widget.apiBase);
    final update = result.update;
    if (!mounted || update == null || !update.hasUrl) {
      _done = true;
      return;
    }
    _done = true;
    final l10n = L10n.of(context);
    showDialog<void>(
      context: context,
      barrierDismissible: !update.mandatory,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.updateAvailable),
        content: Text(
          [
            if (update.mandatory) l10n.updateIsMandatory,
            if (update.minSupportedVersion.isNotEmpty)
              l10n.minSupportedVersionLabel(update.minSupportedVersion),
            update.releaseNotes?.isNotEmpty == true
                ? update.releaseNotes!
                : update.latestVersion,
          ].join('\n\n'),
        ),
        actions: [
          if (!update.mandatory)
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.later),
            ),
          FilledButton(
            onPressed: () async {
              Navigator.of(ctx).pop();
              await openDownloadUrl(update.downloadUrl);
            },
            child: Text(l10n.download),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) => const SizedBox.shrink();
}

class _AppVersionLabel extends StatelessWidget {
  const _AppVersionLabel();

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<PackageInfo>(
      future: PackageInfo.fromPlatform(),
      builder: (context, snapshot) {
        final version = snapshot.data?.version;
        if (version == null || version.isEmpty) {
          return const SizedBox.shrink();
        }
        return Text(
          L10n.of(context).versionLabel(version),
          textAlign: TextAlign.center,
        );
      },
    );
  }
}
