part of 'pulse.dart';

/// Slot ids the server accepts: lowercase letters, digits, `-` and `_`, starting with a letter or
/// digit, at most 64 characters.
final _slotId = RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$');

/// How old cached slot content may get before it is fetched again (on resume and by `load`).
const _contentRefreshAfter = Duration(minutes: 15);

/// Most slots in one request.
const _maxSlotsPerRequest = 50;

/// App content slots for the current person: `Pulse.instance.content`.
///
/// A slot is a named place in the app (e.g. `home_banner`) whose content is chosen in Pulse per
/// person. [fetch] asks the server for several slots at once and caches each one; [cached] reads
/// the cache. After `identify` and `reset` the cache is cleared and every slot asked for so far is
/// fetched again, and when the app resumes, slots older than 15 minutes are fetched again. Listeners
/// are notified whenever cached content changes (`PulseSlot` widgets use this).
class PulseContent extends ChangeNotifier {
  PulseContent._(this._pulse);

  final Pulse _pulse;
  final _cache = <String, PulseSlotContent>{};
  final _fetchedAt = <String, DateTime>{};
  final _known = <String>{};
  final _inFlight = <String, Future<void>>{};
  final _queued = <String>{};
  Completer<void>? _batch;
  int _generation = 0;

  /// Every slot asked for so far (they are fetched again after identify, reset and on resume).
  Set<String> get slots => Set.unmodifiable(_known);

  /// The cached content of [slot], or null (not fetched yet, or nothing to show).
  PulseSlotContent? cached(String slot) => _cache[slot];

  /// Whether [slot] has been fetched for the current person (its content may still be null).
  bool isFetched(String slot) => _fetchedAt.containsKey(slot);

  /// Fetches [slots] now in one request (several when more than 50) and caches each one.
  ///
  /// Returns the content of the slots that have some; a slot the server returned nothing for is
  /// removed from the cache. Never throws for server or network errors: the previously cached
  /// content is kept and returned. Throws [ArgumentError] for an empty list or an invalid slot id.
  Future<Map<String, PulseSlotContent>> fetch(List<String> slots) {
    final ids = slots.toSet().toList();
    if (ids.isEmpty) throw ArgumentError.value(slots, 'slots', 'is empty');
    ids.forEach(_checkSlot);
    _known.addAll(ids);
    final gen = _generation;
    final chunks = [
      for (var i = 0; i < ids.length; i += _maxSlotsPerRequest)
        ids.sublist(i, min(i + _maxSlotsPerRequest, ids.length))
    ];
    return Future.wait(chunks.map((c) => _fetchChunk(c, gen))).then((_) => {
          for (final s in ids)
            if (_cache[s] != null) s: _cache[s]!
        });
  }

  /// The content of [slot], fetched when it is not cached yet or older than 15 minutes.
  ///
  /// Calls made in the same microtask (e.g. by several `PulseSlot` widgets building one screen) are
  /// sent as one request. Never throws for server or network errors. Throws [ArgumentError] for an
  /// invalid slot id.
  Future<PulseSlotContent?> load(String slot) {
    _checkSlot(slot);
    final at = _fetchedAt[slot];
    if (at != null && DateTime.now().difference(at) < _contentRefreshAfter) {
      return Future.value(_cache[slot]);
    }
    final running = _inFlight[slot];
    if (running != null) return running.then((_) => _cache[slot]);
    _queued.add(slot);
    var batch = _batch;
    if (batch == null) {
      batch = _batch = Completer<void>();
      scheduleMicrotask(() {
        final b = _batch!;
        _batch = null;
        final q = _queued.toList();
        _queued.clear();
        fetch(q).then((_) => b.complete(), onError: (Object e) {
          _pulse._log('fetching app content failed: $e', always: true);
          b.complete();
        });
      });
    }
    return batch.future.then((_) => _cache[slot]);
  }

