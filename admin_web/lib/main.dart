import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import 'admin_api_client.dart';
import 'app_config.dart';
import 'models.dart';
import 'session_store.dart';

void main() {
  runApp(const AdminApp());
}

class AdminApp extends StatefulWidget {
  const AdminApp({super.key});

  @override
  State<AdminApp> createState() => _AdminAppState();
}

class _AdminAppState extends State<AdminApp> {
  final SessionStore _sessionStore = SessionStore();
  AdminSession? _session;
  String _apiBase = kDefaultAdminApiBase;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _restoreSession();
  }

  Future<void> _restoreSession() async {
    final savedApiBase = await _sessionStore.loadApiBase();
    final session = await _sessionStore.load();
    if (!mounted) return;
    setState(() {
      _session = session;
      _apiBase = session?.apiBase ?? savedApiBase ?? kDefaultAdminApiBase;
      _loading = false;
    });
  }

  Future<void> _handleLogin(AdminSession session) async {
    await _sessionStore.save(session);
    if (!mounted) return;
    setState(() {
      _session = session;
      _apiBase = session.apiBase;
    });
  }

  Future<void> _handleLogout() async {
    await _sessionStore.clear();
    if (!mounted) return;
    setState(() => _session = null);
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'HeYearth Admin',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.blue),
        useMaterial3: true,
      ),
      home: _loading
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : _session == null
              ? LoginScreen(
                  initialApiBase: _apiBase,
                  onLoggedIn: _handleLogin,
                )
              : DashboardScreen(
                  session: _session!,
                  onLogout: _handleLogout,
                  onSessionUpdated: _handleLogin,
                ),
    );
  }
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({
    super.key,
    required this.initialApiBase,
    required this.onLoggedIn,
  });

  final String initialApiBase;
  final Future<void> Function(AdminSession session) onLoggedIn;

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  late final TextEditingController _apiBaseController;
  final TextEditingController _userController = TextEditingController();
  final TextEditingController _passController = TextEditingController();
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _apiBaseController = TextEditingController(text: widget.initialApiBase);
  }

  @override
  void dispose() {
    _apiBaseController.dispose();
    _userController.dispose();
    _passController.dispose();
    super.dispose();
  }

  Future<void> _login() async {
    setState(() => _loading = true);
    try {
      final apiBase = _apiBaseController.text.trim().replaceFirst(RegExp(r'/$'), '');
      final client = AdminApiClient(apiBase);
      final token = await client.login(
        username: _userController.text,
        password: _passController.text,
      );
      await widget.onLoggedIn(AdminSession(token: token, apiBase: apiBase));
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
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
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
                      style: Theme.of(context).textTheme.headlineSmall,
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 24),
                    TextField(
                      controller: _apiBaseController,
                      decoration: const InputDecoration(
                        labelText: 'API base',
                        hintText: '/api/admin or https://example.com/api/admin',
                        border: OutlineInputBorder(),
                      ),
                    ),
                    const SizedBox(height: 16),
                    TextField(
                      controller: _userController,
                      decoration: const InputDecoration(
                        labelText: 'Username',
                        border: OutlineInputBorder(),
                      ),
                    ),
                    const SizedBox(height: 16),
                    TextField(
                      controller: _passController,
                      obscureText: true,
                      decoration: const InputDecoration(
                        labelText: 'Password',
                        border: OutlineInputBorder(),
                      ),
                    ),
                    const SizedBox(height: 24),
                    SizedBox(
                      height: 48,
                      child: ElevatedButton(
                        onPressed: _loading ? null : _login,
                        child: _loading
                            ? const SizedBox(
                                width: 20,
                                height: 20,
                                child: CircularProgressIndicator(strokeWidth: 2),
                              )
                            : const Text('Login'),
                      ),
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

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({
    super.key,
    required this.session,
    required this.onLogout,
    required this.onSessionUpdated,
  });

  final AdminSession session;
  final Future<void> Function() onLogout;
  final Future<void> Function(AdminSession session) onSessionUpdated;

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  static const _releasePlatforms = <String>['android', 'windows', 'macos', 'linux', 'ios'];

  late AdminSession _session;
  List<AdminServer> _servers = const [];
  List<AppRelease> _releases = const [];
  bool _loadingServers = true;
  bool _loadingReleases = true;

  AdminApiClient get _client => AdminApiClient(_session.apiBase);

  @override
  void initState() {
    super.initState();
    _session = widget.session;
    _refreshAll();
  }

  Future<void> _refreshAll() async {
    await Future.wait([
      _fetchServers(),
      _fetchReleases(),
    ]);
  }

  Future<void> _fetchServers() async {
    setState(() => _loadingServers = true);
    try {
      final servers = await _client.fetchServers(_session.token);
      if (!mounted) return;
      setState(() => _servers = servers);
    } on AdminApiException catch (e) {
      await _handleApiError(e);
    } catch (e) {
      _showMessage(e.toString());
    } finally {
      if (mounted) {
        setState(() => _loadingServers = false);
      }
    }
  }

  Future<void> _fetchReleases() async {
    setState(() => _loadingReleases = true);
    try {
      final releases = await _client.fetchReleases(_session.token);
      if (!mounted) return;
      setState(() => _releases = releases);
    } on AdminApiException catch (e) {
      await _handleApiError(e);
    } catch (e) {
      _showMessage(e.toString());
    } finally {
      if (mounted) {
        setState(() => _loadingReleases = false);
      }
    }
  }

  Future<void> _handleApiError(AdminApiException error) async {
    if (error.statusCode == 401) {
      await widget.onLogout();
      return;
    }
    _showMessage(error.message);
  }

  void _showMessage(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _openApiBaseDialog() async {
    final controller = TextEditingController(text: _session.apiBase);
    final nextValue = await showDialog<String>(
      context: context,
      builder: (context) {
        return AlertDialog(
          title: const Text('API base'),
          content: TextField(
            controller: controller,
            decoration: const InputDecoration(
              hintText: '/api/admin or https://example.com/api/admin',
              border: OutlineInputBorder(),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              onPressed: () => Navigator.of(context).pop(controller.text.trim()),
              child: const Text('Save'),
            ),
          ],
        );
      },
    );
    controller.dispose();

    if (nextValue == null || nextValue.isEmpty) return;
    final nextSession = AdminSession(
      token: _session.token,
      apiBase: nextValue.replaceFirst(RegExp(r'/$'), ''),
    );
    await widget.onSessionUpdated(nextSession);
    if (!mounted) return;
    setState(() => _session = nextSession);
    await _refreshAll();
  }

  Future<void> _showAddServerDialog() async {
    final nameController = TextEditingController();
    final ipController = TextEditingController();
    final countryController = TextEditingController();
    final sshPortController = TextEditingController(text: '22');
    final sshUserController = TextEditingController(text: 'root');

    final payload = await showDialog<AddServerPayload>(
      context: context,
      builder: (context) {
        return AlertDialog(
          title: const Text('Add server'),
          content: SizedBox(
            width: 420,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(controller: nameController, decoration: const InputDecoration(labelText: 'Name')),
                TextField(controller: ipController, decoration: const InputDecoration(labelText: 'IP address')),
                TextField(controller: countryController, decoration: const InputDecoration(labelText: 'Country code')),
                TextField(
                  controller: sshPortController,
                  decoration: const InputDecoration(labelText: 'SSH port'),
                  keyboardType: TextInputType.number,
                ),
                TextField(controller: sshUserController, decoration: const InputDecoration(labelText: 'SSH user')),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              onPressed: () {
                final name = nameController.text.trim();
                final ip = ipController.text.trim();
                if (name.isEmpty || ip.isEmpty) return;
                Navigator.of(context).pop(
                  AddServerPayload(
                    name: name,
                    ipAddress: ip,
                    countryCode: countryController.text.trim(),
                    sshPort: int.tryParse(sshPortController.text.trim()) ?? 22,
                    sshUser: sshUserController.text.trim().isEmpty ? 'root' : sshUserController.text.trim(),
                  ),
                );
              },
              child: const Text('Add'),
            ),
          ],
        );
      },
    );

    nameController.dispose();
    ipController.dispose();
    countryController.dispose();
    sshPortController.dispose();
    sshUserController.dispose();

    if (payload == null) return;
    try {
      await _client.addServer(_session.token, payload);
      await _fetchServers();
      _showMessage('Server created');
    } on AdminApiException catch (e) {
      await _handleApiError(e);
    } catch (e) {
      _showMessage(e.toString());
    }
  }

  Future<void> _showUploadReleaseDialog() async {
    final picked = await FilePicker.platform.pickFiles(withData: true);
    if (picked == null || picked.files.isEmpty) {
      return;
    }

    final file = picked.files.single;
    final fileBytes = file.bytes;
    if (fileBytes == null || fileBytes.isEmpty) {
      _showMessage('Failed to read selected file');
      return;
    }

    final versionController = TextEditingController();
    final notesController = TextEditingController();
    String platform = _releasePlatforms.first;
    bool activate = true;

    final payload = await showDialog<_ReleaseUploadFormResult>(
      context: context,
      builder: (context) {
        return StatefulBuilder(
          builder: (context, setLocalState) {
            return AlertDialog(
              title: const Text('Upload release'),
              content: SizedBox(
                width: 460,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('File: ${file.name}'),
                    const SizedBox(height: 12),
                    DropdownButtonFormField<String>(
                      initialValue: platform,
                      items: _releasePlatforms
                          .map((value) => DropdownMenuItem<String>(value: value, child: Text(value)))
                          .toList(growable: false),
                      onChanged: (value) {
                        if (value == null) return;
                        setLocalState(() => platform = value);
                      },
                      decoration: const InputDecoration(labelText: 'Platform'),
                    ),
                    const SizedBox(height: 12),
                    TextField(
                      controller: versionController,
                      decoration: const InputDecoration(labelText: 'Version'),
                    ),
                    const SizedBox(height: 12),
                    TextField(
                      controller: notesController,
                      maxLines: 4,
                      decoration: const InputDecoration(labelText: 'Release notes'),
                    ),
                    const SizedBox(height: 8),
                    CheckboxListTile(
                      value: activate,
                      contentPadding: EdgeInsets.zero,
                      title: const Text('Activate immediately'),
                      onChanged: (value) => setLocalState(() => activate = value ?? false),
                    ),
                  ],
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: const Text('Cancel'),
                ),
                ElevatedButton(
                  onPressed: () {
                    if (versionController.text.trim().isEmpty) return;
                    Navigator.of(context).pop(
                      _ReleaseUploadFormResult(
                        platform: platform,
                        version: versionController.text.trim(),
                        releaseNotes: notesController.text.trim(),
                        activate: activate,
                        fileName: file.name,
                        fileBytes: fileBytes,
                      ),
                    );
                  },
                  child: const Text('Upload'),
                ),
              ],
            );
          },
        );
      },
    );

    versionController.dispose();
    notesController.dispose();

    if (payload == null) return;

    try {
      await _client.uploadRelease(
        token: _session.token,
        platform: payload.platform,
        version: payload.version,
        releaseNotes: payload.releaseNotes,
        fileName: payload.fileName,
        fileBytes: payload.fileBytes,
        activate: payload.activate,
      );
      await _fetchReleases();
      _showMessage('Release uploaded');
    } on AdminApiException catch (e) {
      await _handleApiError(e);
    } catch (e) {
      _showMessage(e.toString());
    }
  }

  Future<void> _activateRelease(AppRelease release) async {
    try {
      await _client.activateRelease(_session.token, release.id);
      await _fetchReleases();
      _showMessage('Release activated');
    } on AdminApiException catch (e) {
      await _handleApiError(e);
    } catch (e) {
      _showMessage(e.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('HeYearth Admin'),
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Servers', icon: Icon(Icons.dns)),
              Tab(text: 'Releases', icon: Icon(Icons.system_update)),
            ],
          ),
          actions: [
            IconButton(
              tooltip: 'API base',
              onPressed: _openApiBaseDialog,
              icon: const Icon(Icons.link),
            ),
            IconButton(
              tooltip: 'Refresh',
              onPressed: _refreshAll,
              icon: const Icon(Icons.refresh),
            ),
            IconButton(
              tooltip: 'Logout',
              onPressed: widget.onLogout,
              icon: const Icon(Icons.logout),
            ),
          ],
        ),
        body: TabBarView(
          children: [
            _ServersTab(
              apiBase: _session.apiBase,
              loading: _loadingServers,
              servers: _servers,
              onAddServer: _showAddServerDialog,
              onRefresh: _fetchServers,
            ),
            _ReleasesTab(
              loading: _loadingReleases,
              releases: _releases,
              onUpload: _showUploadReleaseDialog,
              onActivate: _activateRelease,
              onRefresh: _fetchReleases,
            ),
          ],
        ),
      ),
    );
  }
}

