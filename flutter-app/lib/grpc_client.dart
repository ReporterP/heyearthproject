import 'package:grpc/grpc.dart';

import 'generated/heyearth/v1/config.pbgrpc.dart';
import 'generated/heyearth/v1/config.pb.dart' as pb;

/// Парсит apiBase (например https://heyearth.ru или http://localhost:8000)
/// и создаёт gRPC канал: для https — TLS на порт 443, для http — без TLS на порт из URL или 50051.
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

/// Создаёт ClientChannel для заданного apiBase (например https://heyearth.ru).
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

/// Создаёт ConfigApiClient для заданного канала.
ConfigApiClient createConfigApiClient(ClientChannel channel) {
  return ConfigApiClient(channel);
}

/// Вызов GetConfig по gRPC.
Future<pb.GetConfigResponse?> getConfigGrpc(String apiBase, String userId) async {
  final channel = createGrpcChannel(apiBase);
  final client = createConfigApiClient(channel);
  try {
    final resp = await client.getConfig(pb.GetConfigRequest(userId: userId));
    return resp;
  } finally {
    await channel.shutdown();
  }
}

/// Вызов CheckAppUpdate по gRPC.
Future<pb.CheckAppUpdateResponse?> checkAppUpdateGrpc(String apiBase) async {
  final channel = createGrpcChannel(apiBase);
  final client = createConfigApiClient(channel);
  try {
    final resp = await client.checkAppUpdate(pb.CheckAppUpdateRequest());
    return resp;
  } finally {
    await channel.shutdown();
  }
}
