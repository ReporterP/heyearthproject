import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:provider/provider.dart';

import '../app_state.dart';
import '../l10n.dart';
import '../update_service.dart';
import 'profile_screen.dart';

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
                              onRestoreById: () =>
                                  _showRestoreDialog(context, state),
                              onClaimCode: () =>
                                  _showClaimCodeDialog(context, state),
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

  void _showRestoreDialog(BuildContext context, AppState state) {
    final l10n = L10n(Locale(state.locale));
    final controller = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.signInWithHeyearthId),
        content: TextField(
          controller: controller,
          decoration: InputDecoration(
            hintText: l10n.pasteHeyearthId,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.cancel)),
          FilledButton(
            onPressed: () async {
              final ok = await state.restoreUserId(controller.text);
              if (!ctx.mounted) {
                return;
              }
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(
                    content: Text(ok
                        ? l10n.accountRestored
                        : l10n.translateError(state.error) .isNotEmpty
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

  void _showClaimCodeDialog(BuildContext context, AppState state) {
    final l10n = L10n(Locale(state.locale));
    final controller = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.enterCodeFromBot),
        content: TextField(
          controller: controller,
          textCapitalization: TextCapitalization.characters,
          decoration: InputDecoration(
            hintText: l10n.enterCodeHint,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.cancel)),
          FilledButton(
            onPressed: () async {
              final ok = await state.claimByCode(controller.text);
              if (!ctx.mounted) {
                return;
              }
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(
                    content: Text(ok
                        ? l10n.deviceLinked
                        : l10n.translateError(state.error).isNotEmpty
                            ? l10n.translateError(state.error)
                            : l10n.pairingFailed)),
              );
            },
            child: Text(l10n.link),
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

    final l10n = L10n.of(context);
    final profile = state.profile;
    final access = state.accessState;

    String _linked(bool v) => v
        ? (l10n.isEn ? 'linked' : 'привязан')
        : (l10n.isEn ? 'not linked' : 'не привязан');
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
                  '\n${l10n.telegramLabel}: ${_linked(profile.telegramLinked)}',
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
          if (state.legacyHappFallback)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Apple fallback',
                        style: Theme.of(context).textTheme.titleMedium),
                    const SizedBox(height: 8),
                    Text(l10n.legacyPairingNote),
                  ],
                ),
              ),
            )
          else ...[
            _MainVpnButton(
              vpnOn: state.vpnOn,
              connecting: state.vpnConnecting,
              enabled: state.canConnect && !state.isBlocked,
              onToggle: () => state.setVpnOn(!state.vpnOn),
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
          ],
          const SizedBox(height: 16),
          SwitchListTile(
            value: state.selectedOnlyMode,
            onChanged: (_) =>
                state.setSelectedOnlyMode(!state.selectedOnlyMode),
            title: Text(l10n.onlyForSelectedServices),
            subtitle: Text(l10n.onlyForSelectedHint),
          ),
          const SizedBox(height: 12),
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
                      MaterialPageRoute(builder: (_) => const ProfileScreen()),
                    );
                  },
                  icon: const Icon(Icons.person_outline),
                  label: Text(l10n.profileLabel),
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
    required this.onRestoreById,
    required this.onClaimCode,
  });

  final AppState state;
  final VoidCallback onCheckUpdate;
  final VoidCallback onRestoreById;
  final VoidCallback onClaimCode;

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
        FilledButton.icon(
          onPressed: onClaimCode,
          icon: const Icon(Icons.link),
          label: Text(l10n.enterCodeFromBot),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: onRestoreById,
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

class _WelcomeScreen extends StatelessWidget {
  const _WelcomeScreen({required this.state});

  final AppState state;

  @override
  Widget build(BuildContext context) {
    final l10n = L10n.of(context);
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text(
          l10n.welcomeTitle,
          style: Theme.of(context)
              .textTheme
              .headlineMedium
              ?.copyWith(fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 8),
        Text(l10n.welcomeDesc),
        const SizedBox(height: 24),
        FilledButton.icon(
          onPressed: () => _showClaimCodeDialog(context, state),
          icon: const Icon(Icons.key_outlined),
          label: Text(l10n.haveCodeFromBot),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: () => _showRestoreDialog(context, state),
          icon: const Icon(Icons.badge_outlined),
          label: Text(l10n.haveHeyearthId),
        ),
        const SizedBox(height: 16),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(l10n.howItWorks,
                    style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 8),
                Text(l10n.howItWorksSteps),
              ],
            ),
          ),
        ),
        if (state.error != null) ...[
          const SizedBox(height: 12),
          _ErrorBanner(message: state.error!),
        ],
      ],
    );
  }

  void _showRestoreDialog(BuildContext context, AppState state) {
    final l10n = L10n(Locale(state.locale));
    final controller = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.signInWithHeyearthId),
        content: TextField(
          controller: controller,
          decoration: InputDecoration(
            hintText: l10n.pasteHeyearthId,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.cancel)),
          FilledButton(
            onPressed: () async {
              final ok = await state.restoreUserId(controller.text);
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

  void _showClaimCodeDialog(BuildContext context, AppState state) {
    final l10n = L10n(Locale(state.locale));
    final controller = TextEditingController();
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.enterCodeFromBot),
        content: TextField(
          controller: controller,
          textCapitalization: TextCapitalization.characters,
          decoration: InputDecoration(
            hintText: l10n.enterCodeHint,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l10n.cancel)),
          FilledButton(
            onPressed: () async {
              final ok = await state.claimByCode(controller.text);
              if (!ctx.mounted) {
                return;
              }
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(
                    content: Text(ok
                        ? l10n.deviceLinkedShort
                        : l10n.translateError(state.error).isNotEmpty
                            ? l10n.translateError(state.error)
                            : l10n.pairingFailed)),
              );
            },
            child: Text(l10n.link),
          ),
        ],
      ),
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
