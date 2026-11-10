/// Push platform of a device token passed to `Pulse.registerPushToken`.
enum PushPlatform {
  /// An Android device (FCM registration token).
  android('android'),

  /// An iOS device. Pulse sends through FCM, so this is the FCM registration token of the iOS app.
  ios('ios');

  const PushPlatform(this.wireName);

  /// The value the server expects in `platform`.
  final String wireName;
}

/// Settings for `Pulse.init`.
class PulseConfig {
  /// Creates a configuration.
  ///
  /// [host] is the Pulse server's base URL (for example `https://pulse.example.com`), and [siteKey]
  /// the workspace's public site key (`sk_...`). The site key is public: never put an API key or the
  /// identity verification secret in an app.
  PulseConfig({
    required String host,
    required this.siteKey,
    this.flushInterval = const Duration(seconds: 10),
    this.maxBatch = 50,
    this.maxQueue = 1000,
    this.flushOnBackground = true,
    this.requestTimeout = const Duration(seconds: 30),
    this.debug = false,
    this.inAppMessages = true,
    this.onInAppAction,
    this.appVersion,
    this.deviceModel,
    this.deviceBrand,
    this.osVersion,
  }) : host = host.replaceAll(RegExp(r'/+$'), '') {
    if (this.host.isEmpty) throw ArgumentError.value(host, 'host', 'is empty');
    if (siteKey.isEmpty) throw ArgumentError.value(siteKey, 'siteKey', 'is empty');
    if (maxBatch < 1 || maxBatch > 50) {
      throw RangeError.range(maxBatch, 1, 50, 'maxBatch');
    }
    if (maxQueue < maxBatch) {
      throw RangeError.value(maxQueue, 'maxQueue', 'must be at least maxBatch');
    }
  }

  /// Server base URL without a trailing slash.
  final String host;

  /// Public site key, sent as `X-Pulse-Site-Key`.
  final String siteKey;

  /// How often queued events are sent.
  final Duration flushInterval;

  /// Events per request (1-50), and the queue length that triggers an immediate flush.
  final int maxBatch;

  /// Most events kept while offline; the oldest are dropped beyond this.
  final int maxQueue;

  /// Flush when the app is paused or detached (uses a `WidgetsBindingObserver`).
  final bool flushOnBackground;

  /// Timeout of one HTTP request.
  final Duration requestTimeout;

  /// Log what the SDK does with `debugPrint`.
  final bool debug;

  /// Fetch in-app campaigns (at init, after identify/reset, and on resume at most every 15 minutes)
  /// and show them when their trigger event happens. Showing needs `Pulse.attachNavigator`.
  final bool inAppMessages;

  /// Called with the button's `value` when someone taps an in-app button whose action is `url`
  /// (a web link or a deep link). The app opens it; the SDK only closes the message.
  final void Function(String url)? onInAppAction;

  /// The app's version, sent as `app_version` (e.g. `PackageInfo.version` from package_info_plus).
  final String? appVersion;

  /// The device model, sent as `device_model` (e.g. "SM-S918B", "Pixel 8", "iPhone15,2"; from
  /// device_info_plus). On iOS it defaults to "iPhone" or "iPad".
  final String? deviceModel;

  /// The device maker, sent as `device_brand` (e.g. "Samsung", "Google"; from device_info_plus).
  /// On iOS it defaults to "Apple".
  final String? deviceBrand;

  /// The OS version, sent as `os_version`, when you know it better than the SDK (e.g.
  /// `AndroidDeviceInfo.version.release`). By default it is read from `Platform.operatingSystemVersion`.
  final String? osVersion;
}
