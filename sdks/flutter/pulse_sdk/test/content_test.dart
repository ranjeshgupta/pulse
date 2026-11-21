import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:pulse_sdk/pulse_sdk.dart';

import 'support.dart';

const path = '/v1/web/content';

Map<String, dynamic> banner({String title = 'Summer sale', String? variant = 'vip'}) => {
      'card': {
        'title': title,
        'body': 'Everything 20% off',
        'image_url': 'https://cdn.test/sale.png',
        'button_text': 'Shop now',
        'deep_link': 'myapp://sale',
        'data': {'color': 'red', 'rank': 2},
      },
      if (variant != null) 'variant': variant,
    };

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late FakeServer server;
  late InMemoryPulseStorage storage;
  late List<String> actions;

  setUp(() {
    server = FakeServer();
    storage = InMemoryPulseStorage();
    actions = [];
  });

  // Answers each request with the given slots' content, for the slots it asked for.
  void serve(Map<String, Map<String, dynamic>> content) => server.routes[path] = (req) {
        final asked = ((jsonDecode(req.body) as Map)['slots'] as List).cast<String>();
        return http.Response(
            jsonEncode({
              'content': {
                for (final s in asked)
                  if (content.containsKey(s)) s: content[s]
              }
            }),
            200);
      };

  Future<Pulse> start() => Pulse.init(config(onInAppAction: actions.add),
      httpClient: server.client, storage: storage);

  Future<List<Map<String, dynamic>>> contentEvents(Pulse p) async {
    await p.flush();
    return [
      for (final e in server.events)
        if ((e['name'] as String).startsWith('app_content_')) e
    ];
  }

  group('fetch', () {
    tearDown(() {
      if (Pulse.isInitialized) Pulse.instance.dispose();
    });

    test('sends id, platform and slots with the site key', () async {
      serve({});
      final p = await start();
      await p.content.fetch(['home_banner', 'cart_tip']);
      final req = server.to(path).single;
      expect(req.headers['X-Pulse-Site-Key'], 'sk_test');
      expect(server.bodies(path).single, {
        'id': p.currentId,
        'platform': 'android',
        'slots': ['home_banner', 'cart_tip'],
      });
    });

    test('sends user_hash when identified with one, and ios on iOS', () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
      try {
        serve({});
        final p = await start();
        await p.identify('u1', userHash: 'h1');
        await p.content.fetch(['home_banner']);
        expect(server.bodies(path).last,
            {'id': 'u1', 'user_hash': 'h1', 'platform': 'ios', 'slots': ['home_banner']});
      } finally {
        debugDefaultTargetPlatformOverride = null;
      }
    });

    test('parses card, html, data and variant; caches per slot', () async {
      serve({
        'home_banner': banner(),
        'promo': {'html': '<b>Hi</b>'},
      });
      final p = await start();
      final got = await p.content.fetch(['home_banner', 'promo', 'empty_slot']);
      expect(got.keys, unorderedEquals(['home_banner', 'promo']));
      final c = p.content.cached('home_banner')!;
      expect(c.slot, 'home_banner');
      expect(c.variant, 'vip');
      expect(c.html, isNull);
      expect(c.card!.title, 'Summer sale');
      expect(c.card!.body, 'Everything 20% off');
      expect(c.card!.imageUrl, 'https://cdn.test/sale.png');
      expect(c.card!.buttonText, 'Shop now');
      expect(c.card!.deepLink, 'myapp://sale');
      expect(c.card!.data, {'color': 'red', 'rank': 2});
      final promo = p.content.cached('promo')!;
      expect(promo.card, isNull);
      expect(promo.html, '<b>Hi</b>');
      expect(p.content.cached('empty_slot'), isNull);
      expect(p.content.isFetched('empty_slot'), isTrue);
      expect(p.content.slots, {'home_banner', 'promo', 'empty_slot'});
    });

    test('a slot missing from the answer is removed from the cache', () async {
      serve({'home_banner': banner()});
      final p = await start();
      await p.content.fetch(['home_banner']);
      serve({});
      expect(await p.content.fetch(['home_banner']), isEmpty);
      expect(p.content.cached('home_banner'), isNull);
    });

    test('a failure keeps what was cached and never throws', () async {
      serve({'home_banner': banner()});
      final p = await start();
      await p.content.fetch(['home_banner']);
      for (final status in [400, 429, 500]) {
        server.json(path, {'error': 'x'}, status: status);
        final got = await p.content.fetch(['home_banner']);
        expect(got['home_banner']!.card!.title, 'Summer sale', reason: '$status');
      }
      server.routes[path] = (_) => http.Response('not json', 200);
      await p.content.fetch(['home_banner']);
      server.fail = true;
      await p.content.fetch(['home_banner']);
      expect(p.content.cached('home_banner')!.variant, 'vip');
    });

    test('rejects invalid slot ids and empty lists', () async {
      final p = await start();
      expect(() => p.content.fetch([]), throwsArgumentError);
      expect(() => p.content.fetch(['Home Banner']), throwsArgumentError);
      expect(() => p.content.load('-x'), throwsArgumentError);
      expect(server.to(path), isEmpty);
    });

    test('more than 50 slots go in several requests', () async {
      serve({});
      final p = await start();
      await p.content.fetch([for (var i = 0; i < 60; i++) 's$i']);
      expect([for (final b in server.bodies(path)) (b['slots'] as List).length], [50, 10]);
    });

    test('load coalesces calls in one microtask and uses the fresh cache', () async {
      serve({'a': banner(title: 'A'), 'b': banner(title: 'B')});
      final p = await start();
      final got = await Future.wait([p.content.load('a'), p.content.load('b'), p.content.load('a')]);
      expect(got.map((c) => c?.card?.title), ['A', 'B', 'A']);
      expect(server.bodies(path).single['slots'], ['a', 'b']);
      expect((await p.content.load('a'))!.card!.title, 'A');
      expect(server.to(path), hasLength(1)); // fresh: not fetched again
    });

    test('identify and reset clear the cache and fetch the known slots again', () async {
      serve({'home_banner': banner(variant: 'anon')});
      final p = await start();
      await p.content.fetch(['home_banner']);
      serve({'home_banner': banner(variant: 'vip')});
      await p.identify('u1', userHash: 'h1');
      expect(p.content.cached('home_banner'), isNull); // cleared right away
      await pumpEventQueue();
      expect(p.content.cached('home_banner')!.variant, 'vip');
      expect(server.bodies(path)[1],
          {'id': 'u1', 'user_hash': 'h1', 'platform': 'android', 'slots': ['home_banner']});
      serve({});
      await p.reset();
      await pumpEventQueue();
      expect(p.content.cached('home_banner'), isNull);
      expect(server.bodies(path).last['id'], p.currentId);
      expect(server.bodies(path), hasLength(3));
    });

    test('a fetch that finishes after identify does not overwrite the new person', () async {
      serve({'home_banner': banner(variant: 'old')});
      final p = await start();
      final old = p.content.fetch(['home_banner']); // still running
      serve({'home_banner': banner(variant: 'new')});
      await p.identify('u1');
      await old;
      await pumpEventQueue();
      expect(p.content.cached('home_banner')!.variant, 'new');
    });

    test('notifies listeners when content changes', () async {
      serve({'home_banner': banner()});
      final p = await start();
      var n = 0;
      void listener() => n++;
      p.content.addListener(listener);
      await p.content.fetch(['home_banner']);
      expect(n, 1);
      await p.content.fetch(['home_banner']); // same content
      expect(n, 1);
      p.content.removeListener(listener);
    });

    test('resume does not fetch slots fetched less than 15 minutes ago', () async {
      serve({});
      final p = await start();
      await p.content.fetch(['home_banner']);
      p.didChangeAppLifecycleState(AppLifecycleState.resumed);
      await pumpEventQueue();
      expect(server.to(path), hasLength(1));
    });
  });

  group('PulseSlot', () {
    Future<Pulse> startApp(WidgetTester tester, Widget slot) async {
      final p = await start();
      await tester.pumpWidget(MaterialApp(home: Scaffold(body: Column(children: [slot]))));
      return p;
    }

    testWidgets('draws the card, records viewed once and clicked with the link', (tester) async {
      serve({
        'home_banner': {
          'card': {
            'title': 'Summer sale',
            'body': 'Everything 20% off',
            'button_text': 'Shop now',
            'deep_link': 'myapp://sale',
          },
          'variant': 'vip',
        }
      });
      final p = await startApp(tester, const PulseSlot(id: 'home_banner'));
      await tester.pumpAndSettle();
      expect(find.text('Summer sale'), findsOneWidget);
      expect(find.text('Everything 20% off'), findsOneWidget);
      await tester.pump(); // rebuilds must not record another view
      await tester.tap(find.text('Shop now'));
      await tester.pumpAndSettle();
      expect(actions, ['myapp://sale']);
      final events = await contentEvents(p);
      expect([for (final e in events) e['name']], ['app_content_viewed', 'app_content_clicked']);
      expect(events[0]['props'], {'slot': 'home_banner', 'variant': 'vip'});
      expect(events[1]['props'],
          {'slot': 'home_banner', 'variant': 'vip', 'link': 'myapp://sale'});
      p.dispose();
    });

    testWidgets('several slots on one screen share one request', (tester) async {
      serve({'a': banner(title: 'A'), 'b': banner(title: 'B')});
      final p = await start();
      await tester.pumpWidget(MaterialApp(
          home: Scaffold(
              body: ListView(children: const [
        PulseSlot(id: 'a'),
        PulseSlot(id: 'b'),
        PulseSlot(id: 'c', placeholder: Text('nothing')),
      ]))));
      await tester.pumpAndSettle();
      expect(find.text('A'), findsOneWidget);
      expect(find.text('B'), findsOneWidget);
      expect(find.text('nothing'), findsOneWidget);
      expect(server.bodies(path).single['slots'], ['a', 'b', 'c']);
      p.dispose();
    });

    testWidgets('placeholder while loading and for html-only content', (tester) async {
      serve({
        'promo': {'html': '<b>Hi</b>'}
      });
      final p = await startApp(
          tester, const PulseSlot(id: 'promo', placeholder: Text('loading')));
      expect(find.text('loading'), findsOneWidget);
      await tester.pumpAndSettle();
      expect(find.text('loading'), findsOneWidget); // the default view draws no html
      expect(await contentEvents(p), isEmpty);
      p.dispose();
    });

    testWidgets('a builder draws html and data itself', (tester) async {
      serve({
        'promo': {
          'html': '<b>Hi</b>',
          'card': {
            'data': {'color': 'red'}
          },
          'variant': 'a',
        }
      });
      final p = await startApp(
          tester,
          PulseSlot(
            id: 'promo',
            padding: const EdgeInsets.all(8),
            builder: (context, c) => TextButton(
              onPressed: () => Pulse.instance.content.recordClick(c, link: 'myapp://x'),
              child: Text('${c.html} ${c.card!.data['color']}'),
            ),
          ));
      await tester.pumpAndSettle();
      await tester.tap(find.text('<b>Hi</b> red'));
      await tester.pumpAndSettle();
      expect(actions, ['myapp://x']);
      final events = await contentEvents(p);
      expect([for (final e in events) e['name']], ['app_content_viewed', 'app_content_clicked']);
      p.dispose();
    });

    testWidgets('redraws with the new person\'s content after identify', (tester) async {
      serve({'home_banner': banner(title: 'Hello stranger')});
      final p = await startApp(tester, const PulseSlot(id: 'home_banner'));
      await tester.pumpAndSettle();
      expect(find.text('Hello stranger'), findsOneWidget);
      serve({'home_banner': banner(title: 'Welcome back', variant: 'member')});
      await p.identify('u1');
      await tester.pumpAndSettle();
      expect(find.text('Welcome back'), findsOneWidget);
      final events = await contentEvents(p);
      expect([for (final e in events) e['props']['variant']], ['vip', 'member']);
      p.dispose();
    });

    testWidgets('before Pulse.init it shows the placeholder', (tester) async {
      await tester.pumpWidget(const MaterialApp(
          home: PulseSlot(id: 'home_banner', placeholder: Text('later'))));
      expect(find.text('later'), findsOneWidget);
    });
  });
}
