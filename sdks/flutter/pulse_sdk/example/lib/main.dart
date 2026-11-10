import 'dart:async';

import 'package:flutter/material.dart';
import 'package:pulse_sdk/pulse_sdk.dart';

// Run with: flutter run --dart-define=PULSE_HOST=http://10.0.2.2:7080 --dart-define=PULSE_SITE_KEY=sk_...
const _host = String.fromEnvironment('PULSE_HOST', defaultValue: 'http://10.0.2.2:7080');
const _siteKey = String.fromEnvironment('PULSE_SITE_KEY', defaultValue: 'sk_replace_me');

final _navigatorKey = GlobalKey<NavigatorState>();
final _messengerKey = GlobalKey<ScaffoldMessengerState>();

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // flushOnBackground (default true) flushes the queue when the app is paused.
  await Pulse.init(PulseConfig(
    host: _host,
    siteKey: _siteKey,
    debug: true,
    // In-app "url" buttons: open the link or deep link (here: just show it).
    onInAppAction: (url) =>
        _messengerKey.currentState?.showSnackBar(SnackBar(content: Text('Open $url'))),
  ));
  Pulse.instance.attachNavigator(_navigatorKey); // in-app messages are shown with this navigator
  runApp(const ExampleApp());
  unawaited(Pulse.instance.track('app_open')); // a common in-app trigger
}

class ExampleApp extends StatelessWidget {
  const ExampleApp({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
        navigatorKey: _navigatorKey,
        scaffoldMessengerKey: _messengerKey,
        home: const HomeScreen(),
      );
}

class InboxScreen extends StatelessWidget {
  const InboxScreen({super.key});

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(
          title: const Text('Inbox'),
          actions: [
            IconButton(
              tooltip: 'Mark all read',
              icon: const Icon(Icons.done_all),
              onPressed: () => Pulse.instance.inbox.markRead(),
            ),
          ],
        ),
        body: PulseInboxView(
          onOpen: (m) {
            if (m.url != null) {
              ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Open ${m.url}')));
            }
          },
        ),
      );
}

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  @override
  void initState() {
    super.initState();
    Pulse.instance.screen('Home');
    _refreshBadge();
  }

  // Updates Pulse.instance.inbox.unread (the badge). A signed-in user needs a user hash.
  Future<void> _refreshBadge() async {
    try {
      await Pulse.instance.inbox.list();
    } on PulseException catch (e) {
      debugPrint('$e');
    }
  }

  Future<void> _signIn() async {
    // In a real app the user hash comes from your server:
    // hex(HMAC-SHA256(identity secret, user id)). Never compute it in the app.
    await Pulse.instance.identify('user-42',
        attributes: {'first_name': 'Ada', 'plan': 'pro'}, email: 'ada@example.com');
    setState(() {});
  }

  Future<void> _signOut() async {
    await Pulse.instance.reset();
    setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final pulse = Pulse.instance;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Pulse example'),
        actions: [
          ValueListenableBuilder<int>(
            valueListenable: pulse.inbox.unread,
            builder: (context, unread, _) => IconButton(
              tooltip: 'Inbox',
              icon: Badge(
                isLabelVisible: unread > 0,
                label: Text('$unread'),
                child: const Icon(Icons.inbox_outlined),
              ),
              onPressed: () => Navigator.of(context)
                  .push(MaterialPageRoute<void>(builder: (_) => const InboxScreen())),
            ),
          ),
        ],
      ),
      body: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('Current id: ${pulse.currentId}'),
            const SizedBox(height: 16),
            ElevatedButton(
              onPressed: () => pulse.track('button_clicked', {'button': 'demo'}),
              child: const Text('Track event'),
            ),
            ElevatedButton(
              onPressed: () => pulse.track('add_to_cart'),
              child: const Text('Track add_to_cart (in-app trigger)'),
            ),
            ElevatedButton(
              onPressed: pulse.isIdentified ? _signOut : _signIn,
              child: Text(pulse.isIdentified ? 'Sign out (reset)' : 'Sign in (identify)'),
            ),
            ElevatedButton(onPressed: pulse.flush, child: const Text('Flush now')),
          ],
        ),
      ),
    );
  }
}
