import 'package:grpc/grpc.dart';

import 'generated/heyearth/v1/config.pbgrpc.dart';
import 'generated/heyearth/v1/config.pb.dart' as pb;

ClientChannel? _cachedChannel;
ConfigApiClient? _cachedClient;
String? _cachedApiBase;

ChannelOptions _channelOptions(Uri uri) {
  if (uri.scheme == 'https') {
    return ChannelOptions(credentials: ChannelCredentials.secure());
  }
  return ChannelOptions(credentials: ChannelCredentials.insecure());
}

int _port(Uri uri) {
  if (uri.hasPort) return uri.port;
  return uri.scheme == 'https' ? 443 : 50051;
}

ClientChannel createGrpcChannel(String apiBase) {
  final base = apiBase.replaceFirst(RegExp(r'/$'), '');
  final uri = Uri.parse(base);
  final host = uri.host.isEmpty ? 'localhost' : uri.host;
  return ClientChannel(
    host,
    port: _port(uri),
    options: _channelOptions(uri),
  );
}

ConfigApiClient getClient(String apiBase) {
  if (_cachedClient != null && _cachedApiBase == apiBase) {
    return _cachedClient!;
  }
  
  if (_cachedChannel != null) {
    _cachedChannel!.shutdown();
  }

  _cachedChannel = createGrpcChannel(apiBase);
  _cachedClient = ConfigApiClient(_cachedChannel!);
  _cachedApiBase = apiBase;
  
  return _cachedClient!;
}

Future<pb.GetConfigResponse?> getConfigGrpc(String apiBase, String userId) async {
  final client = getClient(apiBase);
  return await client.getConfig(pb.GetConfigRequest(userId: userId));
}

Future<pb.CheckAppUpdateResponse?> checkAppUpdateGrpc(String apiBase) async {
  final client = getClient(apiBase);
  return await client.checkAppUpdate(pb.CheckAppUpdateRequest());
}

Future<pb.GetProfileResponse?> getProfileGrpc(String apiBase, String userId) async {
  final client = getClient(apiBase);
  return await client.getProfile(pb.GetProfileRequest(userId: userId));
}

Future<pb.ApplyPromocodeResponse?> applyPromocodeGrpc(String apiBase, String userId, String code) async {
  final client = getClient(apiBase);
  return await client.applyPromocode(pb.ApplyPromocodeRequest(userId: userId, code: code));
}
