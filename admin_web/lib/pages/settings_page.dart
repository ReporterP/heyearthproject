import 'package:flutter/material.dart';

import '../admin_api_client.dart';
import '../l10n.dart';
import '../models.dart';

class SettingsPage extends StatefulWidget {
  const SettingsPage({
    super.key,
    required this.token,
    required this.loginResult,
    required this.themeMode,
    required this.locale,
    required this.onThemeChanged,
    required this.onLocaleChanged,
    required this.onLogout,
  });

  final String token;
  final LoginResult? loginResult;
  final ThemeMode themeMode;
  final String locale;
  final Future<void> Function(ThemeMode mode) onThemeChanged;
  final Future<void> Function(String locale) onLocaleChanged;
  final Future<void> Function() onLogout;

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  Map<String, String>? _serverSettings;
  bool _loading = true;
  String? _error;
  String? _savingKey;

  @override
  void initState() {
    super.initState();
    _loadSettings();
  }

  Future<void> _loadSettings() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      _serverSettings =
          await const AdminApiClient().getSettings(widget.token);
    } on AdminApiException catch (e) {
      _error = e.message;
    } catch (e) {
      _error = e.toString();
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _saveSetting(String key, String value) async {
    setState(() => _savingKey = key);
    try {
      await const AdminApiClient().updateSetting(widget.token, key, value);
      _serverSettings?[key] = value;
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
              content: Text('Saved'), duration: Duration(seconds: 1)),
        );
      }
    } on AdminApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.message)),
        );
      }
    } finally {
      if (mounted) setState(() => _savingKey = null);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    final admin = widget.loginResult?.admin;
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text(l10n.settingsTitle,
            style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 16),
        _buildAccountCard(l10n, admin),
        const SizedBox(height: 16),
        _buildThemeCard(l10n),
        const SizedBox(height: 16),
        _buildLanguageCard(l10n),
        const SizedBox(height: 24),
        Text(l10n.settingsServerSection,
            style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 16),
        _buildServerSettingsSection(l10n),
        const SizedBox(height: 24),
        Text(l10n.settingsB2bTariffsSection,
            style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 16),
        _buildB2bTariffsSection(l10n),
        const SizedBox(height: 24),
        Text(l10n.settingsAppBuildSection,
            style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 16),
        _buildAppBuildSection(l10n),
        const SizedBox(height: 24),
        Align(
          alignment: Alignment.centerLeft,
          child: FilledButton.icon(
            onPressed: widget.onLogout,
            icon: const Icon(Icons.logout),
            label: Text(l10n.settingsLogout),
          ),
        ),
      ],
    );
  }

  Widget _buildAccountCard(AdminL10n l10n, AdminProfile? admin) {
    return Card(
      child: ListTile(
        title: Text(l10n.settingsAccount),
        subtitle: Text(admin == null
            ? 'Unknown'
            : '${admin.username} (${admin.displayName})'),
      ),
    );
  }

  Widget _buildThemeCard(AdminL10n l10n) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l10n.settingsTheme,
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 12),
            SegmentedButton<ThemeMode>(
              segments: [
                ButtonSegment(
                    value: ThemeMode.system, label: Text(l10n.themeSystem)),
                ButtonSegment(
                    value: ThemeMode.light, label: Text(l10n.themeLight)),
                ButtonSegment(
                    value: ThemeMode.dark, label: Text(l10n.themeDark)),
              ],
              selected: {widget.themeMode},
              onSelectionChanged: (selection) {
                final selected = selection.isEmpty ? null : selection.first;
                if (selected != null) {
                  widget.onThemeChanged(selected);
                }
              },
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildLanguageCard(AdminL10n l10n) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l10n.settingsLanguage,
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 12),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'en', label: Text('English')),
                ButtonSegment(value: 'ru', label: Text('Русский')),
              ],
              selected: {widget.locale},
              onSelectionChanged: (selection) {
                if (selection.isNotEmpty) {
                  widget.onLocaleChanged(selection.first);
                }
              },
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildServerSettingsSection(AdminL10n l10n) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            children: [
              Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
              const SizedBox(height: 8),
              FilledButton.icon(
                onPressed: _loadSettings,
                icon: const Icon(Icons.refresh),
                label: Text(l10n.retry),
              ),
            ],
          ),
        ),
      );
    }
    final settings = _serverSettings ?? {};
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _ServerSettingCard(
          title: l10n.settingsApiEndpoints,
          subtitle: l10n.settingsApiEndpointsHint,
          value: settings['api_endpoints'] ?? '[]',
          saving: _savingKey == 'api_endpoints',
          onSave: (v) => _saveSetting('api_endpoints', v),
          multiline: true,
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsYukassaShopId,
          subtitle: l10n.settingsYukassaShopIdHint,
          value: settings['yookassa_shop_id'] ?? '',
          saving: _savingKey == 'yookassa_shop_id',
          onSave: (v) => _saveSetting('yookassa_shop_id', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsYukassaSecretKey,
          subtitle: l10n.settingsYukassaSecretKeyHint,
          value: settings['yookassa_secret_key'] ?? '',
          saving: _savingKey == 'yookassa_secret_key',
          onSave: (v) => _saveSetting('yookassa_secret_key', v),
          obscure: true,
        ),
        const SizedBox(height: 12),
        _ServerSettingToggle(
          title: l10n.settingsYukassaTestMode,
          subtitle: l10n.settingsYukassaTestModeHint,
          value: settings['yookassa_test_mode'] == 'true',
          saving: _savingKey == 'yookassa_test_mode',
          onChanged: (v) => _saveSetting('yookassa_test_mode', v ? 'true' : 'false'),
        ),
      ],
    );
  }

  Widget _buildB2bTariffsSection(AdminL10n l10n) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return const SizedBox.shrink();
    }
    final settings = _serverSettings ?? {};
    final getInt = (String key, int def) =>
        (int.tryParse(settings[key] ?? '') ?? def).toString();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _ServerSettingCard(
          title: l10n.settingsB2bPricePerUser,
          subtitle: l10n.settingsB2bPricePerUserHint,
          value: getInt('b2b_price_per_user', 250),
          saving: _savingKey == 'b2b_price_per_user',
          onSave: (v) => _saveSetting('b2b_price_per_user', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsB2bPriceOurServers,
          subtitle: l10n.settingsB2bPriceOurServersHint,
          value: getInt('b2b_price_our_servers', 1000),
          saving: _savingKey == 'b2b_price_our_servers',
          onSave: (v) => _saveSetting('b2b_price_our_servers', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsB2bBaseOwnServers,
          subtitle: l10n.settingsB2bBaseOwnServersHint,
          value: getInt('b2b_base_own_servers', 5),
          saving: _savingKey == 'b2b_base_own_servers',
          onSave: (v) => _saveSetting('b2b_base_own_servers', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsB2bPriceExtraServer,
          subtitle: l10n.settingsB2bPriceExtraServerHint,
          value: getInt('b2b_price_extra_server', 100),
          saving: _savingKey == 'b2b_price_extra_server',
          onSave: (v) => _saveSetting('b2b_price_extra_server', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsB2bPricePremiumUser,
          subtitle: l10n.settingsB2bPricePremiumUserHint,
          value: getInt('b2b_price_premium_user', 50),
          saving: _savingKey == 'b2b_price_premium_user',
          onSave: (v) => _saveSetting('b2b_price_premium_user', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsB2bPriceUnlimited,
          subtitle: l10n.settingsB2bPriceUnlimitedHint,
          value: getInt('b2b_price_unlimited', 20000),
          saving: _savingKey == 'b2b_price_unlimited',
          onSave: (v) => _saveSetting('b2b_price_unlimited', v),
        ),
      ],
    );
  }

  Widget _buildAppBuildSection(AdminL10n l10n) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return const SizedBox.shrink();
    }
    final settings = _serverSettings ?? {};
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _ServerSettingCard(
          title: l10n.settingsAppBuildRepoUrl,
          subtitle: l10n.settingsAppBuildRepoUrlHint,
          value: settings['app_build_repo_url'] ?? '',
          saving: _savingKey == 'app_build_repo_url',
          onSave: (v) => _saveSetting('app_build_repo_url', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsAppBuildRepoBranch,
          subtitle: l10n.settingsAppBuildRepoBranchHint,
          value: settings['app_build_repo_branch'] ?? 'main',
          saving: _savingKey == 'app_build_repo_branch',
          onSave: (v) => _saveSetting('app_build_repo_branch', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsAppBuildSiteUrl,
          subtitle: l10n.settingsAppBuildSiteUrlHint,
          value: settings['app_build_site_url'] ?? '',
          saving: _savingKey == 'app_build_site_url',
          onSave: (v) => _saveSetting('app_build_site_url', v),
        ),
        const SizedBox(height: 12),
        _ServerSettingCard(
          title: l10n.settingsAppBuildApiBase,
          subtitle: l10n.settingsAppBuildApiBaseHint,
          value: settings['app_build_api_base'] ?? '',
          saving: _savingKey == 'app_build_api_base',
          onSave: (v) => _saveSetting('app_build_api_base', v),
        ),
        const SizedBox(height: 16),
        Text(l10n.settingsAppBuildPlatforms,
            style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        _AppBuildPlatformCheckboxes(
          settings: _serverSettings ?? {},
          savingKey: _savingKey,
          onChanged: _saveSetting,
        ),
        const SizedBox(height: 16),
        FilledButton.icon(
          onPressed: () {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(l10n.settingsAppBuildNotAvailable)),
            );
          },
          icon: const Icon(Icons.build),
          label: Text(l10n.settingsAppBuildNow),
        ),
      ],
    );
  }
}

