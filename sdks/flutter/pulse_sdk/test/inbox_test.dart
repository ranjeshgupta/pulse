import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pulse_sdk/pulse_sdk.dart';

import 'support.dart';

Map<String, dynamic> msg(int id, {bool read = false}) => {
      'id': id,
      'message_id': 'm$id',
      'title': 'Title $id',
      'body': 'Body $id',
      if (id.isEven) 'url': 'app://offer/$id',
      'data': {'k': 'v$id'},
      'created_at': '2026-11-01T10:00:00Z',
      if (read) 'read_at': '2026-11-02T10:00:00Z',
      'expires_at': '2026-12-01T10:00:00Z',
    };

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late FakeServer server;

  Future<Pulse> start() =>
      Pulse.init(config(), httpClient: server.client, storage: InMemoryPulseStorage());

  setUp(() {
    server = FakeServer()
      ..json('/v1/web/inbox', {
        'messages': [msg(3), msg(2), msg(1, read: true)],
        'unread': 2,
      });
  });

  tearDown(() {
    if (Pulse.isInitialized) Pulse.instance.dispose();
  });

  group('list', () {
    test('sends id and site key, parses messages and updates the unread count', () async {
      final p = await start();
      final counts = <int>[];
      p.inbox.unread.addListener(() => counts.add(p.inbox.unread.value));

      final page = await p.inbox.list();
      final req = server.requests.single;
      expect(req.method, 'GET');
      expect(req.url.path, '/v1/web/inbox');
      expect(req.url.queryParameters, {'id': p.currentId});
      expect(req.headers['X-Pulse-Site-Key'], 'sk_test');

      expect(page.messages.map((m) => m.id), [3, 2, 1]);
      expect(page.unread, 2);
      expect(page.hasMore, isFalse);
      final m = page.messages[1];
      expect(m.messageId, 'm2');
      expect(m.title, 'Title 2');
      expect(m.url, 'app://offer/2');
      expect(m.data, {'k': 'v2'});
      expect(m.createdAt, DateTime.utc(2026, 11, 1, 10));
      expect(m.isRead, isFalse);
      expect(page.messages[2].isRead, isTrue);
      expect(page.messages[0].url, isNull);
      expect(p.inbox.unreadCount, 2);
      expect(counts, [2]);
    });

    test('sends user_hash for an identified user and before for older pages', () async {
      final p = await start();
      await p.identify('u1', userHash: 'h1');
      await p.inbox.list(before: 40);
      final q = server.to('/v1/web/inbox').single.url.queryParameters;
      expect(q, {'id': 'u1', 'user_hash': 'h1', 'before': '40'});
    });

    test('a signed-in user without a hash gets a PulseException before any request', () async {
      final p = await start();
      await p.identify('u1');
      await expectLater(p.inbox.list(), throwsA(isA<PulseException>()));
      await expectLater(p.inbox.markRead(), throwsA(isA<PulseException>()));
      expect(server.to('/v1/web/inbox'), isEmpty);
    });

    test('403 throws a PulseException that mentions the user hash', () async {
      server.json('/v1/web/inbox', {'error': 'needs user_hash'}, status: 403);
      final p = await start();
      await p.identify('u1', userHash: 'wrong');
      await expectLater(
          p.inbox.list(),
          throwsA(isA<PulseException>()
              .having((e) => e.statusCode, 'statusCode', 403)
              .having((e) => e.message, 'message', contains('user hash'))));
      await expectLater(p.inbox.click(1), throwsA(isA<PulseException>()));
    });

    test('429, 5xx, bad JSON and network errors give an empty page', () async {
      final p = await start();
      for (final status in [429, 500]) {
        server.json('/v1/web/inbox', {'error': 'x'}, status: status);
        final page = await p.inbox.list();
        expect(page.messages, isEmpty, reason: '$status');
        expect(page.unread, 0);
      }
      server.fail = true;
      expect((await p.inbox.list()).messages, isEmpty);
    });

    test('a full page has more', () async {
      server.json('/v1/web/inbox', {
        'messages': [for (var i = 60; i > 10; i--) msg(i)],
        'unread': 50,
      });
      final p = await start();
      final page = await p.inbox.list();
      expect(page.messages, hasLength(50));
      expect(page.hasMore, isTrue);
    });
  });

  group('actions', () {
    late Pulse p;

    setUp(() async {
      p = await start();
      await p.inbox.list();
      server.requests.clear();
    });

    Map<String, dynamic> lastAction() => server.bodies('/v1/web/inbox').last;

    test('markRead sends ids and lowers the unread count', () async {
      server.json('/v1/web/inbox', {'changed': 1});
      expect(await p.inbox.markRead([3]), 1);
      expect(server.requests.single.method, 'POST');
      expect(server.requests.single.headers['X-Pulse-Site-Key'], 'sk_test');
      expect(lastAction(), {'id': p.currentId, 'action': 'read', 'message_ids': [3]});
      expect(p.inbox.unreadCount, 1);
    });

    test('markRead() marks everything read', () async {
      server.json('/v1/web/inbox', {'changed': 2});
      await p.inbox.markRead();
      expect(lastAction(), {'id': p.currentId, 'action': 'read', 'message_ids': <int>[]});
      expect(p.inbox.unreadCount, 0);
    });

    test('an empty id list sends nothing', () async {
      expect(await p.inbox.markRead([]), 0);
      expect(await p.inbox.delete([]), 0);
      expect(server.requests, isEmpty);
    });

    test('delete sends ids and lowers the count for unread ones only', () async {
      server.json('/v1/web/inbox', {'changed': 2});
      expect(await p.inbox.delete([1, 2]), 2);
      expect(lastAction(), {'id': p.currentId, 'action': 'delete', 'message_ids': [1, 2]});
      expect(p.inbox.unreadCount, 1); // 1 was already read
    });

    test('delete() deletes everything', () async {
      server.json('/v1/web/inbox', {'changed': 3});
      await p.inbox.delete();
      expect(lastAction()['message_ids'], isEmpty);
      expect(lastAction()['action'], 'delete');
      expect(p.inbox.unreadCount, 0);
    });

    test('click sends exactly one id', () async {
      server.json('/v1/web/inbox', {'changed': 1});
      expect(await p.inbox.click(2), isTrue);
      expect(lastAction(), {'id': p.currentId, 'action': 'click', 'message_ids': [2]});
      expect(p.inbox.unreadCount, 2);
    });

    test('identified users send their hash', () async {
      await p.identify('u1', userHash: 'h1');
      server.json('/v1/web/inbox', {'changed': 1});
      await p.inbox.click(2);
      expect(lastAction(), {'id': 'u1', 'user_hash': 'h1', 'action': 'click', 'message_ids': [2]});
    });

    test('failures return 0', () async {
      server.json('/v1/web/inbox', {'error': 'slow down'}, status: 429);
      expect(await p.inbox.markRead([3]), 0);
      expect(p.inbox.unreadCount, 2);
      server.fail = true;
      expect(await p.inbox.delete([3]), 0);
      expect(await p.inbox.click(3), isFalse);
    });

    test('reset clears the unread count', () async {
      expect(p.inbox.unreadCount, 2);
      await p.reset();
      expect(p.inbox.unreadCount, 0);
    });
  });

  testWidgets('PulseInboxView lists messages and opens one', (tester) async {
    server.json('/v1/web/inbox', {
      'messages': [msg(2), msg(1, read: true)],
      'unread': 1,
    });
    final p = await start();
    final opened = <PulseInboxMessage>[];
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: PulseInboxView(onOpen: opened.add))));
    await tester.pumpAndSettle();
    expect(find.text('Title 2'), findsOneWidget);
    expect(find.text('Body 1'), findsOneWidget);

    server.json('/v1/web/inbox', {'changed': 1});
    await tester.tap(find.text('Title 2'));
    await tester.pumpAndSettle();
    expect(opened.single.id, 2);
    final actions = server.bodies('/v1/web/inbox');
    expect(actions.map((b) => b['action']), ['read', 'click']);
    expect(p.inbox.unreadCount, 0);

    await tester.drag(find.text('Title 1'), const Offset(-600, 0));
    await tester.pumpAndSettle();
    expect(find.text('Title 1'), findsNothing);
    expect(server.bodies('/v1/web/inbox').last, containsPair('action', 'delete'));
    expect(jsonEncode(server.bodies('/v1/web/inbox').last['message_ids']), '[1]');
    p.dispose();
  });
}