class _ServersTab extends StatelessWidget {
  const _ServersTab({
    required this.apiBase,
    required this.loading,
    required this.servers,
    required this.onAddServer,
    required this.onRefresh,
  });

  final String apiBase;
  final bool loading;
  final List<AdminServer> servers;
  final Future<void> Function() onAddServer;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return const Center(child: CircularProgressIndicator());
    }

    return RefreshIndicator(
      onRefresh: onRefresh,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Card(
            child: ListTile(
              title: const Text('API base'),
              subtitle: Text(apiBase),
              trailing: FilledButton.icon(
                onPressed: onAddServer,
                icon: const Icon(Icons.add),
                label: const Text('Add server'),
              ),
            ),
          ),
          const SizedBox(height: 12),
          if (servers.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No servers yet'),
              ),
            ),
          for (final server in servers)
            Card(
              child: ListTile(
                leading: const Icon(Icons.dns),
                title: Text(server.name),
                subtitle: Text('${server.ipAddress}:${server.sshPort}  ${server.countryCode}'),
                trailing: Chip(label: Text(server.status)),
              ),
            ),
        ],
      ),
    );
  }
}

class _ReleasesTab extends StatelessWidget {
  const _ReleasesTab({
    required this.loading,
    required this.releases,
    required this.onUpload,
    required this.onActivate,
    required this.onRefresh,
  });

