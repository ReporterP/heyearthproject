import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import 'package:package_info_plus/package_info_plus.dart';

import '../app_state.dart';
import '../l10n.dart';
import '../update_service.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with TickerProviderStateMixin {
  int _navIndex = 0;

  @override
  Widget build(BuildContext context) {
    return Consumer<AppState>(
      builder: (context, state, _) {
        final showNav = !state.loading && state.vpnSupported;
        return Stack(
          children: [
            Scaffold(
              body: AnimatedSwitcher(
                duration: const Duration(milliseconds: 280),
                switchInCurve: Curves.easeOutCubic,
                switchOutCurve: Curves.easeInCubic,
                transitionBuilder: (child, animation) {
                  final slide = Tween<Offset>(
                    begin: Offset(_navIndex == 0 ? -0.03 : 0.03, 0),
                    end: Offset.zero,
                  ).animate(CurvedAnimation(parent: animation, curve: Curves.easeOutCubic));
                  return FadeTransition(
                    opacity: animation,
                    child: SlideTransition(position: slide, child: child),
                  );
                },
                child: state.loading
                    ? _buildLoading(context)
                    : !state.vpnSupported
                        ? _buildUnsupported(context)
                        : _navIndex == 0
                            ? _HomeTab(key: const ValueKey('home'), state: state)
                            : _SettingsTab(
                                key: const ValueKey('settings'),
                                state: state,
                                onCheckUpdate: () => _checkForUpdate(context, state),
                                onRestoreById: () => _showRestoreDialog(context, state),
                              ),
              ),
              bottomNavigationBar: showNav
                  ? NavigationBar(
                      selectedIndex: _navIndex,
                      onDestinationSelected: (i) {
                        setState(() => _navIndex = i);
                      },
                      destinations: [
                        NavigationDestination(
                          icon: const Icon(Icons.home_outlined),
                          selectedIcon: const Icon(Icons.home_rounded),
                          label: L10n.of(context).navHome,
                        ),
                        NavigationDestination(
                          icon: const Icon(Icons.settings_outlined),
                          selectedIcon: const Icon(Icons.settings_rounded),
                          label: L10n.of(context).navSettings,
                        ),
                      ],
                    )
                  : null,
            ),
            if (state.vpnConnecting)
              _ConnectingOverlay(selectedOnly: state.selectedOnlyMode),
            if (state.hasUserId && !state.loading)
              _UpdateChecker(apiBase: state.apiBase),
          ],
        );
      },
    );
  }

  Widget _buildLoading(BuildContext context) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          SizedBox(
            width: 48,
            height: 48,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              color: Theme.of(context).colorScheme.primary,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildUnsupported(BuildContext context) {
    return Center(child: _UnsupportedPlatform());
  }

  Future<void> _checkForUpdate(BuildContext context, AppState state) async {
    final l10n = L10n(Locale(state.locale));
    final messenger = ScaffoldMessenger.of(context);
    messenger.showSnackBar(
      SnackBar(content: Text(l10n.checkingUpdates), duration: const Duration(seconds: 30)),
    );
    final result = await checkForUpdate(state.apiBase);
    if (!context.mounted) return;
    messenger.clearSnackBars();
    final update = result.update;
    if (update == null) {
      final msg = result.serverVersion != null && result.serverVersion!.isNotEmpty
          ? l10n.latestOnServer(result.serverVersion!)
          : l10n.youHaveLatestVersion;
      messenger.showSnackBar(SnackBar(content: Text(msg)));
      return;
    }
    if (update.hasUrl) {
      showDialog(
        context: context,
        builder: (ctx) {
          final l = L10n(Locale(state.locale));
          return AlertDialog(
            title: Text(l.updateAvailable),
            content: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(l.versionAvailable(update.latestVersion)),
                  if (update.releaseNotes != null && update.releaseNotes!.isNotEmpty) ...[
                    const SizedBox(height: 12),
                    Text(update.releaseNotes!, style: Theme.of(ctx).textTheme.bodySmall),
                  ],
                ],
              ),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(),
                child: Text(l.later),
              ),
              FilledButton(
                onPressed: () async {
                  Navigator.of(ctx).pop();
                  await openDownloadUrl(update.downloadUrl);
                },
                child: Text(l.download),
              ),
            ],
          );
        },
      );
    } else {
      messenger.showSnackBar(
        SnackBar(
          content: Text(l10n.versionNoLink(update.latestVersion)),
        ),
      );
    }
  }

  void _showRestoreDialog(BuildContext context, AppState state) {
    final controller = TextEditingController();
    showDialog(
      context: context,
      builder: (ctx) {
        final l = L10n.of(ctx);
        return AlertDialog(
          title: Text(l.restoreById),
          content: TextField(
            controller: controller,
            decoration: InputDecoration(
              hintText: l.pasteHeyearthId,
              border: const OutlineInputBorder(),
            ),
            autocorrect: false,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l.cancel),
            ),
            FilledButton(
              onPressed: () async {
                bool ok = false;
                try {
                  ok = await state.restoreUserId(controller.text);
                } catch (_) {
                  ok = false;
                }
                if (!ctx.mounted) return;
                Navigator.of(ctx).pop();
                final msg = ok ? l.accountRestored : (state.error != null ? l.translateError(state.error) : l.error);
                ScaffoldMessenger.of(ctx).showSnackBar(SnackBar(content: Text(msg)));
              },
              child: Text(l.signIn),
            ),
          ],
        );
      },
    );
  }
}