  /// Records `app_content_viewed` `{slot, variant}` for [content]. `PulseSlot` does this itself.
  Future<void> recordView(PulseSlotContent content) => _pulse.track('app_content_viewed', {
        'slot': content.slot,
        if (content.variant != null) 'variant': content.variant,
      });

  /// Records `app_content_clicked` `{slot, variant, link}` for [content], then opens [link] (by
  /// default the card's deep link) through `PulseConfig.onInAppAction`, like an in-app button.
  /// `PulseSlot`'s default view does this itself; call it from a custom builder.
  Future<void> recordClick(PulseSlotContent content, {String? link}) {
    final url = link ?? content.card?.deepLink;
    final saved = _pulse.track('app_content_clicked', {
      'slot': content.slot,
      if (content.variant != null) 'variant': content.variant,
      if (url != null) 'link': url,
    });
    if (url != null) {
      try {
        _pulse.config.onInAppAction?.call(url);
      } catch (e) {
        _pulse._log('onInAppAction threw: $e', always: true);
      }
    }
    return saved;
  }

  static void _checkSlot(String slot) {
    if (!_slotId.hasMatch(slot)) {
      throw ArgumentError.value(slot, 'slot',
          'must be 1-64 lowercase letters, digits, - or _, starting with a letter or digit');
    }
  }

  Future<void> _fetchChunk(List<String> chunk, int gen) {
    final f = _request(chunk, gen);
    for (final s in chunk) {
      _inFlight[s] = f;
    }
    return f.whenComplete(() {
      for (final s in chunk) {
        if (identical(_inFlight[s], f)) _inFlight.remove(s);
      }
    });
  }

  Future<void> _request(List<String> chunk, int gen) async {
    final body = <String, dynamic>{
      'id': _pulse.currentId,
      if (_pulse._identifiedHash != null) 'user_hash': _pulse._identifiedHash,
      'platform': defaultTargetPlatform == TargetPlatform.iOS ? 'ios' : 'android',
      'slots': chunk,
    };
    try {
      final res = await _pulse._post('/v1/web/content', body);
      if (res.statusCode < 200 || res.statusCode >= 300) {
        _pulse._log('app content: server answered ${res.statusCode}: ${res.body}', always: true);
        return;
      }
      final content = ((jsonDecode(res.body) as Map)['content'] as Map?) ?? const {};
      final parsed = <String, PulseSlotContent>{};
      for (final s in chunk) {
        final c = content[s];
        if (c is! Map) continue;
        try {
          parsed[s] = PulseSlotContent.fromJson(s, c.cast<String, dynamic>());
        } catch (e) {
          _pulse._log('skipping unreadable content of slot $s: $e', always: true);
        }
      }
      if (gen != _generation || _pulse._disposed) return; // identify/reset happened meanwhile
      final now = DateTime.now();
      var changed = false;
      for (final s in chunk) {
        final c = parsed[s];
        if (c != _cache[s]) changed = true;
        if (c == null) {
          _cache.remove(s);
        } else {
          _cache[s] = c;
        }
        _fetchedAt[s] = now;
      }
      _pulse._log('app content for ${_pulse.currentId}: ${parsed.keys.join(', ')}');
      if (changed) notifyListeners();
    } catch (e) {
      _pulse._log('fetching app content failed: $e', always: true);
    }
  }

  // After identify and reset: the cached content belonged to someone else.
  void _onIdentityChange() {
    _generation++;
    _inFlight.clear();
    _fetchedAt.clear();
    final had = _cache.isNotEmpty;
    _cache.clear();
    if (had) notifyListeners();
    if (_known.isNotEmpty) unawaited(fetch(_known.toList()));
  }

  void _refreshIfStale() {
    final now = DateTime.now();
    final stale = [
      for (final s in _known)
        if (!_inFlight.containsKey(s) &&
            (_fetchedAt[s] == null || now.difference(_fetchedAt[s]!) >= _contentRefreshAfter))
          s
    ];
    if (stale.isNotEmpty) unawaited(fetch(stale));
  }

  void _dispose() {
    _generation++;
    _inFlight.clear();
  }
}