  final bool loading;
  final List<AppRelease> releases;
  final Future<void> Function() onUpload;
  final Future<void> Function(AppRelease release) onActivate;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return const Center(child: CircularProgressIndicator());
    }

    return RefreshIndicator(
      onRefresh: onRefresh,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Card(
            child: ListTile(
              title: const Text('Application releases'),
              subtitle: const Text('Upload binary files and activate releases per platform'),
              trailing: FilledButton.icon(
                onPressed: onUpload,
                icon: const Icon(Icons.upload_file),
                label: const Text('Upload'),
              ),
            ),
          ),
          const SizedBox(height: 12),
          if (releases.isEmpty)
            const Card(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('No releases uploaded'),
              ),
            ),
          for (final release in releases)
            Card(
              child: ListTile(
                leading: Icon(release.isActive ? Icons.verified : Icons.inventory_2_outlined),
                title: Text('${release.platform}  ${release.version}'),
                subtitle: Text(
                  '${release.fileName}\n${release.publicPath}${release.releaseNotes.isEmpty ? '' : '\n${release.releaseNotes}'}',
                ),
                isThreeLine: true,
                trailing: release.isActive
                    ? const Chip(label: Text('Active'))
                    : OutlinedButton(
                        onPressed: () => onActivate(release),
                        child: const Text('Activate'),
                      ),
              ),
            ),
        ],
      ),
    );
  }
}

class _ReleaseUploadFormResult {
  const _ReleaseUploadFormResult({
    required this.platform,
    required this.version,
    required this.releaseNotes,
    required this.activate,
    required this.fileName,
    required this.fileBytes,
  });

  final String platform;
  final String version;
  final String releaseNotes;
  final bool activate;
  final String fileName;
  final Uint8List fileBytes;
}
