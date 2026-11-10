import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pulse_sdk/pulse_sdk.dart';
import 'package:pulse_sdk/src/device.dart';

import 'support.dart';

void main() {
  final binding = TestWidgetsFlutterBinding.ensureInitialized();
  late FakeServer server;
  late InMemoryPulseStorage storage;
  late DateTime now;

  Future<Pulse> start({String? appVersion, String? deviceModel, String? deviceBrand, String? osVersion}) =>
      Pulse.init(
        PulseConfig(
          host: 'http://pulse.test',
          siteKey: 'sk_test',
          flushInterval: const Duration(hours: 1),
          flushOnBackground: false,
          inAppMessages: false,
          appVersion: appVersion,
          deviceModel: deviceModel,
          deviceBrand: deviceBrand,
          osVersion: osVersion,
        ),
        httpClient: server.client,
        storage: storage,
        clock: () => now,
      );

  void wait(Duration d) => now = now.add(d);

  List<String> names() => server.allEvents.map((e) => e['name'] as String).toList();

  Map<String, dynamic> propsOf(Map<String, dynamic> e) => (e['props'] as Map).cast<String, dynamic>();

  setUp(() {
    server = FakeServer();
    storage = InMemoryPulseStorage();
    now = DateTime.utc(2026, 11, 11, 9);
    final view = binding.platformDispatcher.views.first;
    view.physicalSize = const Size(1080, 2340);
    view.devicePixelRatio = 3; // 360 x 780 logical: a phone
    binding.platformDispatcher.localeTestValue = const Locale('en', 'IN');
  });

  tearDown(() {
    if (Pulse.isInitialized) Pulse.instance.dispose();
    debugDefaultTargetPlatformOverride = null;
    final view = binding.platformDispatcher.views.first;
    view.resetPhysicalSize();
    view.resetDevicePixelRatio();
    binding.platformDispatcher.clearLocaleTestValue();
  });

  group('cleanOsVersion', () {
    const android = TargetPlatform.android, ios = TargetPlatform.iOS;
    final cases = <(String, TargetPlatform, String?)>[
      ('Version 17.4.1 (Build 21E236)', ios, '17.4'),
      ('Version 18.0 (Build 22A3354)', ios, '18'),
      ('Version 16.7.8 (Build 20H343)', ios, '16.7'),
      ('14', android, '14'),
      ('8.1.0', android, '8.1'),
      ('Android 13', android, '13'),
      ('UP1A.231005.007', android, '14'),
      ('TP1A.220624.014.S918BXXU1AWBD', android, '13'),
      ('SP1A.210812.016', android, '12'),
      ('RQ3A.211001.001', android, '11'),
      ('QP1A.190711.020', android, '10'),
      ('AP2A.240805.005', android, '14'),
      ('AP3A.241005.015', android, '15'),
      ('BP1A.250505.005', android, '16'),
      ('V14.0.6.0.TMRMIXM', android, '13'),
      ('OS1.0.3.0.UMCMIXM', android, '14'),
      ('  ', android, null),
      ('CPH2449_14.0.0.300(EX01)', android, 'CPH2449_14.0.0.300(EX01)'),
    ];
    for (final (raw, platform, want) in cases) {
      test('${platform.name}: "$raw" → $want', () {
        expect(cleanOsVersion(raw, platform), want);
      });
    }

    test('keeps an unreadable value, at most 100 characters', () {
      final raw = 'Linux 6.1.25-android14-11-g34fde9ec08a3 #1 SMP PREEMPT ${'x' * 200}';
      expect(cleanOsVersion(raw, android), raw.substring(0, 100));
    });
  });

  group('buildDeviceProps', () {
    test('Android phone with app-provided values', () {
      expect(
        buildDeviceProps(
          platform: TargetPlatform.android,
          osVersionRaw: 'UP1A.231005.007',
          locale: 'hi-IN',
          shortestSide: 411,
          appVersion: '2.3.0',
          deviceModel: 'SM-S918B',
          deviceBrand: 'samsung',
        ),
        {
          'platform': 'android',
          'device_type': 'mobile',
          'os': 'Android',
          'os_version': '14',
          'device_brand': 'samsung',
          'device_model': 'SM-S918B',
          'app_version': '2.3.0',
          'locale': 'hi-IN',
        },
      );
    });

    test('iPad: tablet from the shortest side, Apple and iPad by default', () {
      expect(
        buildDeviceProps(
            platform: TargetPlatform.iOS, osVersionRaw: 'Version 17.4 (Build 21E219)', shortestSide: 820, locale: 'en-US'),
        {
          'platform': 'ios',
          'device_type': 'tablet',
          'os': 'iOS',
          'os_version': '17.4',
          'device_brand': 'Apple',
          'device_model': 'iPad',
          'locale': 'en-US',
        },
      );
      expect(buildDeviceProps(platform: TargetPlatform.iOS, shortestSide: 390)['device_model'], 'iPhone');
      expect(buildDeviceProps(platform: TargetPlatform.iOS, shortestSide: 390, deviceModel: 'iPhone15,2')['device_model'],
          'iPhone15,2');
    });

    test('600 logical pixels is a tablet; unknown values are left out and long ones cut', () {
      expect(buildDeviceProps(platform: TargetPlatform.android, shortestSide: 600)['device_type'], 'tablet');
      expect(buildDeviceProps(platform: TargetPlatform.android, shortestSide: 599.9)['device_type'], 'mobile');
      expect(buildDeviceProps(platform: TargetPlatform.android, shortestSide: 0, appVersion: ' ', osVersion: ''),
          {'platform': 'android', 'os': 'Android'});
      expect(buildDeviceProps(platform: TargetPlatform.android, appVersion: 'v' * 300)['app_version'], hasLength(100));
    });
  });

  group('nextSession', () {
    final t = DateTime.utc(2026, 1, 1, 12);
    String id() => 'new';

    test('starts on the first event and after 30 minutes idle', () {
      expect(nextSession(t, null, null, id), (sid: 'new', started: true));
      expect(nextSession(t.add(const Duration(minutes: 29)), t, 's1', id), (sid: 's1', started: false));
      expect(nextSession(t.add(const Duration(minutes: 30)), t, 's1', id), (sid: 'new', started: true));
      expect(nextSession(t.add(const Duration(minutes: 31)), t, 's1', id), (sid: 'new', started: true));
    });

    test('keeps the session when the clock goes backwards', () {
      expect(nextSession(t.subtract(const Duration(hours: 3)), t, 's1', id), (sid: 's1', started: false));
    });
  });

  group('device props on events', () {
    test('identify and screen_view carry them; custom events do not', () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.android;
      final p = await start(appVersion: '2.3.0', deviceModel: 'Pixel 8', deviceBrand: 'Google', osVersion: '14');
      await p.identify('u1');
      await p.screen('Home', {'tab': 'deals'});
      await p.track('clicked', {'b': 1});
      await p.flush();

      const device = {
        'platform': 'android',
        'device_type': 'mobile',
        'os': 'Android',
        'os_version': '14',
        'device_brand': 'Google',
        'device_model': 'Pixel 8',
        'app_version': '2.3.0',
        'locale': 'en-IN',
      };
      expect(names(), ['session_start', 'identify', 'screen_view', 'clicked']);
      final [start_, identify, screen, custom] = server.allEvents;
      final sid = p.sessionId!;
      expect(sid, matches(RegExp(r'^[0-9a-f]{24}$')));
      expect(propsOf(start_), {'session_id': sid, ...device});
      expect(propsOf(identify), {...device, 'anonymous_id': startsWith('anon-')});
      expect(propsOf(screen), {...device, 'session_id': sid, 'tab': 'deals', 'screen': 'Home'});
      expect(propsOf(custom), {'b': 1});
      expect(start_['external_id'], 'u1');
      expect(start_['ts'], '2026-11-11T09:00:00.000Z');
      expect(p.deviceProperties, device);
    });

    test('iOS tablet detected from the platform and the view size', () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
      final view = binding.platformDispatcher.views.first;
      view.physicalSize = const Size(1640, 2360);
      view.devicePixelRatio = 2; // 820 x 1180: an iPad
      final p = await start(osVersion: '17.4');
      await p.screen('Home');
      await p.flush();
      expect(propsOf(server.events.single), {
        'platform': 'ios',
        'device_type': 'tablet',
        'os': 'iOS',
        'os_version': '17.4',
        'device_brand': 'Apple',
        'device_model': 'iPad',
        'locale': 'en-IN',
        'session_id': p.sessionId,
        'screen': 'Home',
      });
    });

    test('the OS version is read from the platform when the app does not pass one', () async {
      final p = await start();
      expect(p.deviceProperties['os_version'], isNotEmpty);
      expect(p.deviceProperties['os_version']!.length, lessThanOrEqualTo(100));
    });
  });

  group('sessions', () {
    test('a new session starts after 30 minutes without events, not before', () async {
      final p = await start();
      await p.track('a');
      final first = p.sessionId;
      wait(const Duration(minutes: 29));
      await p.track('b');
      expect(p.sessionId, first);
      wait(const Duration(minutes: 29)); // 58 minutes in, but only 29 since the last event
      await p.screen('Home');
      expect(p.sessionId, first);
      wait(const Duration(minutes: 31));
      await p.screen('Home');
      await p.flush();

      expect(p.sessionId, isNot(first));
      expect(names(), ['session_start', 'a', 'b', 'screen_view', 'session_start', 'screen_view']);
      final starts = server.allEvents.where((e) => e['name'] == 'session_start').toList();
      expect(starts.map((e) => propsOf(e)['session_id']), [first, p.sessionId]);
      expect(propsOf(server.allEvents.last)['session_id'], p.sessionId);
    });

    test('30+ minutes in the background starts a session on resume; a short break does not', () async {
      final p = await start();
      await p.track('a');
      final first = p.sessionId;

      wait(const Duration(minutes: 20));
      p.didChangeAppLifecycleState(AppLifecycleState.paused);
      wait(const Duration(minutes: 25)); // 45 minutes since the event, 25 in the background
      p.didChangeAppLifecycleState(AppLifecycleState.resumed);
      expect(p.sessionId, first);

      p.didChangeAppLifecycleState(AppLifecycleState.paused);
      wait(const Duration(minutes: 30));
      p.didChangeAppLifecycleState(AppLifecycleState.resumed);
      expect(p.sessionId, isNot(first));
      await p.flush();
      expect(names(), ['session_start', 'a', 'session_start']);
    });

    test('a restart within 30 minutes continues the session; a later one starts a new one', () async {
      var p = await start();
      await p.track('a');
      final first = p.sessionId;
      p.dispose();

      wait(const Duration(minutes: 10));
      p = await start();
      expect(p.sessionId, first);
      await p.track('b');
      expect(p.sessionId, first);
      p.dispose();

      wait(const Duration(minutes: 45));
      p = await start();
      await p.track('c');
      expect(p.sessionId, isNot(first));
      await p.flush();
      expect(names(), ['session_start', 'a', 'b', 'session_start', 'c']);
    });

    test('reset starts a new session with the next event', () async {
      final p = await start();
      await p.identify('u1');
      final first = p.sessionId;
      await p.reset();
      expect(p.sessionId, isNull);
      await p.track('x');
      await p.flush();
      expect(p.sessionId, isNot(first));
      expect(names(), ['session_start', 'identify', 'session_start', 'x']);
      expect(server.allEvents[2]['external_id'], p.currentId);
    });
  });
}