/// Вкладка «Главная»: приветствие или ID + кнопка VPN + «только для выбранных».
class _HomeTab extends StatefulWidget {
  const _HomeTab({super.key, required this.state});
  final AppState state;

  @override
  State<_HomeTab> createState() => _HomeTabState();
}

class _HomeTabState extends State<_HomeTab> with SingleTickerProviderStateMixin {
  late AnimationController _animController;
  late Animation<double> _anim;

  @override
  void initState() {
    super.initState();
    _animController = AnimationController(
      duration: const Duration(milliseconds: 400),
      vsync: this,
    );
    _anim = CurvedAnimation(parent: _animController, curve: Curves.easeOutCubic);
    _animController.forward();
  }

  @override
  void dispose() {
    _animController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = widget.state;
    if (!state.hasUserId) {
      return CustomScrollView(
        slivers: [
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(24, 32, 24, 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    L10n.of(context).appTitle,
                    style: Theme.of(context).textTheme.headlineMedium?.copyWith(
                          fontWeight: FontWeight.w700,
                          color: Theme.of(context).colorScheme.onSurface,
                        ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    L10n.of(context).subtitle,
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                ],
              ),
            ),
          ),
          SliverFillRemaining(
            hasScrollBody: false,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: _WelcomeScreen(state: state),
            ),
          ),
        ],
      );
    }
    return FadeTransition(
      opacity: _anim,
      child: CustomScrollView(
        slivers: [
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(24, 32, 24, 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    L10n.of(context).appTitle,
                    style: Theme.of(context).textTheme.headlineMedium?.copyWith(
                          fontWeight: FontWeight.w700,
                          color: Theme.of(context).colorScheme.onSurface,
                        ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    L10n.of(context).subtitle,
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                ],
              ),
            ),
          ),
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            sliver: SliverToBoxAdapter(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                mainAxisSize: MainAxisSize.min,
                children: [
                  const SizedBox(height: 16),
                  _UserIdCard(userId: state.userId!),
                  const SizedBox(height: 16),
                  if (state.error != null) ...[
                    _ErrorBanner(message: L10n.of(context).translateError(state.error!)),
                    const SizedBox(height: 16),
                  ],
                  _AnimatedMainVpnButton(
                    vpnOn: state.vpnOn,
                    connecting: state.vpnConnecting,
                    onToggle: () => state.setVpnOn(!state.vpnOn),
                  ),
                  const SizedBox(height: 16),
                  Text(
                    state.vpnConnecting
                        ? L10n.of(context).connecting
                        : state.vpnOn
                            ? L10n.of(context).connectedTraffic
                            : L10n.of(context).tapToEnable,
                    textAlign: TextAlign.center,
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                  const SizedBox(height: 20),
                  _SelectedOnlyCard(
                    selectedOnly: state.selectedOnlyMode,
                    onToggle: () => state.setSelectedOnlyMode(!state.selectedOnlyMode),
                  ),
                  const SizedBox(height: 32),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Вкладка «Настройки»: тема, язык, обновления, вход по ID, версия.
class _SettingsTab extends StatefulWidget {
  const _SettingsTab({
    super.key,
    required this.state,
    required this.onCheckUpdate,
    required this.onRestoreById,
  });
  final AppState state;
  final VoidCallback onCheckUpdate;
  final VoidCallback onRestoreById;

  @override
  State<_SettingsTab> createState() => _SettingsTabState();
}

class _SettingsTabState extends State<_SettingsTab> with SingleTickerProviderStateMixin {
  late AnimationController _animController;
  late Animation<double> _anim;

  @override
  void initState() {
    super.initState();
    _animController = AnimationController(
      duration: const Duration(milliseconds: 400),
      vsync: this,
    );
    _anim = CurvedAnimation(parent: _animController, curve: Curves.easeOutCubic);
    _animController.forward();
  }

  @override
  void dispose() {
    _animController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = widget.state;
    final l10n = L10n.of(context);
    return FadeTransition(
      opacity: _anim,
      child: CustomScrollView(
        slivers: [
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(24, 32, 24, 8),
              child: Text(
                l10n.navSettings,
                style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                      fontWeight: FontWeight.w700,
                      color: Theme.of(context).colorScheme.onSurface,
                    ),
              ),
            ),
          ),
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            sliver: SliverList(
              delegate: SliverChildListDelegate([
                const SizedBox(height: 16),
                _ThemeModeCard(themeMode: state.themeMode, onChanged: state.setThemeMode),
                const SizedBox(height: 20),
                _LanguageCard(locale: state.locale, onLocaleChanged: state.setLocale),
                const SizedBox(height: 20),
                FilledButton.icon(
                  onPressed: widget.onCheckUpdate,
                  icon: const Icon(Icons.system_update, size: 22),
                  label: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    child: Text(l10n.checkForUpdates),
                  ),
                  style: FilledButton.styleFrom(
                    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                  ),
                ),
                const SizedBox(height: 12),
                OutlinedButton.icon(
                  onPressed: widget.onRestoreById,
                  icon: const Icon(Icons.phone_android, size: 22),
                  label: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    child: Text(l10n.signInWithIdFromDevice),
                  ),
                  style: OutlinedButton.styleFrom(
                    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                  ),
                ),
                const SizedBox(height: 24),
                const _AppVersionLabel(),
                const SizedBox(height: 32),
              ]),
            ),
          ),
        ],
      ),
    );
  }
}

