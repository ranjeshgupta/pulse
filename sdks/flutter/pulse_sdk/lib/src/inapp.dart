part of 'pulse.dart';

const _kInApp = 'pulse.inapp';

/// How often campaigns are fetched again when the app comes back to the foreground.
const _inAppRefreshAfter = Duration(minutes: 15);

/// In-app campaigns for the current person: `Pulse.instance.inApp`.
///
/// The SDK fetches the campaigns at init, after `identify` and `reset`, and when the app resumes
/// (at most every 15 minutes). When the app calls `track(name)` or `screen(name)`, the first campaign
/// whose trigger matches and whose frequency allows is shown after its delay, with the navigator
/// given to `Pulse.attachNavigator`. Showing, tapping a button and dismissing are recorded as
/// `inapp_shown`, `inapp_clicked` and `inapp_dismissed`.
class PulseInApp {
  PulseInApp._(this._pulse);

  final Pulse _pulse;
  List<PulseInAppMessage> _campaigns = const [];
  DateTime? _fetchedAt;
  Future<void>? _fetching;
  int _generation = 0;

  final _sessionShown = <int>{};
  final _pending = <int>{};
  final _timers = <Timer>{};
  Set<int> _once = {};
  Map<int, String> _daily = {};
  GlobalKey<NavigatorState>? _navigator;
  bool _showing = false;

  /// The campaigns fetched last, in priority order.
  List<PulseInAppMessage> get campaigns => List.unmodifiable(_campaigns);

  /// Whether an in-app message is on screen.
  bool get isShowing => _showing;

  /// Fetches the current person's campaigns now. Never throws; on failure the previous ones are kept.
  Future<void> refresh() {
    final gen = ++_generation;
    final f = _fetch(gen);
    _fetching = f;
    return f.whenComplete(() {
      if (identical(_fetching, f)) _fetching = null;
    });
  }

  Future<void> _fetch(int gen) async {
    final body = <String, dynamic>{
      'id': _pulse.currentId,
      if (_pulse._identifiedHash != null) 'user_hash': _pulse._identifiedHash,
      'platform': defaultTargetPlatform == TargetPlatform.iOS ? 'ios' : 'android',
    };
    try {
      final res = await _pulse._post('/v1/web/inapp', body);
      if (res.statusCode < 200 || res.statusCode >= 300) {
        _pulse._log('in-app campaigns: server answered ${res.statusCode}: ${res.body}',
            always: true);
        return;
      }
      final list = (jsonDecode(res.body) as Map)['messages'] as List? ?? const [];
      final campaigns = <PulseInAppMessage>[];
      for (final m in list) {
        try {
          campaigns.add(PulseInAppMessage.fromJson((m as Map).cast<String, dynamic>()));
        } catch (e) {
          _pulse._log('skipping an unreadable in-app campaign: $e', always: true);
        }
      }
      if (gen != _generation || _pulse._disposed) return; // a newer fetch (e.g. after identify) wins
      _campaigns = campaigns;
      _fetchedAt = DateTime.now();
      _pulse._log('${campaigns.length} in-app campaigns for ${_pulse.currentId}');
    } catch (e) {
      _pulse._log('fetching in-app campaigns failed: $e', always: true);
    }
  }

  void _refreshIfStale() {
    final at = _fetchedAt;
    if (_fetching == null &&
        (at == null || DateTime.now().difference(at) >= _inAppRefreshAfter)) {
      unawaited(refresh());
    }
  }

  Future<void> _load() async {
    final raw = await _pulse._storage.read(_kInApp);
    if (raw == null) return;
    try {
      final j = (jsonDecode(raw) as Map).cast<String, dynamic>();
      _once = {for (final id in (j['once'] as List? ?? const [])) (id as num).toInt()};
      _daily = {
        for (final e in ((j['daily'] as Map?) ?? const {}).entries)
          int.parse('${e.key}'): '${e.value}'
      };
    } catch (_) {
      _pulse._log('stored in-app state is unreadable; starting empty', always: true);
    }
  }

  Future<void> _save() => _pulse._storage
      .write(
          _kInApp,
          jsonEncode({
            'once': _once.toList(),
            'daily': {for (final e in _daily.entries) '${e.key}': e.value},
          }))
      .catchError((Object e) => _pulse._log('could not save in-app state: $e', always: true));

  static String _today() {
    final d = DateTime.now();
    String two(int n) => n.toString().padLeft(2, '0');
    return '${d.year}-${two(d.month)}-${two(d.day)}';
  }

  bool _allows(PulseInAppMessage m) {
    if (_pending.contains(m.id)) return false;
    final end = m.endAt;
    if (end != null && DateTime.now().isAfter(end)) return false;
    switch (m.trigger.frequency) {
      case 'always':
        return true;
      case 'session':
        return !_sessionShown.contains(m.id);
      case 'daily':
        return _daily[m.id] != _today();
      default: // once (and anything unknown)
        return !_once.contains(m.id) && !_sessionShown.contains(m.id);
    }
  }

  // Called for every tracked event and screen.
  void _onEvent(String name, String? screen) {
    if (_pulse._disposed) return;
    final fetching = _fetching;
    if (fetching != null && _fetchedAt == null) {
      // The first fetch is still running (e.g. app_open right after init): decide once it's done.
      unawaited(fetching.then((_) => _match(name, screen)));
    } else {
      _match(name, screen);
    }
  }

  void _match(String name, String? screen) {
    if (_pulse._disposed || _showing) return;
    for (final m in _campaigns) {
      if (!m.trigger.matches(name, screen) || !_allows(m)) continue;
      _pending.add(m.id);
      late final Timer t;
      t = Timer(Duration(seconds: max(0, m.trigger.delaySeconds)), () {
        _timers.remove(t);
        _pending.remove(m.id);
        unawaited(_show(m));
      });
      _timers.add(t);
      return;
    }
  }

  Future<void> _show(PulseInAppMessage m) async {
    if (_pulse._disposed || _showing) return;
    final nav = _navigator?.currentState;
    if (nav == null || !nav.mounted) {
      _pulse._log('in-app ${m.id} not shown: call Pulse.instance.attachNavigator(navigatorKey)',
          always: true);
      return;
    }
    _showing = true;
    _sessionShown.add(m.id);
    if (m.trigger.frequency == 'daily') {
      _daily[m.id] = _today();
      unawaited(_save());
    } else if (m.trigger.frequency != 'session' && m.trigger.frequency != 'always') {
      _once.add(m.id);
      unawaited(_save());
    }
    unawaited(_pulse._enqueue('inapp_shown', props: {'inapp_id': m.id}));
    try {
      final outcome = await presentInApp(nav, m);
      final b = outcome.button;
      if (b != null && b.isUrl) {
        unawaited(_pulse._enqueue('inapp_clicked',
            props: {'inapp_id': m.id, 'button': b.text, 'url': b.value}));
        try {
          _pulse.config.onInAppAction?.call(b.value!);
        } catch (e) {
          _pulse._log('onInAppAction threw: $e', always: true);
        }
      } else if (!outcome.expired) {
        unawaited(_pulse._enqueue('inapp_dismissed', props: {'inapp_id': m.id}));
      }
    } catch (e) {
      _pulse._log('could not show in-app ${m.id}: $e', always: true);
    } finally {
      _showing = false;
    }
  }

  void _dispose() {
    for (final t in _timers) {
      t.cancel();
    }
    _timers.clear();
    _pending.clear();
  }
}
