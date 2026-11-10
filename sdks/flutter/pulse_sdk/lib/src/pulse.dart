import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/widgets.dart';
import 'package:http/http.dart' as http;

import 'config.dart';
import 'storage.dart';
import 'validation.dart';

// Storage keys, named like the web SDK's localStorage keys.
const _kAnonId = 'pulse.aid';
const _kUserId = 'pulse.uid';
const _kUserHash = 'pulse.uh';
const _kQueue = 'pulse.queue';
const _kPush = 'pulse.push';

const _maxBackoff = Duration(minutes: 5);

enum _Outcome { sent, dropped, retry }

/// The Pulse SDK. Call [Pulse.init] once at startup, then use [Pulse.instance].
///
/// People are anonymous (`anon-` + 24 hex characters) until [identify]. Events are queued,
/// persisted, and sent in batches every [PulseConfig.flushInterval], as soon as
/// [PulseConfig.maxBatch] events are waiting, and whenever [flush] is called.
class Pulse with WidgetsBindingObserver {
  Pulse._(this.config, this._http, this._storage, this._ownsHttp);

  static Pulse? _instance;

  /// The instance [init] created.
  static Pulse get instance {
    final p = _instance;
    if (p == null) {
      throw StateError('Pulse.init must be called before Pulse.instance');
    }
    return p;
  }

  /// Whether [init] has completed.
  static bool get isInitialized => _instance != null;

  /// Starts the SDK: loads the stored ids and queued events and starts the flush timer.
  ///
  /// [httpClient] and [storage] are for tests and special setups; by default the SDK uses its own
  /// `http.Client` and `shared_preferences`. Calling init again replaces the previous instance.
  static Future<Pulse> init(PulseConfig config,
      {http.Client? httpClient, PulseStorage? storage}) async {
    if (config.flushOnBackground || storage == null) {
      WidgetsFlutterBinding.ensureInitialized();
    }
    final p = Pulse._(
      config,
      httpClient ?? http.Client(),
      storage ?? await SharedPreferencesPulseStorage.open(),
      httpClient == null,
    );
    await p._load();
    _instance?.dispose();
    _instance = p;
    return p;
  }

  /// The configuration this instance was started with.
  final PulseConfig config;
  final http.Client _http;
  final PulseStorage _storage;
  final bool _ownsHttp;
  final _random = Random.secure();

  late String _anonId;
  String? _userId;
  String? _userHash;
  ({String token, PushPlatform platform})? _push;
  List<Map<String, dynamic>> _queue = [];

  Timer? _timer;
  Future<void>? _flushing;
  Future<void> _writes = Future.value();
  int _failures = 0;
  DateTime? _retryAt;
  bool _disposed = false;
  bool _warnedFull = false;

  /// The id events are recorded under: the identified user, else this install's anonymous id.
  String get currentId => _userId ?? _anonId;

  /// Whether a user is identified (since the last [reset]).
  bool get isIdentified => _userId != null;

  /// Number of events waiting to be sent.
  int get pendingEvents => _queue.length;

  Future<void> _load() async {
    var aid = await _storage.read(_kAnonId);
    if (aid == null || !aid.startsWith('anon-')) {
      aid = _newAnonId();
      await _storage.write(_kAnonId, aid);
    }
    _anonId = aid;
    _userId = await _storage.read(_kUserId);
    _userHash = _userId == null ? null : await _storage.read(_kUserHash);
    _queue = _decodeQueue(await _storage.read(_kQueue));
    final push = await _storage.read(_kPush);
    if (push != null) {
      try {
        final m = (jsonDecode(push) as Map).cast<String, dynamic>();
        final platform = PushPlatform.values.byName(m['platform'] as String);
        _push = (token: m['token'] as String, platform: platform);
      } catch (_) {
        await _storage.delete(_kPush);
      }
    }
    _timer = Timer.periodic(config.flushInterval, (_) => _flushIfDue());
    if (config.flushOnBackground) WidgetsBinding.instance.addObserver(this);
    _log('ready as $currentId with ${_queue.length} queued events');
  }

  List<Map<String, dynamic>> _decodeQueue(String? raw) {
    if (raw == null) return [];
    try {
      return (jsonDecode(raw) as List)
          .map((e) => (e as Map).cast<String, dynamic>())
          .toList();
    } catch (_) {
      _log('stored queue is unreadable; starting empty', always: true);
      return [];
    }
  }

