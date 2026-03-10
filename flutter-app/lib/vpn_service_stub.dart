import 'package:heyearth_vpn/vpn_service_interface.dart';

/// Заглушка для платформ без VPN (web, desktop, тесты).
class VpnServiceStub implements VpnService {
  @override
  bool get isSupported => false;

  @override
  Future<void> connect(String ssUri,
      {bool proxyOnly = false, bool selectedOnly = false}) async {}

  @override
  Future<void> disconnect() async {}
}
