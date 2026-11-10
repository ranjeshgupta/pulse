// Sends a few real events to a running Pulse server. Skipped unless PULSE_SITE_KEY is set:
//
//   PULSE_SITE_KEY=sk_... PULSE_HOST=http://localhost:7080 flutter test tool/integration_test.dart
//
// It uses in-memory storage, so it leaves nothing behind on this machine. Workspaces with identity
// verification on will answer 403 to the identify step (no user hash is sent); that is reported.
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:pulse_sdk/pulse_sdk.dart';

void main() {
  final siteKey = Platform.environment['PULSE_SITE_KEY'];
  final host = Platform.environment['PULSE_HOST'] ?? 'http://localhost:7080';

  test('sends events to $host', () async {
    final statuses = <String, int>{};
    final client = _Recording(http.Client(), statuses);
    final pulse = await Pulse.init(
      PulseConfig(
          host: host, siteKey: siteKey!, flushOnBackground: false, inAppMessages: false, debug: true),
      httpClient: client,
      storage: InMemoryPulseStorage(),
    );
    final anon = pulse.currentId;
    await pulse.screen('IntegrationCheck');
    await pulse.track('sdk_integration_check', {'sdk': 'flutter', 'at': DateTime.now()});
    await pulse.flush();
    expect(pulse.pendingEvents, 0);
    expect(statuses['/v1/web/events'], 202, reason: 'anonymous events for $anon');

    // Anonymous inbox and in-app campaigns need no user hash.
    final page = await pulse.inbox.list();
    expect(statuses['/v1/web/inbox'], 200);
    await pulse.inApp.refresh();
    expect(statuses['/v1/web/inapp'], 200);
    // ignore: avoid_print
    print('inbox: ${page.messages.length} messages; in-app: ${pulse.inApp.campaigns.length} campaigns');

    await pulse.identify('flutter-sdk-check-${DateTime.now().millisecondsSinceEpoch}',
        attributes: {'sdk': 'flutter'});
    await pulse.flush();
    // ignore: avoid_print
    print('identify answered ${statuses['/v1/web/events']} (403 = identity verification on)');
    pulse.dispose();
  }, skip: siteKey == null ? 'set PULSE_SITE_KEY to run against a server' : false);
}

class _Recording extends http.BaseClient {
  _Recording(this._inner, this._statuses);
  final http.Client _inner;
  final Map<String, int> _statuses;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final res = await _inner.send(request);
    _statuses[request.url.path] = res.statusCode;
    return res;
  }
}
