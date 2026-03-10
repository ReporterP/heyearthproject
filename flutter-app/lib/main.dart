import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:heyearth_vpn/app_widget.dart';
import 'package:heyearth_vpn/vpn_service.dart';

void main() {
  runZonedGuarded(() {
    WidgetsFlutterBinding.ensureInitialized();
    FlutterError.onError = (FlutterErrorDetails details) {
      FlutterError.presentError(details);
      if (kDebugMode) {
        debugPrint('FlutterError: ${details.exception}');
        debugPrint(details.stack?.toString() ?? '');
      }
    };
    runApp(HeYearthApp(vpn: createVpnService()));
  }, (error, stack) {
    if (kDebugMode) {
      debugPrint('Uncaught error: $error');
      debugPrint(stack.toString());
    }
  });
}
