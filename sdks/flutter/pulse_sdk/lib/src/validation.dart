import 'dart:convert';

final _eventName = RegExp(r'^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$');
final _attrKey = RegExp(r'^[a-zA-Z_][a-zA-Z0-9_]{0,63}$');

/// Event names the site-key API refuses: consent changes and events only a server may send.
const reservedEventNames = {
  'unsubscribed',
  'resubscribed',
  'tag_added',
  'tag_removed',
  'points_changed',
  'list_joined',
  'list_left',
  'contact_merged',
  'contact_merged_into',
};

/// Throws [ArgumentError] unless [name] is an event name the server accepts from an app.
void checkEventName(String name) {
  if (!_eventName.hasMatch(name)) {
    throw ArgumentError.value(name, 'name',
        'must start with a letter and have at most 64 letters, digits, _ . : or -');
  }
  if (reservedEventNames.contains(name)) {
    throw ArgumentError.value(
        name, 'name', 'is reserved and cannot be sent from an app');
  }
}

/// Throws [ArgumentError] unless [userId] can be used as a contact's external id.
void checkUserId(String userId) {
  if (userId.isEmpty) throw ArgumentError.value(userId, 'userId', 'is empty');
  if (utf8.encode(userId).length > 255) {
    throw ArgumentError.value(userId, 'userId', 'is longer than 255 bytes');
  }
  if (userId.startsWith('anon-')) {
    // The server treats anon- ids as anonymous visitors (no identity verification).
    throw ArgumentError.value(
        userId, 'userId', 'must not start with "anon-" (reserved for anonymous ids)');
  }
}

/// Throws [ArgumentError] if an attribute name is one the server would reject.
void checkAttributes(Map<String, dynamic>? attrs) {
  for (final k in attrs?.keys ?? const <String>[]) {
    if (!_attrKey.hasMatch(k)) {
      throw ArgumentError.value(k, 'attributes',
          'names must start with a letter or _ and have at most 64 letters, digits or _');
    }
  }
}

/// Converts [m] to plain JSON values (DateTime becomes ISO 8601, unknown objects their toString).
Map<String, dynamic> jsonSafe(Map<String, dynamic> m) =>
    (jsonDecode(jsonEncode(m, toEncodable: _encodable)) as Map)
        .cast<String, dynamic>();

Object? _encodable(Object? o) =>
    o is DateTime ? o.toUtc().toIso8601String() : o.toString();
