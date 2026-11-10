/// Pulse mobile SDK for Flutter.
///
/// ```dart
/// await Pulse.init(PulseConfig(host: 'https://pulse.example.com', siteKey: 'sk_...'));
/// Pulse.instance.track('signed_up', {'plan': 'pro'});
/// ```
library;

export 'src/config.dart' show PulseConfig, PushPlatform;
export 'src/pulse.dart' show Pulse;
export 'src/storage.dart'
    show PulseStorage, InMemoryPulseStorage, SharedPreferencesPulseStorage;
