import 'package:grpc/grpc.dart';

import 'generated/heyearth/v1/config.pb.dart' as pb;
import 'generated/heyearth/v1/config.pbgrpc.dart';

ClientChannel? _cachedChannel;
ClientApiClient? _cachedClientApi;
String? _cachedApiBase;

ChannelOptions _channelOptions(Uri uri) {
  if (uri.scheme == 'https') {
    return ChannelOptions(credentials: ChannelCredentials.secure());
  }
  return ChannelOptions(credentials: ChannelCredentials.insecure());
}

int _port(Uri uri) {
  if (uri.hasPort) {
    return uri.port;
  }
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

void _ensureClients(String apiBase) {
  if (_cachedChannel != null && _cachedApiBase == apiBase) {
    return;
  }
  if (_cachedChannel != null) {
    _cachedChannel!.shutdown();
  }
  _cachedChannel = createGrpcChannel(apiBase);
  _cachedClientApi = ClientApiClient(_cachedChannel!);
  _cachedApiBase = apiBase;
}

ClientApiClient getClientApi(String apiBase) {
  _ensureClients(apiBase);
  return _cachedClientApi!;
}

pb.DeviceContext buildDeviceContext({
  required String platform,
  required String deviceId,
  required String locale,
  required String linkedVia,
  String deviceLabel = '',
}) {
  return pb.DeviceContext(
    deviceId: deviceId,
    platform: platform,
    deviceLabel: deviceLabel,
    locale: locale,
    linkedVia: linkedVia,
  );
}

Future<pb.CheckAppUpdateResponse?> checkAppUpdateGrpc(
  String apiBase,
  String platform,
) async {
  final client = getClientApi(apiBase);
  return client.checkAppUpdate(pb.CheckAppUpdateRequest(platform: platform));
}

Future<pb.UserProfileResponse?> getMeGrpc({
  required String apiBase,
  required String userId,
  required pb.DeviceContext device,
}) async {
  final client = getClientApi(apiBase);
  return client.getMe(pb.GetMeRequest(userId: userId, device: device));
}

Future<pb.AccessStateResponse?> getAccessStateGrpc({
  required String apiBase,
  required String userId,
  required String platform,
}) async {
  final client = getClientApi(apiBase);
  return client.getAccessState(
    pb.GetAccessStateRequest(userId: userId, platform: platform),
  );
}

Future<pb.ConnectionBundleResponse?> getConnectionBundleGrpc({
  required String apiBase,
  required String userId,
  required String platform,
  bool forceRefresh = false,
}) async {
  final client = getClientApi(apiBase);
  return client.getConnectionBundle(
    pb.GetConnectionBundleRequest(
      userId: userId,
      platform: platform,
      forceRefresh: forceRefresh,
    ),
  );
}

Future<pb.ListSubscriptionsResponse?> listSubscriptionsGrpc({
  required String apiBase,
  required String userId,
}) async {
  final client = getClientApi(apiBase);
  return client.listSubscriptions(pb.ListSubscriptionsRequest(userId: userId));
}

Future<pb.ListPaymentsResponse?> listPaymentsGrpc({
  required String apiBase,
  required String userId,
}) async {
  final client = getClientApi(apiBase);
  return client.listPayments(pb.ListPaymentsRequest(userId: userId));
}

Future<pb.ApplyPromocodeResponse?> applyPromocodeGrpc(
  String apiBase,
  String userId,
  String code,
) async {
  final client = getClientApi(apiBase);
  return client.applyPromocode(
    pb.ApplyPromocodeRequest(userId: userId, code: code),
  );
}