class _WelcomeScreen extends StatelessWidget {
  final AppState state;

  const _WelcomeScreen({required this.state});

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 24),
        Text(
          L10n.of(context).firstLaunch,
          style: Theme.of(context).textTheme.titleLarge?.copyWith(
                fontWeight: FontWeight.w600,
                color: Theme.of(context).colorScheme.onSurface,
              ),
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: 8),
        Text(
          L10n.of(context).chooseHowToContinue,
          style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: 32),
        FilledButton.icon(
          onPressed: () async {
            await state.chooseNewUser();
          },
          icon: const Icon(Icons.person_add, size: 22),
          label: Padding(
            padding: const EdgeInsets.symmetric(vertical: 14),
            child: Text(L10n.of(context).imNewUser),
          ),
          style: FilledButton.styleFrom(
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
          ),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: () => _showEnterIdDialog(context, state),
          icon: const Icon(Icons.login, size: 22),
          label: Padding(
            padding: const EdgeInsets.symmetric(vertical: 14),
            child: Text(L10n.of(context).iHaveId),
          ),
          style: OutlinedButton.styleFrom(
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
          ),
        ),
      ],
    );
  }

  void _showEnterIdDialog(BuildContext context, AppState state) {
    final controller = TextEditingController();
    showDialog(
      context: context,
      builder: (ctx) {
        final l = L10n.of(ctx);
        return AlertDialog(
          title: Text(l.enterYourId),
          content: TextField(
            controller: controller,
            decoration: InputDecoration(
              hintText: l.heyearthId,
              border: const OutlineInputBorder(),
            ),
            autocorrect: false,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: Text(l.cancel),
            ),
            FilledButton(
              onPressed: () async {
                bool ok = false;
                try {
                  ok = await state.restoreUserId(controller.text);
                } catch (_) {
                  ok = false;
                }
                if (!ctx.mounted) return;
                Navigator.of(ctx).pop();
                final msg = ok ? l.accountRestored : (state.error != null ? l.translateError(state.error) : l.error);
                ScaffoldMessenger.of(ctx).showSnackBar(SnackBar(content: Text(msg)));
              },
              child: Text(l.signIn),
            ),
          ],
        );
      },
    );
  }
}

