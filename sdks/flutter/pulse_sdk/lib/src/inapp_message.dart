import 'package:flutter/foundation.dart';

String? _str(Map<String, dynamic> j, String k) {
  final v = j[k];
  return v is String && v.isNotEmpty ? v : null;
}

Map<String, dynamic> _map(Object? v) =>
    v is Map ? v.cast<String, dynamic>() : const <String, dynamic>{};

/// A button of an in-app message.
@immutable
class PulseInAppButton {
  /// Creates a button.
  const PulseInAppButton({required this.text, required this.action, this.value});

  /// Reads a button from the server's JSON.
  factory PulseInAppButton.fromJson(Map<String, dynamic> j) => PulseInAppButton(
        text: _str(j, 'text') ?? '',
        action: _str(j, 'action') ?? 'dismiss',
        value: _str(j, 'value'),
      );

  /// Label.
  final String text;

  /// `url` (open [value], a link or deep link, through `PulseConfig.onInAppAction`) or `dismiss`.
  final String action;

  /// Where a `url` button goes.
  final String? value;

  /// Whether tapping the button opens a link.
  bool get isUrl => action == 'url' && value != null;
}

/// What an in-app message shows.
@immutable
class PulseInAppContent {
  /// Creates content.
  const PulseInAppContent({
    this.title = '',
    this.body = '',
    this.imageUrl,
    this.buttons = const [],
    this.background,
    this.textColor,
    this.buttonColor,
    this.position,
  });

  /// Reads content from the server's JSON.
  factory PulseInAppContent.fromJson(Map<String, dynamic> j) => PulseInAppContent(
        title: _str(j, 'title') ?? '',
        body: _str(j, 'body') ?? '',
        imageUrl: _str(j, 'image_url'),
        buttons: [
          for (final b in (j['buttons'] as List? ?? const []))
            PulseInAppButton.fromJson(_map(b))
        ],
        background: _str(j, 'background'),
        textColor: _str(j, 'text_color'),
        buttonColor: _str(j, 'button_color'),
        position: _str(j, 'position'),
      );

  /// Title (may be empty).
  final String title;

  /// Text (may be empty).
  final String body;

  /// Image shown above the text.
  final String? imageUrl;

  /// Up to two buttons.
  final List<PulseInAppButton> buttons;

  /// Background colour, `#rrggbb`.
  final String? background;

  /// Text colour, `#rrggbb`.
  final String? textColor;

  /// Button colour, `#rrggbb`.
  final String? buttonColor;

  /// Banner position: `top` or `bottom` (default).
  final String? position;
}

/// When an in-app message is shown.
@immutable
class PulseInAppTrigger {
  /// Creates a trigger.
  const PulseInAppTrigger({
    required this.event,
    this.screen,
    this.delaySeconds = 0,
    this.frequency = 'once',
    this.platforms = const [],
  });

  /// Reads a trigger from the server's JSON.
  factory PulseInAppTrigger.fromJson(Map<String, dynamic> j) => PulseInAppTrigger(
        event: _str(j, 'trigger_event') ?? '',
        screen: _str(j, 'screen'),
        delaySeconds: (j['delay_seconds'] as num? ?? 0).toInt(),
        frequency: _str(j, 'frequency') ?? 'once',
        platforms: [for (final p in (j['platforms'] as List? ?? const [])) '$p'],
      );

  /// The event that shows the message (e.g. `app_open`, `screen_view`, `add_to_cart`).
  final String event;

  /// With `screen_view`: only this screen.
  final String? screen;

  /// Wait this long after the event.
  final int delaySeconds;

  /// `once` (ever, on this device), `session` (once per app run), `daily` or `always`.
  final String frequency;

  /// `android` and/or `ios` (empty: both). The server already filters by platform.
  final List<String> platforms;

  /// Whether event [name] (with [screen] for `screen_view`) triggers this.
  bool matches(String name, String? screen) =>
      event == name && (this.screen == null || this.screen == screen);
}

/// An in-app campaign the server returned for the current person.
@immutable
class PulseInAppMessage {
  /// Creates a campaign.
  const PulseInAppMessage({
    required this.id,
    required this.layout,
    required this.content,
    required this.trigger,
    this.endAt,
  });

  /// Reads one entry of the server's `messages` list.
  factory PulseInAppMessage.fromJson(Map<String, dynamic> j) => PulseInAppMessage(
        id: (j['id'] as num).toInt(),
        layout: _str(j, 'layout') ?? 'modal',
        content: PulseInAppContent.fromJson(_map(j['content'])),
        trigger: PulseInAppTrigger.fromJson(_map(j['trigger'])),
        endAt: DateTime.tryParse(_str(j, 'end_at') ?? ''),
      );

  /// Campaign id (`inapp_id` in the recorded events).
  final int id;

  /// `modal`, `banner` or `fullscreen`.
  final String layout;

  /// What it shows.
  final PulseInAppContent content;

  /// When it shows.
  final PulseInAppTrigger trigger;

  /// When the campaign ends, if set.
  final DateTime? endAt;

  @override
  String toString() => 'PulseInAppMessage($id, $layout, ${trigger.event})';
}
