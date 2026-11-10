part of 'pulse.dart';

/// Messages per page of `GET /v1/web/inbox`.
const _inboxPageSize = 50;

/// One message in the app inbox.
@immutable
class PulseInboxMessage {
  /// Creates a message (the SDK builds them from the server's answer).
  const PulseInboxMessage({
    required this.id,
    required this.messageId,
    required this.title,
    required this.body,
    required this.createdAt,
    required this.expiresAt,
    this.imageUrl,
    this.url,
    this.category,
    this.data = const {},
    this.readAt,
  });

  /// Reads one entry of the server's `messages` list.
  factory PulseInboxMessage.fromJson(Map<String, dynamic> j) {
    String? str(String k) {
      final v = j[k];
      return v is String && v.isNotEmpty ? v : null;
    }

    DateTime? time(String k) {
      final v = str(k);
      return v == null ? null : DateTime.tryParse(v);
    }

    final data = j['data'];
    return PulseInboxMessage(
      id: (j['id'] as num).toInt(),
      messageId: str('message_id') ?? '',
      title: str('title') ?? '',
      body: str('body') ?? '',
      imageUrl: str('image_url'),
      url: str('url'),
      category: str('category'),
      data: data is Map
          ? {for (final e in data.entries) '${e.key}': '${e.value}'}
          : const {},
      createdAt: time('created_at') ?? DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      readAt: time('read_at'),
      expiresAt: time('expires_at') ?? DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
    );
  }

  /// The inbox entry's id: pass it to `markRead`, `delete`, `click` and `list(before:)`.
  final int id;

  /// The id of the send this message came from.
  final String messageId;

  /// Title (may be empty).
  final String title;

  /// Text (may be empty).
  final String body;

  /// Image to show with the message.
  final String? imageUrl;

  /// Link or deep link to open when the message is tapped.
  final String? url;

  /// Category set by the sender (for tabs or filters in your UI).
  final String? category;

  /// Custom key-value data set by the sender.
  final Map<String, String> data;

  /// When the message arrived.
  final DateTime createdAt;

  /// When the message was read, or null while unread.
  final DateTime? readAt;

  /// When the server removes the message.
  final DateTime expiresAt;

  /// Whether the message has been read.
  bool get isRead => readAt != null;

  /// A copy marked read at [at] (default now).
  PulseInboxMessage asRead([DateTime? at]) => PulseInboxMessage(
        id: id,
        messageId: messageId,
        title: title,
        body: body,
        imageUrl: imageUrl,
        url: url,
        category: category,
        data: data,
        createdAt: createdAt,
        readAt: readAt ?? at ?? DateTime.now().toUtc(),
        expiresAt: expiresAt,
      );

  @override
  String toString() => 'PulseInboxMessage($id, $title)';
}

/// One page of the inbox, newest first.
@immutable
class PulseInboxPage {
  /// Creates a page.
  const PulseInboxPage({this.messages = const [], this.unread = 0});

  /// An empty page (also what `list` returns when the request failed).
  static const empty = PulseInboxPage();

  /// The messages, newest first (at most 50).
  final List<PulseInboxMessage> messages;

  /// Unread messages in the whole inbox.
  final int unread;

  /// Whether older messages may exist: call `list(before: messages.last.id)`.
  bool get hasMore => messages.length >= _inboxPageSize;
}

/// The current person's app inbox: `Pulse.instance.inbox`.
///
/// A signed-in user needs their user hash (`identify(userId, userHash: ...)`) even when the workspace
/// does not require identity verification for events, because inboxes are private: without it the
/// SDK throws [PulseException]. Anonymous people need none.
class PulseInbox {
  PulseInbox._(this._pulse);

  final Pulse _pulse;
  final _unread = ValueNotifier<int>(0);
  // Unread state of messages seen in list(), to keep the count right after delete.
  final _known = <int, bool>{};

  /// Unread messages, as of the last [list] (and lowered by [markRead] and [delete]).
  int get unreadCount => _unread.value;

  /// The unread count, for a badge:
  /// `ValueListenableBuilder(valueListenable: Pulse.instance.inbox.unread, builder: ...)`.
  ValueListenable<int> get unread => _unread;

