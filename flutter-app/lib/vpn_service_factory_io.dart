import 'dart:io' show Platform;

import 'package:heyearth_vpn/vpn_service_interface.dart';
import 'package:heyearth_vpn/vpn_service_io.dart';
import 'package:heyearth_vpn/vpn_service_windows.dart';

VpnService createVpnService() {
  if (Platform.isWindows) return VpnServiceWindows();
  return VpnServiceIo();
}
