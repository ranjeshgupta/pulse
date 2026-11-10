import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:pulse_sdk/pulse_sdk.dart';

import 'support.dart';

/// An event's props without the device and session props the SDK adds.
Map<String, dynamic> appProps(Map<String, dynamic> e) => {
      ...?(e['props'] as Map?)?.cast<String, dynamic>(),
    }..removeWhere((k, _) => deviceKeys.contains(k) || k == 'session_id');

const deviceKeys = {
  'platform', 'device_type', 'os', 'os_version', 'device_brand', 'device_model', 'app_version', 'locale',
};

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late FakeServer server;
  late InMemoryPulseStorage storage;

  Future<Pulse> start({int maxBatch = 50}) =>
      Pulse.init(config(maxBatch: maxBatch), httpClient: server.client, storage: storage);

  setUp(() {
    server = FakeServer();
    storage = InMemoryPulseStorage();
  });

  tearDown(() {
    if (Pulse.isInitialized) Pulse.instance.dispose();
  });

  group('anonymous id', () {
    test('is anon- plus 24 hex characters and persists across restarts', () async {
      final p = await start();
      expect(p.currentId, matches(RegExp(r'^anon-[0-9a-f]{24}$')));
      expect(p.isIdentified, isFalse);
      final first = p.currentId;
      p.dispose();

      final again = await start();
      expect(again.currentId, first);
    });

    test('differs between installs', () async {
      final a = (await start()).currentId;
      storage = InMemoryPulseStorage();
      final b = (await start()).currentId;
      expect(a, isNot(b));
    });
  });

  group('track', () {
    test('sends name, props, ts, id and the site key', () async {
      final p = await start();
      await p.track('button_clicked', {'button': 'buy', 'at': DateTime.utc(2026, 1, 2)});
      await p.flush();

      final req = server.requests.single;
      expect(req.url.toString(), 'http://pulse.test/v1/web/events');
      expect(req.headers['X-Pulse-Site-Key'], 'sk_test');
      expect(req.headers['Content-Type'], startsWith('application/json'));
      final e = server.events.single;
      expect(e['name'], 'button_clicked');
      expect(e['external_id'], p.currentId);
      expect(e['props'], {'button': 'buy', 'at': '2026-01-02T00:00:00.000Z'});
      expect(e['id'], matches(RegExp(r'^[0-9a-f]{32}$')));
      final ts = DateTime.parse(e['ts'] as String);
      expect(ts.isUtc, isTrue);
      expect(DateTime.now().toUtc().difference(ts).inSeconds.abs(), lessThan(5));
      expect(e.containsKey('attrs'), isFalse);
      expect(p.pendingEvents, 0);
    });

    test('screen records screen_view with props.screen', () async {
      final p = await start();
      await p.screen('Checkout', {'step': 2});
      await p.flush();
      final e = server.events.single;
      expect(e['name'], 'screen_view');
      expect(appProps(e), {'step': 2, 'screen': 'Checkout'});
      expect((e['props'] as Map)['session_id'], p.sessionId);
    });

    test('validates event names', () async {
      final p = await start();
      for (final bad in ['', '1abc', 'has space', 'a' * 65, 'é', 'tag_added', 'unsubscribed', 'list_joined']) {
        expect(() => p.track(bad), throwsArgumentError, reason: bad);
      }
      for (final good in ['a', 'order.completed', 'cart:add', 'x-y_z', 'A${'b' * 63}']) {
        await p.track(good);
      }
      expect(p.pendingEvents, 6); // and session_start
      expect(() => p.screen(''), throwsArgumentError);
    });
  });

  group('identify', () {
    test('sends anonymous_id and switches currentId', () async {
      final p = await start();
      final anon = p.currentId;
      await p.identify('user-42', attributes: {'plan': 'pro'}, email: 'a@example.com');
      await p.flush();

      expect(p.currentId, 'user-42');
      expect(p.isIdentified, isTrue);
      final e = server.events.single;
      expect(e['name'], 'identify');
      expect(e['external_id'], 'user-42');
      expect(appProps(e), {'anonymous_id': anon});
      expect(e['attrs'], {'plan': 'pro'});
      expect(e['email'], 'a@example.com');

      await p.track('after');
      await p.flush();
      expect(server.events.last['external_id'], 'user-42');
    });

    test('flushes immediately', () async {
      final p = await start();
      await p.identify('u1');
      await p.flush(); // joins the flush identify started
      expect(server.requests, hasLength(1));
    });

    test('does not send anonymous_id when switching between users', () async {
      final p = await start();
      await p.identify('u1');
      await p.identify('u2');
      await p.flush();
      expect(appProps(server.events.last), isEmpty);
    });

    test('identity persists across restarts', () async {
      final p = await start();
      await p.identify('u1', userHash: 'abc');
      p.dispose();
      final again = await start();
      expect(again.currentId, 'u1');
      await again.track('x');
      await again.flush();
      expect(server.events.last['props'], {'user_hash': 'abc'});
    });

    test('validates the user id and attribute names', () async {
      final p = await start();
      expect(() => p.identify(''), throwsArgumentError);
      expect(() => p.identify('anon-123'), throwsArgumentError);
      expect(() => p.identify('x' * 256), throwsArgumentError);
      expect(() => p.identify('é' * 128), throwsArgumentError); // 256 bytes
      expect(() => p.identify('u', attributes: {'bad-key': 1}), throwsArgumentError);
      expect(p.isIdentified, isFalse);
    });
  });

  group('identity verification', () {
    test('user_hash is attached to every event of an identified user', () async {
      final p = await start();
      await p.track('before');
      await p.identify('u1', userHash: 'deadbeef');
      await p.track('after', {'k': 'v'});
      await p.flush();

      final events = server.events;
      expect(events[0]['props'], isNull); // anonymous: no hash
      expect((events[1]['props'] as Map)['user_hash'], 'deadbeef');
      expect(events[2]['props'], {'k': 'v', 'user_hash': 'deadbeef'});
    });

    test('identify without a hash forgets the previous one', () async {
      final p = await start();
      await p.identify('u1', userHash: 'h1');
      await p.identify('u2');
      await p.track('x');
      await p.flush();
      expect(server.events.last['props'], isNull);
    });
  });

  group('batching', () {
    test('sends at most maxBatch (50) events per request', () async {
      server.fail = true; // build up a backlog first
      final p = await start();
      for (var i = 0; i < 120; i++) {
        await p.track('e$i');
      }
      await p.flush();
      expect(p.pendingEvents, 121); // and session_start

      server
        ..fail = false
        ..requests.clear();
      await p.flush();
      expect(server.eventRequests.map((b) => (b['events'] as List).length), [50, 50, 21]);
      expect(server.events.map((e) => e['name']), [for (var i = 0; i < 120; i++) 'e$i']);
      expect(p.pendingEvents, 0);
    });

    test('flushes on its own when maxBatch events are queued', () async {
      final p = await start(maxBatch: 5);
      for (var i = 0; i < 5; i++) {
        await p.track('e$i');
      }
      await p.flush(); // waits for the flush the 5th event started
      expect(server.eventRequests.first['events'], hasLength(5));
    });

    test('caps the queue at 1000 events, dropping the oldest', () async {
      server.fail = true;
      final p = await start();
      for (var i = 0; i < 1005; i++) {
        await p.track('e$i');
      }
      expect(p.pendingEvents, 1000);
      server
        ..fail = false
        ..requests.clear();
      await p.flush();
      expect(server.events.first['name'], 'e5');
      expect(server.events, hasLength(1000));
    });
  });

  group('retry', () {
    test('keeps events on 503 and sends them later', () async {
      server.status = 503;
      final p = await start();
      await p.track('a');
      await p.flush();
      expect(p.pendingEvents, 2); // a and session_start

      server.status = 202;
      await p.flush();
      expect(p.pendingEvents, 0);
      expect(server.events.map((e) => e['id']).toSet(), hasLength(1)); // same event both times
    });

    test('keeps events on 429', () async {
      server.status = 429;
      final p = await start();
      await p.track('a');
      await p.flush();
      expect(p.pendingEvents, 2); // a and session_start
    });

    test('keeps events on network errors', () async {
      server.fail = true;
      final p = await start();
      await p.track('a');
      await p.flush();
      expect(p.pendingEvents, 2); // a and session_start
    });

    test('drops the batch on 400', () async {
      server.status = 400;
      final p = await start();
      await p.track('a');
      await p.track('b');
      await p.flush();
      expect(p.pendingEvents, 0);
      expect(server.requests, hasLength(1));
    });

    test('drops the batch on 403 (identity verification failed)', () async {
      server.status = 403;
      final p = await start();
      await p.identify('u1');
      await p.flush();
      expect(p.pendingEvents, 0);
    });
  });

  group('persistence', () {
    test('queued events survive a restart', () async {
      server.fail = true;
      final p = await start();
      await p.track('a', {'n': 1});
      await p.screen('Home');
      p.dispose();

      final again = await start();
      expect(again.pendingEvents, 3); // and session_start
      server.fail = false;
      await again.flush();
      expect(server.events.map((e) => e['name']), ['a', 'screen_view']);
      expect(server.events.first['props'], {'n': 1});

      final third = await start();
      expect(third.pendingEvents, 0);
    });

    test('an unreadable stored queue is discarded', () async {
      storage.values['pulse.queue'] = 'not json';
      final p = await start();
      expect(p.pendingEvents, 0);
    });
  });

  group('reset', () {
    test('starts a new anonymous id and forgets the user and hash', () async {
      final p = await start();
      final anon = p.currentId;
      await p.identify('u1', userHash: 'h');
      await p.reset();

      expect(p.isIdentified, isFalse);
      expect(p.currentId, matches(RegExp(r'^anon-[0-9a-f]{24}$')));
      expect(p.currentId, isNot(anon));
      await p.track('x');
      await p.flush();
      expect(server.events.last['external_id'], p.currentId);
      expect(server.events.last['props'], isNull);
      expect(storage.values.containsKey('pulse.uid'), isFalse);
      expect(storage.values.containsKey('pulse.uh'), isFalse);

      final newAnon = p.currentId;
      p.dispose();
      expect((await start()).currentId, newAnon);
    });
  });

  group('push tokens', () {
    test('registers the token for the current id', () async {
      server.status = 204;
      final p = await start();
      final ok = await p.registerPushToken('fcm-token', platform: PushPlatform.android);
      expect(ok, isTrue);
      final req = server.requests.single;
      expect(req.url.toString(), 'http://pulse.test/v1/web/devices');
      expect(req.headers['X-Pulse-Site-Key'], 'sk_test');
      expect(jsonDecode(req.body), {
        'external_id': p.currentId,
        'token': 'fcm-token',
        'platform': 'android',
      });
    });

    test('includes user_hash for an identified user and re-registers after identify', () async {
      server.status = 204;
      final p = await start();
      await p.registerPushToken('apns-token', platform: PushPlatform.ios);
      await p.identify('u1', userHash: 'h1');
      await p.flush();
      await pumpEventQueue();

      final devices = server.requests
          .where((r) => r.url.path == '/v1/web/devices')
          .map((r) => jsonDecode(r.body))
          .toList();
      expect(devices, hasLength(2));
      expect(devices.last, {
        'external_id': 'u1',
        'token': 'apns-token',
        'platform': 'ios',
        'user_hash': 'h1',
      });
    });

    test('returns false when the server refuses', () async {
      server.status = 403;
      final p = await start();
      expect(await p.registerPushToken('t', platform: PushPlatform.android), isFalse);
      server.fail = true;
      expect(await p.registerPushToken('t', platform: PushPlatform.android), isFalse);
      expect(() => p.registerPushToken('', platform: PushPlatform.ios), throwsArgumentError);
    });
  });

  group('config', () {
    test('rejects bad values', () {
      expect(() => PulseConfig(host: '', siteKey: 'k'), throwsArgumentError);
      expect(() => PulseConfig(host: 'h', siteKey: ''), throwsArgumentError);
      expect(() => PulseConfig(host: 'h', siteKey: 'k', maxBatch: 51), throwsRangeError);
      expect(() => PulseConfig(host: 'h', siteKey: 'k', maxBatch: 0), throwsRangeError);
      expect(PulseConfig(host: 'http://x//', siteKey: 'k').host, 'http://x');
    });

    test('Pulse.instance needs init', () {
      expect(() => Pulse.instance, throwsStateError);
    });
  });
}
