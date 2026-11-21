import 'package:flutter/foundation.dart';

String? _str(Map<String, dynamic> j, String k) {
  final v = j[k];
  return v is String && v.isNotEmpty ? v : null;
}

/// The card of an app content slot: what the default `PulseSlot` view draws.
@immutable
class PulseSlotCard {
  /// Creates a card.
  const PulseSlotCard({
    this.title,
    this.body,
    this.imageUrl,
    this.buttonText,
    this.deepLink,
    this.data = const {},
  });

  /// Reads a card from the server's JSON.
  factory PulseSlotCard.fromJson(Map<String, dynamic> j) {
    final d = j['data'];
    return PulseSlotCard(
      title: _str(j, 'title'),
      body: _str(j, 'body'),
      imageUrl: _str(j, 'image_url'),
      buttonText: _str(j, 'button_text'),
      deepLink: _str(j, 'deep_link'),
      data: d is Map ? Map.unmodifiable(d.cast<String, dynamic>()) : const {},
    );
  }

  /// Title.
  final String? title;

  /// Text.
  final String? body;

  /// Image shown above the text.
  final String? imageUrl;

  /// Label of the button (no button when null).
  final String? buttonText;

  /// Link or deep link opened (through `PulseConfig.onInAppAction`) when the card or button is tapped.
  final String? deepLink;

  /// Extra key/values set in Pulse, for a custom `PulseSlot.builder`.
  final Map<String, dynamic> data;

  /// Whether the card has anything the default view can draw.
  bool get isEmpty => title == null && body == null && imageUrl == null && buttonText == null;

  @override
  bool operator ==(Object other) =>
      other is PulseSlotCard &&
      other.title == title &&
      other.body == body &&
      other.imageUrl == imageUrl &&
      other.buttonText == buttonText &&
      other.deepLink == deepLink &&
      mapEquals(other.data, data);

  @override
  int get hashCode => Object.hash(title, body, imageUrl, buttonText, deepLink, data.length);

  @override
  String toString() => 'PulseSlotCard(${title ?? ''})';
}

/// What the server chose for the current person in one app content slot.
@immutable
class PulseSlotContent {
  /// Creates slot content.
  const PulseSlotContent({required this.slot, this.card, this.html, this.variant});

  /// Reads the entry of slot [slot] in the server's `content` map.
  factory PulseSlotContent.fromJson(String slot, Map<String, dynamic> j) {
    final c = j['card'];
    return PulseSlotContent(
      slot: slot,
      card: c is Map ? PulseSlotCard.fromJson(c.cast<String, dynamic>()) : null,
      html: _str(j, 'html'),
      variant: _str(j, 'variant'),
    );
  }

  /// The slot id (e.g. `home_banner`).
  final String slot;

  /// A card (title, body, image, button, deep link, data), if the content has one.
  final PulseSlotCard? card;

  /// HTML, if the content has some. The SDK does not render HTML: draw it with `PulseSlot.builder`.
  final String? html;

  /// Which variant was chosen (e.g. `vip`), sent with the `app_content_*` events.
  final String? variant;

  @override
  bool operator ==(Object other) =>
      other is PulseSlotContent &&
      other.slot == slot &&
      other.card == card &&
      other.html == html &&
      other.variant == variant;

  @override
  int get hashCode => Object.hash(slot, card, html, variant);

  @override
  String toString() => 'PulseSlotContent($slot, ${variant ?? '-'})';
}
