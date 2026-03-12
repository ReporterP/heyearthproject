import 'dart:async';
import 'package:connectivity_plus/connectivity_plus.dart';
import 'generated/heyearth/v1/config.pb.dart' as pb;

enum NetworkType { wifi, cellular, ethernet, other }

class ConnectionManager {
  final Connectivity _connectivity = Connectivity();
  StreamSubscription<List<ConnectivityResult>>? _sub;

  NetworkType _currentNetwork = NetworkType.other;
  NetworkType get currentNetwork => _currentNetwork;

  void Function(NetworkType)? onNetworkChanged;

  Future<void> init() async {
    final results = await _connectivity.checkConnectivity();
    _currentNetwork = _mapResult(results);
    _sub = _connectivity.onConnectivityChanged.listen((results) {
      final net = _mapResult(results);
      if (net != _currentNetwork) {
        _currentNetwork = net;
        onNetworkChanged?.call(net);
      }
    });
  }

  void dispose() {
    _sub?.cancel();
  }

  NetworkType _mapResult(List<ConnectivityResult> results) {
    if (results.contains(ConnectivityResult.wifi)) return NetworkType.wifi;
    if (results.contains(ConnectivityResult.mobile)) return NetworkType.cellular;
    if (results.contains(ConnectivityResult.ethernet)) return NetworkType.ethernet;
    return NetworkType.other;
  }

  /// Pick the best URI from a list of connection options based on current network.
  /// Returns null if list is empty.
  pb.ConnectionOption? pickBest(List<pb.ConnectionOption> options) {
    if (options.isEmpty) return null;

    final networkKey = _networkKey(_currentNetwork);

    final matching = options.where((o) =>
        o.recommendedNetworks.contains(networkKey) ||
        o.recommendedNetworks.contains('any')).toList();
    if (matching.isNotEmpty) {
      matching.sort((a, b) => a.priority.compareTo(b.priority));
      return matching.first;
    }

    final sorted = List<pb.ConnectionOption>.from(options)
      ..sort((a, b) => a.priority.compareTo(b.priority));
    return sorted.first;
  }

  /// Get the next fallback option after `current` fails.
  pb.ConnectionOption? nextFallback(
      List<pb.ConnectionOption> options, pb.ConnectionOption current) {
    if (options.length <= 1) return null;
    final sorted = List<pb.ConnectionOption>.from(options)
      ..sort((a, b) => a.priority.compareTo(b.priority));
    final idx = sorted.indexWhere((o) => o.uri == current.uri);
    if (idx < 0 || idx >= sorted.length - 1) return null;
    return sorted[idx + 1];
  }

  String _networkKey(NetworkType type) {
    switch (type) {
      case NetworkType.wifi:
        return 'wifi';
      case NetworkType.cellular:
        return 'cellular';
      case NetworkType.ethernet:
        return 'wifi';
      case NetworkType.other:
        return 'any';
    }
  }
}
