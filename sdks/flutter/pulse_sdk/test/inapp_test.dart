import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pulse_sdk/pulse_sdk.dart';

import 'support.dart';

Map<String, dynamic> campaign(
  int id, {
  String layout = 'modal',
  String event = 'app_open',
  String? screen,
  int delay = 0,
  String frequency = 'once',
  List<Map<String, dynamic>>? buttons,
  String? position,
}) =>
    {
      'id': id,
      'layout': layout,
      'content': {
        'title': 'Hello $id',
        'body': 'Body $id',
        'background': '#102030',
        'text_color': '#ffffff',
        'button_color': '#ff8800',
        if (buttons != null) 'buttons': buttons,
        if (position != null) 'position': position,
      },
      'trigger': {
        'trigger_event': event,
        if (screen != null) 'screen': screen,
        if (delay > 0) 'delay_seconds': delay,
        'frequency': frequency,
      },
    };

const urlButton = {'text': 'Shop now', 'action': 'url', 'value': 'myapp://shop'};
const dismissButton = {'text': 'Later', 'action': 'dismiss'};

String today([int addDays = 0]) {
  final d = DateTime.now().add(Duration(days: addDays));
  return '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';
}

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

  void serve(List<Map<String, dynamic>> campaigns) =>
      server.json('/v1/web/inapp', {'messages': campaigns});

  Future<Pulse> start() => Pulse.init(config(inApp: true, onInAppAction: actions.add),
      httpClient: server.client, storage: storage);

  // Starts Pulse inside an app whose navigator the SDK shows messages with.
  Future<Pulse> startApp(WidgetTester tester) async {
    final p = await start();
    final key = GlobalKey<NavigatorState>();
    p.attachNavigator(key);
    await tester.pumpWidget(MaterialApp(
      navigatorKey: key,
      home: const Scaffold(body: Center(child: Text('home'))),
    ));
    return p;
  }

  // The inapp_* events recorded so far.
  Future<List<Map<String, dynamic>>> inAppEvents(Pulse p) async {
    await p.flush();
    return [
      for (final e in server.events)
        if ((e['name'] as String).startsWith('inapp_')) e
    ];
  }

  Future<List<String>> inAppNames(Pulse p) async =>
      [for (final e in await inAppEvents(p)) e['name'] as String];

  group('fetch', () {
    tearDown(() {
      if (Pulse.isInitialized) Pulse.instance.dispose();
    });

    test('at init, with id and platform', () async {
      serve([campaign(1)]);
      final p = await start();
      await p.inApp.refresh();
      final first = server.bodies('/v1/web/inapp').first;
      expect(first, {'id': p.currentId, 'platform': 'android'});
      expect(server.to('/v1/web/inapp').first.headers['X-Pulse-Site-Key'], 'sk_test');
      expect(p.inApp.campaigns.single.id, 1);
      final c = p.inApp.campaigns.single;
      expect(c.layout, 'modal');
      expect(c.content.title, 'Hello 1');
      expect(c.content.background, '#102030');
      expect(c.trigger.event, 'app_open');
      expect(c.trigger.frequency, 'once');
    });

    test('again after identify (with the user hash) and after reset', () async {
      serve([]);
      final p = await start();
      await p.identify('u1', userHash: 'h1');
      await p.reset();
      await pumpEventQueue();
      final bodies = server.bodies('/v1/web/inapp');
      expect(bodies, hasLength(3));
      expect(bodies[1], {'id': 'u1', 'user_hash': 'h1', 'platform': 'android'});
      expect(bodies[2], {'id': p.currentId, 'platform': 'android'});
    });

    test('sends ios on iOS', () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
      try {
        serve([]);
        final p = await start();
        await p.inApp.refresh();
        expect(server.bodies('/v1/web/inapp').last['platform'], 'ios');
      } finally {
        debugDefaultTargetPlatformOverride = null;
      }
    });

    test('keeps the cached campaigns when a fetch fails', () async {
      serve([campaign(1)]);
      final p = await start();
      await p.inApp.refresh();
      server.json('/v1/web/inapp', {'error': 'x'}, status: 500);
      await p.inApp.refresh();
      server.fail = true;
      await p.inApp.refresh();
      expect(p.inApp.campaigns.map((c) => c.id), [1]);
    });

    test('is off with inAppMessages: false', () async {
      final p = await Pulse.init(config(), httpClient: server.client, storage: storage);
      await p.track('app_open');
      await p.identify('u1');
      await pumpEventQueue();
      expect(server.to('/v1/web/inapp'), isEmpty);
    });

    test('no navigator: nothing is shown or recorded', () async {
      serve([campaign(1)]);
      final p = await start();
      await p.inApp.refresh();
      await p.track('app_open');
      await Future<void>.delayed(Duration.zero);
      expect(p.inApp.isShowing, isFalse);
      expect(await inAppNames(p), isEmpty);
      expect(storage.values.containsKey('pulse.inapp'), isFalse);
    });
  });

  group('triggers', () {
    testWidgets('first matching campaign in priority order wins', (tester) async {
      serve([campaign(1, event: 'add_to_cart'), campaign(2), campaign(3)]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      expect(find.text('Hello 2'), findsOneWidget);
      expect(find.text('Hello 3'), findsNothing);
      expect(find.text('Hello 1'), findsNothing);
      expect(await inAppEvents(p), [
        containsPair('props', {'inapp_id': 2})
      ]);
      p.dispose();
    });

    testWidgets('a campaign that waits for the first fetch still shows', (tester) async {
      serve([campaign(1)]);
      final key = GlobalKey<NavigatorState>();
      await tester.pumpWidget(MaterialApp(navigatorKey: key, home: const SizedBox()));
      final p = await start();
      p.attachNavigator(key);
      await p.track('app_open'); // init's fetch hasn't finished yet
      await tester.pumpAndSettle();
      expect(find.text('Hello 1'), findsOneWidget);
      p.dispose();
    });

    testWidgets('screen trigger matches only its screen', (tester) async {
      serve([
        campaign(1, event: 'screen_view', screen: 'Checkout'),
        campaign(2, event: 'screen_view', frequency: 'always'),
      ]);
      final p = await startApp(tester);
      await p.screen('Home');
      await tester.pumpAndSettle();
      expect(find.text('Hello 2'), findsOneWidget); // any screen
      await tester.tap(find.byTooltip('Close'));
      await tester.pumpAndSettle();

      await p.screen('Checkout');
      await tester.pumpAndSettle();
      expect(find.text('Hello 1'), findsOneWidget);
      await tester.tap(find.byTooltip('Close'));
      await tester.pumpAndSettle();

      // track('screen_view', {'screen': ...}) works like screen().
      await p.track('screen_view', {'screen': 'Checkout'});
      await tester.pumpAndSettle();
      expect(find.text('Hello 2'), findsOneWidget); // 1 was once
      await tester.tap(find.byTooltip('Close'));
      await tester.pumpAndSettle();
      p.dispose();
    });

    testWidgets('waits delay_seconds', (tester) async {
      serve([campaign(1, delay: 3)]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pump(const Duration(seconds: 2));
      expect(find.text('Hello 1'), findsNothing);
      await tester.pump(const Duration(seconds: 1));
      await tester.pumpAndSettle();
      expect(find.text('Hello 1'), findsOneWidget);
      p.dispose();
    });

    testWidgets('a campaign past end_at is not shown', (tester) async {
      serve([
        {...campaign(1), 'end_at': '2020-01-01T00:00:00Z'}
      ]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      expect(find.text('Hello 1'), findsNothing);
      p.dispose();
    });
  });

  group('frequency', () {
    Future<void> trigger(WidgetTester tester, Pulse p) async {
      await p.track('app_open');
      await tester.pumpAndSettle();
    }

    Future<void> close(WidgetTester tester) async {
      await tester.tap(find.byTooltip('Close'));
      await tester.pumpAndSettle();
    }

    testWidgets('once is remembered across a new Pulse instance', (tester) async {
      serve([campaign(7, frequency: 'once')]);
      var p = await startApp(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsOneWidget);
      await close(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsNothing);
      expect(jsonDecode(storage.values['pulse.inapp']!)['once'], [7]);
      p.dispose();

      p = await startApp(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsNothing);
      p.dispose();
    });

    testWidgets('session shows once per app run', (tester) async {
      serve([campaign(7, frequency: 'session')]);
      var p = await startApp(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsOneWidget);
      await close(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsNothing);
      p.dispose();

      p = await startApp(tester); // a new run
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsOneWidget);
      await close(tester);
      p.dispose();
    });

    testWidgets('daily shows once per calendar day', (tester) async {
      serve([campaign(7, frequency: 'daily')]);
      storage.values['pulse.inapp'] = jsonEncode({
        'daily': {'7': today()}
      });
      var p = await startApp(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsNothing); // already shown today
      p.dispose();

      storage.values['pulse.inapp'] = jsonEncode({
        'daily': {'7': today(-1)}
      });
      p = await startApp(tester);
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsOneWidget); // last shown yesterday
      await close(tester);
      expect(jsonDecode(storage.values['pulse.inapp']!)['daily'], {'7': today()});
      await trigger(tester, p);
      expect(find.text('Hello 7'), findsNothing);
      p.dispose();
    });

    testWidgets('always shows every time', (tester) async {
      serve([campaign(7, frequency: 'always')]);
      final p = await startApp(tester);
      for (var i = 0; i < 3; i++) {
        await trigger(tester, p);
        expect(find.text('Hello 7'), findsOneWidget);
        await close(tester);
      }
      expect(await inAppNames(p), [for (var i = 0; i < 3; i++) ...['inapp_shown', 'inapp_dismissed']]);
      p.dispose();
    });
  });

  group('rendering and events', () {
    // Runs the 0 s delay and builds the banner (no pumpAndSettle: the banner has an 8 s timer).
    Future<void> showBanner(WidgetTester tester) async {
      for (var i = 0; i < 3; i++) {
        await tester.pump(const Duration(milliseconds: 10));
      }
    }

    testWidgets('modal: url button calls onInAppAction and records the click', (tester) async {
      serve([
        campaign(5, buttons: [urlButton, dismissButton])
      ]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      expect(find.text('Hello 5'), findsOneWidget);
      expect(find.text('Body 5'), findsOneWidget);
      final card = tester.widget<Material>(find
          .ancestor(of: find.text('Hello 5'), matching: find.byType(Material))
          .first);
      expect(card.color, const Color(0xFF102030));

      await tester.tap(find.text('Shop now'));
      await tester.pumpAndSettle();
      expect(find.text('Hello 5'), findsNothing);
      expect(actions, ['myapp://shop']);
      final events = await inAppEvents(p);
      expect(events.map((e) => e['name']), ['inapp_shown', 'inapp_clicked']);
      expect(events[0]['props'], {'inapp_id': 5});
      expect(events[1]['props'], {'inapp_id': 5, 'button': 'Shop now', 'url': 'myapp://shop'});
      expect(events[1]['external_id'], p.currentId);
      p.dispose();
    });

    testWidgets('modal: dismiss button records inapp_dismissed', (tester) async {
      serve([
        campaign(5, buttons: [urlButton, dismissButton])
      ]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      await tester.tap(find.text('Later'));
      await tester.pumpAndSettle();
      expect(find.text('Hello 5'), findsNothing);
      expect(actions, isEmpty);
      final events = await inAppEvents(p);
      expect(events.map((e) => e['name']), ['inapp_shown', 'inapp_dismissed']);
      expect(events[1]['props'], {'inapp_id': 5});
      p.dispose();
    });

    testWidgets('modal: tapping outside dismisses', (tester) async {
      serve([campaign(5)]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      await tester.tapAt(const Offset(5, 5));
      await tester.pumpAndSettle();
      expect(find.text('Hello 5'), findsNothing);
      expect(await inAppNames(p), ['inapp_shown', 'inapp_dismissed']);
      p.dispose();
    });

    testWidgets('banner: hides itself after 8 s without a dismissed event', (tester) async {
      serve([campaign(6, layout: 'banner', position: 'top')]);
      final p = await startApp(tester);
      await p.track('app_open');
      await showBanner(tester);
      expect(find.text('Hello 6'), findsOneWidget);
      expect(find.text('home'), findsOneWidget); // the app stays visible
      await tester.pump(const Duration(seconds: 8));
      expect(find.text('Hello 6'), findsNothing);
      expect(await inAppNames(p), ['inapp_shown']);
      p.dispose();
    });

    testWidgets('banner: tapping it follows its link', (tester) async {
      serve([
        campaign(6, layout: 'banner', buttons: [urlButton])
      ]);
      final p = await startApp(tester);
      await p.track('app_open');
      await showBanner(tester);
      await tester.tap(find.text('Body 6'));
      await tester.pump();
      expect(find.text('Hello 6'), findsNothing);
      expect(actions, ['myapp://shop']);
      expect(await inAppNames(p), ['inapp_shown', 'inapp_clicked']);
      p.dispose();
    });

    testWidgets('banner: close records inapp_dismissed', (tester) async {
      serve([campaign(6, layout: 'banner')]);
      final p = await startApp(tester);
      await p.track('app_open');
      await showBanner(tester);
      await tester.tap(find.byTooltip('Close'));
      await tester.pump();
      expect(find.text('Hello 6'), findsNothing);
      expect(await inAppNames(p), ['inapp_shown', 'inapp_dismissed']);
      p.dispose();
    });

    testWidgets('fullscreen: covers the app and closes', (tester) async {
      serve([
        campaign(8, layout: 'fullscreen', buttons: [urlButton])
      ]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      expect(find.text('Hello 8'), findsOneWidget);
      expect(find.text('home'), findsNothing);
      await tester.tap(find.byTooltip('Close'));
      await tester.pumpAndSettle();
      expect(find.text('home'), findsOneWidget);
      expect(await inAppNames(p), ['inapp_shown', 'inapp_dismissed']);
      p.dispose();
    });

    testWidgets('only one message at a time', (tester) async {
      serve([
        campaign(1, frequency: 'always'),
        campaign(2, event: 'add_to_cart', frequency: 'always'),
      ]);
      final p = await startApp(tester);
      await p.track('app_open');
      await tester.pumpAndSettle();
      await p.track('add_to_cart');
      await tester.pumpAndSettle();
      expect(find.text('Hello 1'), findsOneWidget);
      expect(find.text('Hello 2'), findsNothing);
      p.dispose();
    });
  });
}