class _ServerSettingCard extends StatefulWidget {
  const _ServerSettingCard({
    required this.title,
    required this.subtitle,
    required this.value,
    required this.saving,
    required this.onSave,
    this.multiline = false,
    this.obscure = false,
  });

  final String title;
  final String subtitle;
  final String value;
  final bool saving;
  final void Function(String) onSave;
  final bool multiline;
  final bool obscure;

  @override
  State<_ServerSettingCard> createState() => _ServerSettingCardState();
}

class _ServerSettingCardState extends State<_ServerSettingCard> {
  late TextEditingController _controller;
  bool _dirty = false;

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: widget.value);
  }

  @override
  void didUpdateWidget(_ServerSettingCard old) {
    super.didUpdateWidget(old);
    if (old.value != widget.value && !_dirty) {
      _controller.text = widget.value;
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.title,
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 4),
            Text(widget.subtitle,
                style: Theme.of(context).textTheme.bodySmall),
            const SizedBox(height: 12),
            TextField(
              controller: _controller,
              maxLines: widget.multiline ? 4 : 1,
              obscureText: widget.obscure,
              decoration: InputDecoration(
                border: const OutlineInputBorder(),
                isDense: true,
              ),
              onChanged: (_) {
                if (!_dirty) setState(() => _dirty = true);
              },
            ),
            const SizedBox(height: 8),
            Align(
              alignment: Alignment.centerRight,
              child: FilledButton(
                onPressed: widget.saving || !_dirty
                    ? null
                    : () {
                        widget.onSave(_controller.text);
                        setState(() => _dirty = false);
                      },
                child: widget.saving
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2))
                    : Text(l10n.save),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ServerSettingToggle extends StatelessWidget {
  const _ServerSettingToggle({
    required this.title,
    required this.subtitle,
    required this.value,
    required this.saving,
    required this.onChanged,
  });

  final String title;
  final String subtitle;
  final bool value;
  final bool saving;
  final void Function(bool) onChanged;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: SwitchListTile(
        title: Text(title),
        subtitle: Text(subtitle),
        value: value,
        onChanged: saving ? null : onChanged,
      ),
    );
  }
}

class _AppBuildPlatformCheckboxes extends StatelessWidget {
  const _AppBuildPlatformCheckboxes({
    required this.settings,
    required this.savingKey,
    required this.onChanged,
  });

  final Map<String, String> settings;
  final String? savingKey;
  final void Function(String key, String value) onChanged;

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    final platforms = <String, String>{
      'app_build_platform_android': l10n.settingsAppBuildPlatformAndroid,
      'app_build_platform_linux': l10n.settingsAppBuildPlatformLinux,
      'app_build_platform_windows': l10n.settingsAppBuildPlatformWindows,
      'app_build_platform_ios': l10n.settingsAppBuildPlatformIos,
      'app_build_platform_macos': l10n.settingsAppBuildPlatformMacos,
    };
    return Wrap(
      spacing: 16,
      runSpacing: 8,
      children: platforms.entries.map((e) {
        final key = e.key;
        final checked = settings[key] == 'true';
        return FilterChip(
          label: Text(e.value),
          selected: checked,
          onSelected: savingKey != null
              ? null
              : (selected) {
                  onChanged(key, selected ? 'true' : 'false');
                },
        );
      }).toList(),
    );
  }
}
