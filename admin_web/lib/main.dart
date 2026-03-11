import 'package:flutter/material.dart';

import 'admin_api_client.dart';
import 'app_theme.dart';
import 'models.dart';
import 'pages/admins_page.dart';
import 'pages/dashboard_page.dart';
import 'pages/groups_page.dart';
import 'pages/payments_page.dart';
import 'pages/promocodes_page.dart';
import 'pages/releases_page.dart';
import 'pages/servers_page.dart';
import 'pages/settings_page.dart';
import 'pages/ssh_keys_page.dart';
import 'pages/subscriptions_page.dart';
import 'pages/users_page.dart';
import 'session_store.dart';

void main() {
  runApp(const AdminApp());
}

enum AdminSection {
  dashboard,
  users,
  groups,
  subscriptions,
  promocodes,
  payments,
  servers,
  sshKeys,
  releases,
  admins,
  settings,
}

class AdminApp extends StatefulWidget {
  const AdminApp({super.key});

  @override
  State<AdminApp> createState() => _AdminAppState();
}

class _AdminAppState extends State<AdminApp> {
  final SessionStore _sessionStore = SessionStore();
  AdminSession? _session;
  LoginResult? _loginResult;
  ThemeMode _themeMode = ThemeMode.system;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _restoreSession();
  }

  Future<void> _restoreSession() async {
    final themeMode = await _sessionStore.loadThemeMode();
    final session = await _sessionStore.load();
    if (!mounted) {
      return;
    }
    setState(() {
      _themeMode = themeMode;
      _session = session;
      _loginResult = session?.loginResult;
      _loading = false;
    });
  }

  Future<void> _handleLogin(LoginResult result) async {
    final session = AdminSession(token: result.token, loginResult: result);
    await _sessionStore.save(session);
    if (!mounted) {
      return;
    }
    setState(() {
      _session = session;
      _loginResult = result;
    });
  }

  Future<void> _handleLogout() async {
    await _sessionStore.clear();
    if (!mounted) {
      return;
    }
    setState(() {
      _session = null;
      _loginResult = null;
    });
  }

  Future<void> _handleThemeChanged(ThemeMode mode) async {
    await _sessionStore.saveThemeMode(mode);
    if (!mounted) {
      return;
    }
    setState(() => _themeMode = mode);
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'HeYearth Admin',
      theme: buildLightTheme(),
      darkTheme: buildDarkTheme(),
      themeMode: _themeMode,
      home: _loading
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : _session == null
              ? LoginScreen(onLoggedIn: _handleLogin)
              : AdminShell(
                  session: _session!,
                  initialLoginResult: _loginResult,
                  themeMode: _themeMode,
                  onThemeChanged: _handleThemeChanged,
                  onLogout: _handleLogout,
                ),
    );
  }
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key, required this.onLoggedIn});

  final Future<void> Function(LoginResult result) onLoggedIn;

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final TextEditingController _userController = TextEditingController();
  final TextEditingController _passController = TextEditingController();
  bool _loading = false;

  @override
  void dispose() {
    _userController.dispose();
    _passController.dispose();
    super.dispose();
  }

  Future<void> _login() async {
    setState(() => _loading = true);
    try {
      final result = await const AdminApiClient().login(
        username: _userController.text,
        password: _passController.text,
      );
      await widget.onLoggedIn(result);
    } on AdminApiException catch (e) {
      _showMessage(e.message);
    } catch (e) {
      _showMessage(e.toString());
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Card(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text(
                      'HeYearth Admin',
                      style: Theme.of(context).textTheme.headlineMedium,
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'Same-origin admin console',
                      style: Theme.of(context).textTheme.bodyMedium,
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 24),
                    TextField(
                      controller: _userController,
                      decoration: const InputDecoration(labelText: 'Username'),
                    ),
                    const SizedBox(height: 16),
                    TextField(
                      controller: _passController,
                      obscureText: true,
                      decoration: const InputDecoration(labelText: 'Password'),
                    ),
                    const SizedBox(height: 24),
                    FilledButton(
                      onPressed: _loading ? null : _login,
                      child: _loading
                          ? const SizedBox(
                              width: 20,
                              height: 20,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Text('Login'),
                    ),
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

class AdminShell extends StatefulWidget {
  const AdminShell({
    super.key,
    required this.session,
    required this.initialLoginResult,
    required this.themeMode,
    required this.onThemeChanged,
    required this.onLogout,
  });

  final AdminSession session;
  final LoginResult? initialLoginResult;
  final ThemeMode themeMode;
  final Future<void> Function(ThemeMode mode) onThemeChanged;
  final Future<void> Function() onLogout;

  @override
  State<AdminShell> createState() => _AdminShellState();
}

class _AdminShellState extends State<AdminShell> {
  LoginResult? _loginResult;
  AdminSection _section = AdminSection.dashboard;

  @override
  void initState() {
    super.initState();
    _loginResult = widget.initialLoginResult;
  }

  bool _can(String permission) {
    final permissions = _loginResult?.permissions ?? const <String>[];
    return permissions.contains(permission);
  }

  List<_SectionItem> get _items => <_SectionItem>[
        if (_can('dashboard.read'))
          const _SectionItem(
              AdminSection.dashboard, 'Dashboard', Icons.dashboard_outlined),
        if (_can('users.read'))
          const _SectionItem(AdminSection.users, 'Users', Icons.people_outline),
        if (_can('groups.read'))
          const _SectionItem(
              AdminSection.groups, 'Groups', Icons.layers_outlined),
        if (_can('subscriptions.read'))
          const _SectionItem(AdminSection.subscriptions, 'Subscriptions',
              Icons.workspace_premium_outlined),
        if (_can('promocodes.read'))
          const _SectionItem(AdminSection.promocodes, 'Promocodes',
              Icons.confirmation_number_outlined),
        if (_can('payments.read'))
          const _SectionItem(
              AdminSection.payments, 'Payments', Icons.payments_outlined),
        if (_can('servers.read'))
          const _SectionItem(
              AdminSection.servers, 'Servers', Icons.dns_outlined),
        if (_can('servers.read'))
          const _SectionItem(
              AdminSection.sshKeys, 'SSH Keys', Icons.key_outlined),
        if (_can('releases.read'))
          const _SectionItem(AdminSection.releases, 'Releases',
              Icons.system_update_alt_outlined),
        if (_can('admins.read'))
          const _SectionItem(AdminSection.admins, 'Admins',
              Icons.admin_panel_settings_outlined),
        const _SectionItem(
            AdminSection.settings, 'Settings', Icons.settings_outlined),
      ];

  Widget _buildSection() {
    final token = widget.session.token;
    switch (_section) {
      case AdminSection.dashboard:
        return DashboardPage(token: token);
      case AdminSection.users:
        return UsersPage(token: token);
      case AdminSection.groups:
        return GroupsPage(token: token);
      case AdminSection.subscriptions:
        return SubscriptionsPage(token: token);
      case AdminSection.promocodes:
        return PromocodesPage(token: token);
      case AdminSection.payments:
        return PaymentsPage(token: token);
      case AdminSection.servers:
        return ServersPage(token: token);
      case AdminSection.sshKeys:
        return SshKeysPage(token: token);
      case AdminSection.releases:
        return ReleasesPage(token: token);
      case AdminSection.admins:
        return AdminsPage(token: token);
      case AdminSection.settings:
        return SettingsPage(
          loginResult: _loginResult,
          themeMode: widget.themeMode,
          onThemeChanged: widget.onThemeChanged,
          onLogout: widget.onLogout,
        );
    }
  }

  @override
  Widget build(BuildContext context) {
    final items = _items;
    final currentIndex = items
        .indexWhere((item) => item.section == _section)
        .clamp(0, items.length - 1);
    if (!items.any((item) => item.section == _section) && items.isNotEmpty) {
      _section = items.first.section;
    }
    return Scaffold(
      appBar: AppBar(
        title: const Text('HeYearth Admin'),
        actions: [
          IconButton(
            tooltip: 'Logout',
            onPressed: widget.onLogout,
            icon: const Icon(Icons.logout),
          ),
        ],
      ),
      body: Row(
        children: [
          NavigationRail(
            selectedIndex: currentIndex,
            onDestinationSelected: (index) =>
                setState(() => _section = items[index].section),
            labelType: NavigationRailLabelType.all,
            destinations: items
                .map((item) => NavigationRailDestination(
                      icon: Icon(item.icon),
                      label: Text(item.label),
                    ))
                .toList(growable: false),
          ),
          const VerticalDivider(width: 1),
          Expanded(child: _buildSection()),
        ],
      ),
    );
  }
}

class _SectionItem {
  const _SectionItem(this.section, this.label, this.icon);

  final AdminSection section;
  final String label;
  final IconData icon;
}
