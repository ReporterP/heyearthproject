/// Интерфейс VPN-сервиса (реализации: заглушка для тестов/web, реальная для Android/iOS).
abstract class VpnService {
  bool get isSupported;

  /// Connect to the VPN using the provided URI (VLESS, Shadowsocks, etc.).
  Future<void> connect(String uri, {bool proxyOnly = false});
  Future<void> disconnect();

  /// Returns true if traffic is currently flowing through the VPN tunnel.
  /// Used by auto-failover to detect dead connections.
  Future<bool> isTrafficFlowing() async => true;
}
