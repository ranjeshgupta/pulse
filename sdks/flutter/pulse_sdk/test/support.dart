import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pulse_sdk/pulse_sdk.dart';

/// Records requests and answers with [status] (or throws when [fail] is set). [routes] answers
/// specific paths instead.
class FakeServer {
  int status = 202;
  bool fail = false;
  final requests = <http.Request>[];
  final routes = <String, http.Response Function(http.Request)>{};

  late final client = MockClient((req) async {
    requests.add(req);
    if (fail) throw http.ClientException('offline', req.url);
    final route = routes[req.url.path];
    if (route != null) return route(req);
    return http.Response(status == 202 ? '' : '{"error":"x"}', status);
  });

  /// Answers [path] with [body] as JSON and [status].
  void json(String path, Object body, {int status = 200}) =>
      routes[path] = (_) => http.Response(jsonEncode(body), status,
          headers: {'content-type': 'application/json'});

  List<http.Request> to(String path) => requests.where((r) => r.url.path == path).toList();

  List<Map<String, dynamic>> bodies(String path) =>
      [
        for (final r in to(path))
          if (r.method == 'POST') (jsonDecode(r.body) as Map).cast<String, dynamic>()
      ];

  List<Map<String, dynamic>> get eventRequests => bodies('/v1/web/events');

  List<Map<String, dynamic>> get events => [
        for (final body in eventRequests)
          for (final e in body['events'] as List) (e as Map).cast<String, dynamic>()
      ];
}

PulseConfig config({int maxBatch = 50, bool inApp = false, void Function(String)? onInAppAction}) =>
    PulseConfig(
      host: 'http://pulse.test/',
      siteKey: 'sk_test',
      flushInterval: const Duration(hours: 1),
      maxBatch: maxBatch,
      flushOnBackground: false,
      inAppMessages: inApp,
      onInAppAction: onInAppAction,
    );
