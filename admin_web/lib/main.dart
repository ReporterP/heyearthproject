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
      _showError(e.message);
    } catch (e) {
      _showError(e.toString());
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  void _showError(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
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
  late AdminSession _session;
  List<AdminServer> _servers = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _session = widget.session;
    _fetchServers();
  }

  AdminApiClient get _client => AdminApiClient(_session.apiBase);

  Future<void> _fetchServers() async {
    setState(() => _loading = true);
    try {
      final servers = await _client.fetchServers(_session.token);
      if (!mounted) return;
      setState(() => _servers = servers);
    } on AdminApiException catch (e) {
      if (e.statusCode == 401) {
        await widget.onLogout();
        return;
      }
      _showMessage(e.message);
    } catch (e) {
      _showMessage(e.toString());
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
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

    if (nextValue == null || nextValue.isEmpty) {
      return;
    }
    final nextSession = AdminSession(
      token: _session.token,
      apiBase: nextValue.replaceFirst(RegExp(r'/$'), ''),
    );
    await widget.onSessionUpdated(nextSession);
    if (!mounted) return;
    setState(() => _session = nextSession);
    await _fetchServers();
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
                TextField(
                  controller: nameController,
                  decoration: const InputDecoration(labelText: 'Name'),
                ),
                TextField(
                  controller: ipController,
                  decoration: const InputDecoration(labelText: 'IP address'),
                ),
                TextField(
                  controller: countryController,
                  decoration: const InputDecoration(labelText: 'Country code'),
                ),
                TextField(
                  controller: sshPortController,
                  decoration: const InputDecoration(labelText: 'SSH port'),
                  keyboardType: TextInputType.number,
                ),
                TextField(
                  controller: sshUserController,
                  decoration: const InputDecoration(labelText: 'SSH user'),
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
                final name = nameController.text.trim();
                final ip = ipController.text.trim();
                final country = countryController.text.trim();
                final sshPort = int.tryParse(sshPortController.text.trim()) ?? 22;
                final sshUser = sshUserController.text.trim().isEmpty ? 'root' : sshUserController.text.trim();
                if (name.isEmpty || ip.isEmpty) {
                  return;
                }
                Navigator.of(context).pop(
                  AddServerPayload(
                    name: name,
                    ipAddress: ip,
                    countryCode: country,
                    sshPort: sshPort,
                    sshUser: sshUser,
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

    if (payload == null) {
      return;
    }

    try {
      await _client.addServer(_session.token, payload);
      await _fetchServers();
      _showMessage('Server created');
    } on AdminApiException catch (e) {
      if (e.statusCode == 401) {
        await widget.onLogout();
        return;
      }
      _showMessage(e.message);
    } catch (e) {
      _showMessage(e.toString());
    }
  }

  void _showMessage(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('HeYearth Admin'),
        actions: [
          IconButton(
            tooltip: 'API base',
            onPressed: _openApiBaseDialog,
            icon: const Icon(Icons.link),
          ),
          IconButton(
            tooltip: 'Refresh',
            onPressed: _fetchServers,
            icon: const Icon(Icons.refresh),
          ),
          IconButton(
            tooltip: 'Logout',
            onPressed: widget.onLogout,
            icon: const Icon(Icons.logout),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: _showAddServerDialog,
        child: const Icon(Icons.add),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _fetchServers,
              child: ListView(
                padding: const EdgeInsets.all(16),
                children: [
                  Card(
                    child: ListTile(
                      title: const Text('API base'),
                      subtitle: Text(_session.apiBase),
                    ),
                  ),
                  const SizedBox(height: 12),
                  if (_servers.isEmpty)
                    const Card(
                      child: Padding(
                        padding: EdgeInsets.all(24),
                        child: Text('No servers yet'),
                      ),
                    ),
                  for (final server in _servers)
                    Card(
                      child: ListTile(
                        leading: const Icon(Icons.dns),
                        title: Text(server.name),
                        subtitle: Text(
                          '${server.ipAddress}:${server.sshPort}  ${server.countryCode}',
                        ),
                        trailing: Chip(label: Text(server.status)),
                      ),
                    ),
                ],
              ),
            ),
    );
  }
}
import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;
import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  runApp(const AdminApp());
}

class AdminApp extends StatelessWidget {
  const AdminApp({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'HeYearth Admin',
      theme: ThemeData(
        primarySwatch: Colors.blue,
        useMaterial3: true,
      ),
      home: const LoginScreen(),
    );
  }
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({Key? key}) : super(key: key);

  @override
  _LoginScreenState createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _userController = TextEditingController();
  final _passController = TextEditingController();
  bool _loading = false;

  Future<void> _login() async {
    setState(() => _loading = true);
    try {
      final response = await http.post(
        Uri.parse('http://localhost:8000/api/admin/login'),
        body: jsonEncode({
          'username': _userController.text,
          'password': _passController.text,
        }),
        headers: {'Content-Type': 'application/json'},
      );

      if (response.statusCode == 200) {
        final data = jsonDecode(response.body);
        final prefs = await SharedPreferences.getInstance();
        await prefs.setString('token', data['token']);
        
        if (!mounted) return;
        Navigator.of(context).pushReplacement(
          MaterialPageRoute(builder: (_) => const DashboardScreen()),
        );
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Login failed')),
        );
      }
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Error: $e')),
      );
    } finally {
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Center(
        child: Container(
          width: 300,
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text('HeYearth Admin', style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold)),
              const SizedBox(height: 32),
              TextField(
                controller: _userController,
                decoration: const InputDecoration(labelText: 'Username', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _passController,
                decoration: const InputDecoration(labelText: 'Password', border: OutlineInputBorder()),
                obscureText: true,
              ),
              const SizedBox(height: 24),
              SizedBox(
                width: double.infinity,
                height: 48,
                child: ElevatedButton(
                  onPressed: _loading ? null : _login,
                  child: _loading ? const CircularProgressIndicator() : const Text('Login'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({Key? key}) : super(key: key);

  @override
  _DashboardScreenState createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  List<dynamic> _servers = [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _fetchServers();
  }

  Future<void> _fetchServers() async {
    setState(() => _loading = true);
    try {
      final prefs = await SharedPreferences.getInstance();
      final token = prefs.getString('token');
      
      final response = await http.get(
        Uri.parse('http://localhost:8000/api/admin/servers'),
        headers: {'Authorization': 'Bearer $token'},
      );

      if (response.statusCode == 200) {
        setState(() {
          _servers = jsonDecode(response.body);
        });
      } else if (response.statusCode == 401 || response.statusCode == 403) {
        Navigator.of(context).pushReplacement(MaterialPageRoute(builder: (_) => const LoginScreen()));
      }
    } catch (e) {
      print(e);
    } finally {
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Dashboard'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _fetchServers,
          ),
          IconButton(
            icon: const Icon(Icons.logout),
            onPressed: () async {
              final prefs = await SharedPreferences.getInstance();
              await prefs.remove('token');
              if (!mounted) return;
              Navigator.of(context).pushReplacement(MaterialPageRoute(builder: (_) => const LoginScreen()));
            },
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : ListView.builder(
              padding: const EdgeInsets.all(16),
              itemCount: _servers.length,
              itemBuilder: (context, index) {
                final s = _servers[index];
                return Card(
                  child: ListTile(
                    leading: const Icon(Icons.dns),
                    title: Text(s['name']),
                    subtitle: Text('${s['ip_address']} (${s['country_code']})'),
                    trailing: Chip(
                      label: Text(s['status'] ?? 'unknown'),
                      backgroundColor: s['status'] == 'online' ? Colors.green.shade100 : Colors.grey.shade200,
                    ),
                  ),
                );
              },
            ),
      floatingActionButton: FloatingActionButton(
        onPressed: () {
          // Show add server dialog
        },
        child: const Icon(Icons.add),
      ),
    );
  }
}
