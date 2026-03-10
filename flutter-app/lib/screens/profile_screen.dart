import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../app_state.dart';
import '../grpc_client.dart';

class ProfileScreen extends StatefulWidget {
  const ProfileScreen({Key? key}) : super(key: key);

  @override
  State<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends State<ProfileScreen> {
  bool _isLoading = true;
  String _currentPlan = 'Loading...';
  String _validUntil = '';
  final _promoController = TextEditingController();
  bool _isApplyingPromo = false;

  @override
  void initState() {
    super.initState();
    _loadProfile();
  }

  @override
  void dispose() {
    _promoController.dispose();
    super.dispose();
  }

  Future<void> _loadProfile() async {
    setState(() => _isLoading = true);
    final state = context.read<AppState>();
    final userId = state.userId;
    if (userId == null || userId.trim().isEmpty) {
      setState(() {
        _currentPlan = 'User ID not set';
        _validUntil = '';
        _isLoading = false;
      });
      return;
    }
    try {
      final resp = await getProfileGrpc(state.apiBase, userId);
      if (!mounted) return;
      if (resp != null) {
        setState(() {
          _currentPlan = resp.currentPlan;
          _validUntil = resp.validUntil;
        });
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _currentPlan = 'Error loading profile';
      });
    } finally {
      if (mounted) {
        setState(() => _isLoading = false);
      }
    }
  }

  Future<void> _applyPromo() async {
    final code = _promoController.text.trim();
    if (code.isEmpty) return;

    setState(() => _isApplyingPromo = true);
    final state = context.read<AppState>();
    final userId = state.userId;
    if (userId == null || userId.trim().isEmpty) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('User ID is missing')),
        );
        setState(() => _isApplyingPromo = false);
      }
      return;
    }
    try {
      final resp = await applyPromocodeGrpc(state.apiBase, userId, code);
      if (!mounted) return;
      if (resp != null && resp.success) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Promocode applied successfully!')),
        );
        _promoController.clear();
        await _loadProfile();
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(resp?.message ?? 'Failed to apply promocode')),
        );
      }
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Error: $e')),
      );
    } finally {
      if (mounted) {
        setState(() => _isApplyingPromo = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    
    return Scaffold(
      appBar: AppBar(
        title: const Text('Profile & Subscription'),
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Card(
                    child: ListTile(
                      leading: const Icon(Icons.person),
                      title: const Text('User ID'),
                      subtitle: Text(state.userId ?? 'Not set'),
                    ),
                  ),
                  const SizedBox(height: 16),
                  Card(
                    child: ListTile(
                      leading: const Icon(Icons.star),
                      title: const Text('Current Plan'),
                      subtitle: Text('$_currentPlan\nValid until: ${_validUntil.isEmpty ? "Forever" : _validUntil}'),
                    ),
                  ),
                  const SizedBox(height: 32),
                  const Text('Apply Promocode', style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold)),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      Expanded(
                        child: TextField(
                          controller: _promoController,
                          decoration: const InputDecoration(
                            hintText: 'Enter code here',
                            border: OutlineInputBorder(),
                          ),
                        ),
                      ),
                      const SizedBox(width: 16),
                      ElevatedButton(
                        onPressed: _isApplyingPromo ? null : _applyPromo,
                        child: _isApplyingPromo
                            ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                            : const Text('Apply'),
                      ),
                    ],
                  ),
                ],
              ),
            ),
    );
  }
}