  String _randomHex(int bytes) => List.generate(
          bytes, (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0'))
      .join();

  String _newAnonId() => 'anon-${_randomHex(12)}';

  /// Records event [name] with optional [props] for the current person.
  ///
  /// Throws [ArgumentError] when the name is invalid (it must match `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`)
  /// or reserved. The returned future completes once the event is queued and persisted; it does not
  /// wait for the upload.
  Future<void> track(String name, [Map<String, dynamic>? props]) {
    checkEventName(name);
    return _enqueue(name, props: props);
  }

  /// Records a `screen_view` event with `props.screen` = [name].
  Future<void> screen(String name, [Map<String, dynamic>? props]) {
    if (name.isEmpty) throw ArgumentError.value(name, 'name', 'is empty');
    return _enqueue('screen_view', props: {...?props, 'screen': name});
  }

  /// Ties this install to your user id [userId] and updates their profile.
  ///
  /// When the person was anonymous, the event carries `props.anonymous_id` so the server merges the
  /// anonymous visitor (and their history) into the user. With identity verification on, pass
  /// [userHash]: hex(HMAC-SHA256(identity secret, userId)) computed on your server.
  ///
  /// [attributes] are merged into the profile (`optout_*`, `tags`, `points` and `merged_into` are
  /// ignored by the server). Throws [ArgumentError] for an invalid user id or attribute name.
  Future<void> identify(String userId,
      {Map<String, dynamic>? attributes, String? email, String? userHash}) {
    checkUserId(userId);
    checkAttributes(attributes);
    return _identify(userId, attributes, email, userHash);
  }

  Future<void> _identify(String userId, Map<String, dynamic>? attributes,
      String? email, String? userHash) async {
    final previous = currentId;
    final wasAnonymous = _userId == null;
    _userId = userId;
    _userHash = (userHash == null || userHash.isEmpty) ? null : userHash;
    await _storage.write(_kUserId, userId);
    if (_userHash != null) {
      await _storage.write(_kUserHash, _userHash!);
    } else {
      await _storage.delete(_kUserHash);
    }
    await _enqueue('identify',
        props: wasAnonymous ? {'anonymous_id': previous} : null,
        attrs: attributes,
        email: email,
        flushWhenFull: false);
    unawaited(flush());
    if (_push != null && previous != userId) unawaited(_registerStoredToken());
  }

  /// Forgets the identified user (call it on sign-out): a new anonymous id is used from now on.
  /// Events already queued keep the id they were recorded under.
  Future<void> reset() async {
    _userId = null;
    _userHash = null;
    _anonId = _newAnonId();
    await _storage.delete(_kUserId);
    await _storage.delete(_kUserHash);
    await _storage.write(_kAnonId, _anonId);
    // Move this device's push token off the signed-out user.
    if (_push != null) unawaited(_registerStoredToken());
  }

  /// Registers a push [token] (the FCM registration token, obtained by the
  /// app) for the current person. The token is remembered and registered again after [identify]
  /// and [reset]. Returns whether the server accepted it.
  Future<bool> registerPushToken(String token,
      {required PushPlatform platform}) {
    if (token.isEmpty) throw ArgumentError.value(token, 'token', 'is empty');
    return _savePushToken(token, platform);
  }

  Future<bool> _savePushToken(String token, PushPlatform platform) async {
    _push = (token: token, platform: platform);
    await _storage.write(
        _kPush, jsonEncode({'token': token, 'platform': platform.name}));
    return _registerStoredToken();
  }

  Future<bool> _registerStoredToken() async {
    final push = _push;
    if (push == null) return false;
    final body = <String, dynamic>{
      'external_id': currentId,
      'token': push.token,
      'platform': push.platform.wireName,
      if (_userId != null && _userHash != null) 'user_hash': _userHash,
    };
    try {
      final res = await _post('/v1/web/devices', body);
      if (res.statusCode >= 200 && res.statusCode < 300) {
        _log('registered ${push.platform.wireName} push token for $currentId');
        return true;
      }
      _log('push token rejected: ${res.statusCode} ${res.body}', always: true);
    } catch (e) {
      _log('push token registration failed: $e', always: true);
    }
    return false;
  }

  Future<void> _enqueue(String name,
      {Map<String, dynamic>? props,
      Map<String, dynamic>? attrs,
      String? email,
      bool flushWhenFull = true}) {
    final p = <String, dynamic>{...?props};
    if (_userId != null && _userHash != null) p['user_hash'] = _userHash;
    final ev = <String, dynamic>{
      'id': _randomHex(16),
      'external_id': currentId,
      'name': name,
      'ts': DateTime.now().toUtc().toIso8601String(),
      if (p.isNotEmpty) 'props': jsonSafe(p),
      if (attrs != null && attrs.isNotEmpty) 'attrs': jsonSafe(attrs),
      if (email != null && email.isNotEmpty) 'email': email,
    };
    _queue.add(ev);
    if (_queue.length > config.maxQueue) {
      final n = _queue.length - config.maxQueue;
      _queue.removeRange(0, n);
      _log('queue full (${config.maxQueue}): dropping the oldest events',
          always: !_warnedFull);
      _warnedFull = true;
    }
    _log('queued $name for ${ev['external_id']}');
    final saved = _persist();
    if (flushWhenFull && _queue.length >= config.maxBatch && !_backingOff) {
      unawaited(flush());
    }
    return saved;
  }

  // Writes are chained so they reach storage in order.
  Future<void> _persist() {
    final snapshot = jsonEncode(_queue);
    return _writes = _writes
        .then((_) => _storage.write(_kQueue, snapshot))
        .catchError((Object e) => _log('could not save the queue: $e', always: true));
  }

  bool get _backingOff =>
      _retryAt != null && DateTime.now().isBefore(_retryAt!);

  void _flushIfDue() {
    if (_queue.isNotEmpty && !_backingOff) unawaited(flush());
  }

  /// Sends every queued event now. Call it when the app goes to the background
  /// (`AppLifecycleState.paused`); with [PulseConfig.flushOnBackground] the SDK does that itself.
  ///
  /// Events the server could not take for now (429, 5xx, network errors) stay queued and are retried
  /// with backoff; events it rejected (400, 403) are dropped. The future never throws.
  Future<void> flush() => _flushing ??= _drain().whenComplete(() => _flushing = null);

  Future<void> _drain() async {
    while (_queue.isNotEmpty && !_disposed) {
      final batch = _queue.take(config.maxBatch).toList();
      final outcome = await _send(batch);
      if (outcome == _Outcome.retry) {
        _failures++;
        final secs = min(pow(2, _failures).toInt(), _maxBackoff.inSeconds);
        final delay = Duration(milliseconds: secs * 1000 + _random.nextInt(1000));
        _retryAt = DateTime.now().add(delay);
        _log('will retry ${_queue.length} events in ${delay.inSeconds}s');
        break;
      }
      _failures = 0;
      _retryAt = null;
      _warnedFull = false;
      final ids = batch.map((e) => e['id']).toSet();
      _queue.removeWhere((e) => ids.contains(e['id']));
      await _persist();
    }
  }

  Future<_Outcome> _send(List<Map<String, dynamic>> batch) async {
    final http.Response res;
    try {
      res = await _post('/v1/web/events', {'events': batch});
    } catch (e) {
      _log('sending ${batch.length} events failed: $e');
      return _Outcome.retry;
    }
    final code = res.statusCode;
    if (code >= 200 && code < 300) {
      _log('sent ${batch.length} events');
      return _Outcome.sent;
    }
    if (code == 400 || code == 403) {
      _log('server rejected ${batch.length} events ($code): ${res.body}; dropping them',
          always: true);
      return _Outcome.dropped;
    }
    // 429, 5xx, and anything unexpected (e.g. 401 while a site key is being rotated): keep.
    _log('server answered $code for ${batch.length} events: ${res.body}',
        always: code == 401);
    return _Outcome.retry;
  }

  Future<http.Response> _post(String path, Map<String, dynamic> body) => _http
      .post(
        Uri.parse('${config.host}$path'),
        headers: {
          'Content-Type': 'application/json',
          'X-Pulse-Site-Key': config.siteKey,
        },
        body: jsonEncode(body),
      )
      .timeout(config.requestTimeout);

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      unawaited(flush());
    }
  }

  /// Stops the flush timer and lifecycle observer. Queued events stay in storage for the next start.
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _timer?.cancel();
    if (config.flushOnBackground) WidgetsBinding.instance.removeObserver(this);
    if (_ownsHttp) _http.close();
    if (identical(_instance, this)) _instance = null;
  }

  void _log(String msg, {bool always = false}) {
    if (config.debug || always) debugPrint('[pulse] $msg');
  }
}