  /// Returns the newest 50 messages, or the 50 before message id [before], and updates [unreadCount].
  ///
  /// Returns an empty page (and logs) on network errors, rate limiting (429) and server errors.
  /// Throws [PulseException] when the server refuses the user hash (403).
  Future<PulseInboxPage> list({int? before}) async {
    _checkHash();
    final query = <String, String>{
      'id': _pulse.currentId,
      if (_pulse._identifiedHash != null) 'user_hash': _pulse._identifiedHash!,
      if (before != null) 'before': '$before',
    };
    final http.Response res;
    try {
      res = await _pulse._get('/v1/web/inbox', query);
    } catch (e) {
      _pulse._log('inbox request failed: $e', always: true);
      return PulseInboxPage.empty;
    }
    if (!_ok(res, 'list')) return PulseInboxPage.empty;
    try {
      final j = (jsonDecode(res.body) as Map).cast<String, dynamic>();
      final messages = [
        for (final m in (j['messages'] as List? ?? const []))
          PulseInboxMessage.fromJson((m as Map).cast<String, dynamic>())
      ];
      final unread = (j['unread'] as num? ?? 0).toInt();
      if (before == null) _known.clear();
      for (final m in messages) {
        _known[m.id] = !m.isRead;
      }
      _unread.value = unread;
      return PulseInboxPage(messages: messages, unread: unread);
    } catch (e) {
      _pulse._log('inbox answer is unreadable: $e', always: true);
      return PulseInboxPage.empty;
    }
  }

  /// Marks messages [ids] read, or every message when [ids] is null. Returns how many changed
  /// (0 on failure; an empty list sends nothing). Reads count as opens in the sender's report.
  Future<int> markRead([List<int>? ids]) async {
    if (ids != null && ids.isEmpty) return 0;
    final changed = await _action('read', ids ?? const []);
    if (ids == null) {
      _known.updateAll((_, __) => false);
      _unread.value = 0;
    } else if (changed > 0) {
      for (final id in ids) {
        if (_known.containsKey(id)) _known[id] = false;
      }
      _unread.value = max(0, _unread.value - changed);
    }
    return changed;
  }

  /// Deletes messages [ids], or **every message** when [ids] is null. Returns how many were deleted
  /// (0 on failure; an empty list sends nothing).
  Future<int> delete([List<int>? ids]) async {
    if (ids != null && ids.isEmpty) return 0;
    final changed = await _action('delete', ids ?? const []);
    if (ids == null) {
      _known.clear();
      _unread.value = 0;
    } else if (changed > 0) {
      final unreadGone = ids.where((id) => _known.remove(id) ?? false).length;
      _unread.value = max(0, _unread.value - unreadGone);
    }
    return changed;
  }

  /// Records a tap on message [id] (a click in the sender's report). Open its `url` yourself.
  /// Returns whether the server recorded it.
  Future<bool> click(int id) async => await _action('click', [id]) > 0;

  Future<int> _action(String action, List<int> ids) async {
    _checkHash();
    final body = <String, dynamic>{
      'id': _pulse.currentId,
      if (_pulse._identifiedHash != null) 'user_hash': _pulse._identifiedHash,
      'action': action,
      'message_ids': ids,
    };
    final http.Response res;
    try {
      res = await _pulse._post('/v1/web/inbox', body);
    } catch (e) {
      _pulse._log('inbox $action failed: $e', always: true);
      return 0;
    }
    if (!_ok(res, action)) return 0;
    try {
      return ((jsonDecode(res.body) as Map)['changed'] as num? ?? 0).toInt();
    } catch (_) {
      return 0;
    }
  }

  void _checkHash() {
    if (_pulse.isIdentified && _pulse._identifiedHash == null) {
      throw const PulseException(
          'the inbox of a signed-in user needs their user hash: call '
          'identify(userId, userHash: hex(HMAC-SHA256(identity secret, userId))) with the hash '
          'computed on your server');
    }
  }

  bool _ok(http.Response res, String what) {
    final code = res.statusCode;
    if (code >= 200 && code < 300) return true;
    if (code == 403) {
      throw PulseException(
          'the server refused the inbox of ${_pulse.currentId}: a signed-in user needs a valid user '
          'hash (identify(userId, userHash: ...), computed on your server with the identity secret)',
          statusCode: 403);
    }
    _pulse._log(
        code == 429
            ? 'inbox $what rate limited (429); try again later'
            : 'inbox $what answered $code: ${res.body}',
        always: true);
    return false;
  }

  void _forget() {
    _known.clear();
    _unread.value = 0;
  }
}
