import 'package:flutter/material.dart';

import '../l10n.dart';
import '../models.dart';

class SettingsPage extends StatelessWidget {
  const SettingsPage({
    super.key,
    required this.loginResult,
    required this.themeMode,
    required this.locale,
    required this.onThemeChanged,
    required this.onLocaleChanged,
    required this.onLogout,
  });

  final LoginResult? loginResult;
  final ThemeMode themeMode;
  final String locale;
  final Future<void> Function(ThemeMode mode) onThemeChanged;
  final Future<void> Function(String locale) onLocaleChanged;
  final Future<void> Function() onLogout;

  @override
  Widget build(BuildContext context) {
    final l10n = AdminL10n.of(context);
    final admin = loginResult?.admin;
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text(l10n.settingsTitle,
            style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 16),
        Card(
          child: ListTile(
            title: Text(l10n.settingsAccount),
            subtitle: Text(admin == null
                ? 'Unknown'
                : '${admin.username} (${admin.displayName})'),
          ),
        ),
        const SizedBox(height: 16),
        Card(
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
                  selected: {themeMode},
                  onSelectionChanged: (selection) {
                    final selected = selection.isEmpty ? null : selection.first;
                    if (selected != null) {
                      onThemeChanged(selected);
                    }
                  },
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 16),
        Card(
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
                  selected: {locale},
                  onSelectionChanged: (selection) {
                    if (selection.isNotEmpty) {
                      onLocaleChanged(selection.first);
                    }
                  },
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 16),
        Align(
          alignment: Alignment.centerLeft,
          child: FilledButton.icon(
            onPressed: onLogout,
            icon: const Icon(Icons.logout),
            label: Text(l10n.settingsLogout),
          ),
        ),
      ],
    );
  }
}