class _ConnectingOverlay extends StatefulWidget {
  const _ConnectingOverlay({required this.selectedOnly});
  final bool selectedOnly;

  @override
  State<_ConnectingOverlay> createState() => _ConnectingOverlayState();
}

class _ConnectingOverlayState extends State<_ConnectingOverlay>
    with SingleTickerProviderStateMixin {
  late AnimationController _pulseController;
  late Animation<double> _pulse;

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      duration: const Duration(milliseconds: 1400),
      vsync: this,
    )..repeat(reverse: true);
    _pulse = Tween<double>(begin: 0.92, end: 1.08).animate(
      CurvedAnimation(parent: _pulseController, curve: Curves.easeInOut),
    );
  }

  @override
  void dispose() {
    _pulseController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return Positioned.fill(
      child: Material(
        child: Container(
          width: double.infinity,
          height: double.infinity,
          decoration: const BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [
                Color(0xFF0D1117),
                Color(0xFF161B22),
                Color(0xFF0D1520),
              ],
            ),
          ),
          child: SafeArea(
            child: Center(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 40),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    AnimatedBuilder(
                      animation: _pulse,
                      builder: (context, child) {
                        return Transform.scale(
                          scale: _pulse.value,
                          child: child,
                        );
                      },
                      child: Container(
                        padding: const EdgeInsets.all(20),
                        decoration: BoxDecoration(
                          color: colorScheme.primaryContainer.withOpacity(0.25),
                          shape: BoxShape.circle,
                          boxShadow: [
                            BoxShadow(
                              color: colorScheme.primary.withOpacity(0.3),
                              blurRadius: 24,
                              spreadRadius: 2,
                            ),
                          ],
                        ),
                        child: SizedBox(
                          width: 56,
                          height: 56,
                          child: CircularProgressIndicator(
                            strokeWidth: 3,
                            color: colorScheme.primary,
                            backgroundColor: colorScheme.primaryContainer.withOpacity(0.5),
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(height: 32),
                    Text(
                      L10n.of(context).initXray,
                      style: Theme.of(context).textTheme.titleLarge?.copyWith(
                            fontWeight: FontWeight.w600,
                            color: colorScheme.onSurface,
                            letterSpacing: 0.2,
                          ),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 12),
                    Text(
                      L10n.of(context).waitLaunchHeyearth,
                      style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                            color: colorScheme.onSurfaceVariant,
                          ),
                      textAlign: TextAlign.center,
                    ),
                    if (widget.selectedOnly) ...[
                      const SizedBox(height: 16),
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                        decoration: BoxDecoration(
                          color: colorScheme.primaryContainer.withOpacity(0.2),
                          borderRadius: BorderRadius.circular(20),
                        ),
                        child: Text(
                          L10n.of(context).modeSelectedOnly,
                          style: Theme.of(context).textTheme.labelLarge?.copyWith(
                                color: colorScheme.primary,
                                fontWeight: FontWeight.w500,
                              ),
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _UserIdCard extends StatelessWidget {
  final String userId;

  const _UserIdCard({required this.userId});

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      color: colorScheme.surfaceContainerHighest,
      child: InkWell(
        onTap: () {
          Clipboard.setData(ClipboardData(text: userId));
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(L10n.of(context).idCopied), behavior: SnackBarBehavior.floating),
          );
        },
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Row(
            children: [
              Icon(Icons.badge_outlined, color: colorScheme.onSurfaceVariant, size: 22),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      L10n.of(context).myHeyearthId,
                      style: Theme.of(context).textTheme.labelMedium?.copyWith(
                            color: colorScheme.onSurfaceVariant,
                          ),
                    ),
                    const SizedBox(height: 2),
                    SelectableText(
                      userId,
                      style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                            fontWeight: FontWeight.w500,
                            fontFamily: 'monospace',
                            color: colorScheme.onSurface,
                          ),
                    ),
                  ],
                ),
              ),
              Icon(Icons.copy, size: 20, color: colorScheme.onSurfaceVariant),
            ],
          ),
        ),
      ),
    );
  }
}

