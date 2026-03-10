import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:provider/provider.dart';
import 'package:heyearth_vpn/app_state.dart';
import 'package:heyearth_vpn/screens/home_screen.dart';
import 'package:heyearth_vpn/vpn_service_interface.dart';

ThemeData _buildDarkTheme() {
  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    colorScheme: ColorScheme.dark(
      primary: const Color(0xFF81D4FA),
      onPrimary: const Color(0xFF003547),
      primaryContainer: const Color(0xFF004E66),
      onPrimaryContainer: const Color(0xFFB6EAFF),
      surface: const Color(0xFF0F1419),
      onSurface: const Color(0xFFE3E2E6),
      onSurfaceVariant: const Color(0xFFBEC6D0),
      outline: const Color(0xFF88929C),
      error: const Color(0xFFCF6679),
      onError: const Color(0xFF410002),
    ),
    scaffoldBackgroundColor: const Color(0xFF0F1419),
    appBarTheme: const AppBarTheme(
      centerTitle: true,
      elevation: 0,
      backgroundColor: Colors.transparent,
      foregroundColor: Color(0xFFE3E2E6),
    ),
    cardTheme: CardThemeData(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
      color: const Color(0xFF1A2229),
    ),
  );
}

ThemeData _buildLightTheme() {
  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.light,
    colorScheme: ColorScheme.light(
      primary: const Color(0xFF0277BD),
      onPrimary: Colors.white,
      primaryContainer: const Color(0xFFB3E5FC),
      onPrimaryContainer: const Color(0xFF01579B),
      surface: const Color(0xFFF5F5F5),
      onSurface: const Color(0xFF1A1A1A),
      onSurfaceVariant: const Color(0xFF5C5C5C),
      outline: const Color(0xFF9E9E9E),
      error: const Color(0xFFB00020),
      onError: Colors.white,
    ),
    scaffoldBackgroundColor: const Color(0xFFFAFAFA),
    appBarTheme: const AppBarTheme(
      centerTitle: true,
      elevation: 0,
      backgroundColor: Colors.transparent,
      foregroundColor: Color(0xFF1A1A1A),
    ),
    cardTheme: CardThemeData(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
      color: Colors.white,
    ),
  );
}

class HeYearthApp extends StatefulWidget {
  const HeYearthApp({super.key, required this.vpn});
  final VpnService vpn;

  @override
  State<HeYearthApp> createState() => _HeYearthAppState();
}

class _HeYearthAppState extends State<HeYearthApp> {
  late final AppState _state;
  StreamSubscription<Uri>? _linkSubscription;
  final AppLinks _appLinks = AppLinks();

  @override
  void initState() {
    super.initState();
    _state = AppState(vpn: widget.vpn)..load();
    _bindDeepLinks();
  }

  Future<void> _bindDeepLinks() async {
    try {
      final initialUri = await _appLinks.getInitialLink();
      if (initialUri != null) {
        unawaited(_state.handleIncomingUri(initialUri));
      }
    } catch (_) {}

    _linkSubscription = _appLinks.uriLinkStream.listen((uri) {
      unawaited(_state.handleIncomingUri(uri));
    });
  }

  @override
  void dispose() {
    _linkSubscription?.cancel();
    _state.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ChangeNotifierProvider.value(
      value: _state,
      child: Consumer<AppState>(
        builder: (context, state, _) {
          return MaterialApp(
            title: 'HeYearth',
            debugShowCheckedModeBanner: false,
            locale: Locale(state.locale),
            supportedLocales: const [Locale('ru'), Locale('en')],
            localizationsDelegates: const [
              GlobalMaterialLocalizations.delegate,
              GlobalWidgetsLocalizations.delegate,
              GlobalCupertinoLocalizations.delegate,
            ],
            theme: _buildLightTheme(),
            darkTheme: _buildDarkTheme(),
            themeMode: state.themeMode,
            builder: (context, child) {
              final brightness = Theme.of(context).brightness;
              SystemChrome.setSystemUIOverlayStyle(SystemUiOverlayStyle(
                statusBarColor: Colors.transparent,
                statusBarIconBrightness: brightness == Brightness.dark
                    ? Brightness.light
                    : Brightness.dark,
                systemNavigationBarColor: brightness == Brightness.dark
                    ? const Color(0xFF0F1419)
                    : const Color(0xFFFAFAFA),
                systemNavigationBarIconBrightness: brightness == Brightness.dark
                    ? Brightness.light
                    : Brightness.dark,
              ));
              return LayoutBuilder(
                builder: (context, constraints) {
                  if (constraints.maxWidth <= 0 || constraints.maxHeight <= 0) {
                    return const SizedBox.shrink();
                  }
                  return child ?? const SizedBox.shrink();
                },
              );
            },
            home: const HomeScreen(),
          );
        },
      ),
    );
  }
}
