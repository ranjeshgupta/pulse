import 'package:flutter/material.dart';
import 'package:pulse_sdk/pulse_sdk.dart';

// Run with: flutter run --dart-define=PULSE_HOST=http://10.0.2.2:7080 --dart-define=PULSE_SITE_KEY=sk_...
const _host = String.fromEnvironment('PULSE_HOST', defaultValue: 'http://10.0.2.2:7080');
const _siteKey = String.fromEnvironment('PULSE_SITE_KEY', defaultValue: 'sk_replace_me');

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // flushOnBackground (default true) flushes the queue when the app is paused.
  await Pulse.init(PulseConfig(host: _host, siteKey: _siteKey, debug: true));
  runApp(const ExampleApp());
}

class ExampleApp extends StatelessWidget {
  const ExampleApp({super.key});

  @override
  Widget build(BuildContext context) => const MaterialApp(home: HomeScreen());
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
      appBar: AppBar(title: const Text('Pulse example')),
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
