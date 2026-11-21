/// Pulse mobile SDK for Flutter.
///
/// ```dart
/// await Pulse.init(PulseConfig(host: 'https://pulse.example.com', siteKey: 'sk_...'));
/// Pulse.instance.track('signed_up', {'plan': 'pro'});
/// ```
library;

export 'src/config.dart' show PulseConfig, PushPlatform;
export 'src/content_model.dart' show PulseSlotContent, PulseSlotCard;
export 'src/content_view.dart' show PulseSlot, PulseSlotBuilder;
export 'src/exception.dart' show PulseException;
export 'src/inapp_message.dart'
    show PulseInAppMessage, PulseInAppContent, PulseInAppButton, PulseInAppTrigger;
export 'src/inbox_view.dart' show PulseInboxView;
export 'src/pulse.dart'
    show Pulse, PulseInbox, PulseInboxMessage, PulseInboxPage, PulseInApp, PulseContent;
export 'src/storage.dart'
    show PulseStorage, InMemoryPulseStorage, SharedPreferencesPulseStorage;