class _SelectedOnlyCard extends StatelessWidget {
  final bool selectedOnly;
  final VoidCallback onToggle;

  const _SelectedOnlyCard({
    required this.selectedOnly,
    required this.onToggle,
  });

  static const _serviceLabels = [
    'YouTube', 'Instagram', 'LinkedIn', 'Spotify', 'OpenAI', 'Telegram',
  ];

  @override
  Widget build(BuildContext context) {
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Expanded(
                  child: Text(
                    L10n.of(context).onlyForSelectedServices,
                    style: Theme.of(context).textTheme.titleSmall?.copyWith(
                          fontWeight: FontWeight.w600,
                          color: Theme.of(context).colorScheme.onSurface,
                        ),
                  ),
                ),
                Switch(
                  value: selectedOnly,
                  onChanged: (_) => onToggle(),
                ),
              ],
            ),
            const SizedBox(height: 6),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: _serviceLabels
                  .map((l) => Chip(
                        label: Text(l, style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurface)),
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        visualDensity: VisualDensity.compact,
                        backgroundColor: Theme.of(context).colorScheme.surfaceContainerHighest,
                      ))
                  .toList(),
            ),
            const SizedBox(height: 6),
            Text(
              L10n.of(context).onlyForSelectedHint,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ThemeModeCard extends StatelessWidget {
  final ThemeMode themeMode;
  final ValueChanged<ThemeMode> onChanged;

  const _ThemeModeCard({required this.themeMode, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              L10n.of(context).theme,
              style: Theme.of(context).textTheme.titleSmall?.copyWith(
                    fontWeight: FontWeight.w600,
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
            ),
            const SizedBox(height: 12),
            SegmentedButton<ThemeMode>(
              segments: [
                ButtonSegment<ThemeMode>(
                  value: ThemeMode.system,
                  icon: const Icon(Icons.brightness_auto, size: 20),
                  label: Text(L10n.of(context).system),
                ),
                ButtonSegment<ThemeMode>(
                  value: ThemeMode.light,
                  icon: const Icon(Icons.light_mode, size: 20),
                  label: Text(L10n.of(context).light),
                ),
                ButtonSegment<ThemeMode>(
                  value: ThemeMode.dark,
                  icon: const Icon(Icons.dark_mode, size: 20),
                  label: Text(L10n.of(context).dark),
                ),
              ],
              selected: {themeMode},
              onSelectionChanged: (Set<ThemeMode> selected) {
                onChanged(selected.first);
              },
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
    final l10n = L10n.of(context);
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              l10n.language,
              style: Theme.of(context).textTheme.titleSmall?.copyWith(
                    fontWeight: FontWeight.w600,
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
            ),
            const SizedBox(height: 12),
            SegmentedButton<String>(
              segments: [
                ButtonSegment<String>(
                  value: 'ru',
                  label: Text(l10n.langRu),
                ),
                ButtonSegment<String>(
                  value: 'en',
                  label: Text(l10n.langEn),
                ),
              ],
              selected: {locale},
              onSelectionChanged: (Set<String> selected) {
                onLocaleChanged(selected.first);
              },
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
    final colorScheme = Theme.of(context).colorScheme;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.phone_android, size: 64, color: colorScheme.outline),
            const SizedBox(height: 16),
            Text(
              L10n.of(context).unsupportedTitle,
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                    color: colorScheme.onSurfaceVariant,
                  ),
            ),
            const SizedBox(height: 8),
            Text(
              L10n.of(context).unsupportedHint,
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: colorScheme.onSurfaceVariant,
                  ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  final String message;

  const _ErrorBanner({required this.message});

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return Material(
      color: colorScheme.errorContainer,
      borderRadius: BorderRadius.circular(12),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Row(
          children: [
            Icon(Icons.warning_amber_rounded, color: colorScheme.onErrorContainer, size: 24),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                message,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                      color: colorScheme.onErrorContainer,
                      fontSize: 14,
                    ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Кнопка VPN с плавной анимацией масштаба при смене состояния.
class _AnimatedMainVpnButton extends StatelessWidget {
  final bool vpnOn;
  final bool connecting;
  final VoidCallback onToggle;

  const _AnimatedMainVpnButton({
    required this.vpnOn,
    required this.connecting,
    required this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    return TweenAnimationBuilder<double>(
      key: ValueKey('$vpnOn-$connecting'),
      tween: Tween(begin: 0.96, end: 1.0),
      duration: const Duration(milliseconds: 220),
      curve: Curves.easeOutCubic,
      builder: (context, scale, child) {
        return Transform.scale(
          scale: scale,
          child: child,
        );
      },
      child: _MainVpnButton(
        vpnOn: vpnOn,
        connecting: connecting,
        onToggle: onToggle,
      ),
    );
  }
}

class _MainVpnButton extends StatelessWidget {
  final bool vpnOn;
  final bool connecting;
  final VoidCallback onToggle;

  const _MainVpnButton({
    required this.vpnOn,
    required this.connecting,
    required this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
      color: vpnOn ? Theme.of(context).colorScheme.primaryContainer : null,
      child: InkWell(
        onTap: connecting ? null : onToggle,
        borderRadius: BorderRadius.circular(20),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 32, horizontal: 24),
          child: Column(
            children: [
              if (connecting)
                const SizedBox(
                  height: 48,
                  width: 48,
                  child: CircularProgressIndicator(strokeWidth: 3),
                )
              else
                Icon(
                  vpnOn ? Icons.vpn_lock_rounded : Icons.vpn_lock_outlined,
                  size: 56,
                  color: vpnOn
                      ? Theme.of(context).colorScheme.onPrimaryContainer
                      : Theme.of(context).colorScheme.primary,
                ),
              const SizedBox(height: 16),
              Text(
                connecting
                    ? L10n.of(context).connecting
                    : vpnOn
                        ? L10n.of(context).turnOffHeyearth
                        : L10n.of(context).turnOnHeyearth,
                style: Theme.of(context).textTheme.titleLarge?.copyWith(
                      fontWeight: FontWeight.w600,
                      color: vpnOn
                          ? Theme.of(context).colorScheme.onPrimaryContainer
                          : Theme.of(context).colorScheme.primary,
                    ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Невидимый виджет: при первом построении через 3 с проверяет обновления и показывает диалог при наличии.
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
    if (_done) return;
    await Future.delayed(const Duration(seconds: 3));
    if (!mounted) return;
    final result = await checkForUpdate(widget.apiBase);
    final update = result.update;
    if (!mounted || update == null || !update.hasUrl) {
      if (mounted) _done = true;
      return;
    }
    _done = true;
    if (!mounted) return;
    final state = Provider.of<AppState>(context, listen: false);
    final l10n = L10n(Locale(state.locale));
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.updateAvailable),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(l10n.versionAvailable(update.latestVersion)),
              if (update.releaseNotes != null && update.releaseNotes!.isNotEmpty) ...[
                const SizedBox(height: 12),
                Text(update.releaseNotes!, style: Theme.of(ctx).textTheme.bodySmall),
              ],
            ],
          ),
        ),
        actions: [
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
        if (version == null || version.isEmpty) return const SizedBox.shrink();
        return Text(
          L10n.of(context).versionLabel(version),
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: Theme.of(context).colorScheme.onSurfaceVariant.withOpacity(0.8),
              ),
          textAlign: TextAlign.center,
        );
      },
    );
  }
}
