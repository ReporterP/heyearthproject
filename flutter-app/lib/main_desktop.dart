// Точка входа для Linux / Windows / macOS (без плагина VPN).
// Запуск: flutter run -d linux -t lib/main_desktop.dart

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:heyearth_vpn/app_widget.dart';
import 'package:heyearth_vpn/vpn_service_stub.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setSystemUIOverlayStyle(
    const SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      statusBarIconBrightness: Brightness.dark,
      systemNavigationBarColor: Colors.transparent,
      systemNavigationBarIconBrightness: Brightness.dark,
    ),
  );
  runApp(HeYearthApp(vpn: VpnServiceStub()));
}
