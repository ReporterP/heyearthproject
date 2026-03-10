import 'package:flutter/material.dart';

import '../models.dart';

class SettingsPage extends StatelessWidget {
  const SettingsPage({
    super.key,
    required this.loginResult,
    required this.themeMode,
    required this.onThemeChanged,
    required this.onLogout,
  });

  final LoginResult? loginResult;
  final ThemeMode themeMode;
  final Future<void> Function(ThemeMode mode) onThemeChanged;
  final Future<void> Function() onLogout;

  @override
  Widget build(BuildContext context) {
    final admin = loginResult?.admin;
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text('Settings', style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 16),
        Card(
          child: ListTile(
            title: const Text('Current admin'),
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
                Text('Theme', style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 12),
                SegmentedButton<ThemeMode>(
                  segments: const [
                    ButtonSegment(
                        value: ThemeMode.system, label: Text('System')),
                    ButtonSegment(value: ThemeMode.light, label: Text('Light')),
                    ButtonSegment(value: ThemeMode.dark, label: Text('Dark')),
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
        Align(
          alignment: Alignment.centerLeft,
          child: FilledButton.icon(
            onPressed: onLogout,
            icon: const Icon(Icons.logout),
            label: const Text('Logout'),
          ),
        ),
      ],
    );
  }
}
