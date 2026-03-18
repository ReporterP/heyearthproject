import 'dart:convert';
import 'package:http/http.dart' as http;

class PaymentApi {
  PaymentApi(this.apiBase, {this.jwtToken});

  final String apiBase;
  final String? jwtToken;

  Uri _uri(String path) => Uri.parse('${apiBase.replaceFirst(RegExp(r'/$'), '')}$path');

  Map<String, String> _headers() {
    final h = <String, String>{'Content-Type': 'application/json'};
    if (jwtToken != null && jwtToken!.isNotEmpty) {
      h['Authorization'] = 'Bearer $jwtToken';
    }
    return h;
  }

  Future<Map<String, dynamic>> createPayment({
    required String userId,
    required String plan,
    String returnUrl = '',
  }) async {
    final resp = await http.post(
      _uri('/api/v1/payment/create'),
      headers: _headers(),
      body: jsonEncode({
        'user_id': userId,
        'plan': plan,
        'return_url': returnUrl,
      }),
    );
    return _parseResponse(resp);
  }

  Future<Map<String, dynamic>> checkAccess({required String userId}) async {
    final resp = await http.post(
      _uri('/api/v1/payment/check-access'),
      headers: _headers(),
      body: jsonEncode({'user_id': userId}),
    );
    return _parseResponse(resp);
  }

  Future<Map<String, dynamic>> redeemCode({
    required String userId,
    required String code,
    String name = '',
  }) async {
    final resp = await http.post(
      _uri('/api/v1/payment/redeem-code'),
      headers: _headers(),
      body: jsonEncode({
        'user_id': userId,
        'code': code,
        'name': name,
      }),
    );
    return _parseResponse(resp);
  }

  Map<String, dynamic> _parseResponse(http.Response resp) {
    final body = jsonDecode(resp.body) as Map<String, dynamic>;
    if (resp.statusCode >= 400) {
      throw Exception(body['error'] ?? 'Request failed');
    }
    return body;
  }
}
