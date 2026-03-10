/// Интерфейс VPN-сервиса (реализации: заглушка для тестов/web, реальная для Android/iOS).
abstract class VpnService {
  bool get isSupported;
  /// [selectedOnly]: если true, VPN остаётся включённым, но через туннель идёт только трафик к выбранным доменам (routing в конфиге).
  Future<void> connect(String ssUri, {bool proxyOnly = false, bool selectedOnly = false});
  Future<void> disconnect();
}
