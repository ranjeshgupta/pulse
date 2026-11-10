import 'package:flutter/foundation.dart';

/// How long without events (or in the background) ends a session.
const sessionTimeout = Duration(minutes: 30);

const _maxLen = 100;

String? _clip(String? s) {
  final t = s?.trim();
  if (t == null || t.isEmpty) return null;
  return t.length > _maxLen ? t.substring(0, _maxLen) : t;
}

/// "17.4.1" → "17.4", "14.0" → "14".
String? _majorMinor(String? v) {
  final m = RegExp(r'^(\d+)(?:\.(\d+))?').firstMatch(v ?? '');
  if (m == null) return null;
  final minor = m.group(2);
  return minor != null && minor != '0' ? '${m.group(1)}.$minor' : m.group(1);
}

// Android release by the first letter of an AOSP build id (UP1A.231005.007 → 14) or of the
// Android part of a MIUI / HyperOS version (V14.0.6.0.TMRMIXM → 13).
const _androidLetters = {
  'Q': '10', 'R': '11', 'S': '12', 'T': '13', 'U': '14', 'V': '15', 'B': '16', 'C': '17',
};

/// Shortens `Platform.operatingSystemVersion` to the OS version people know ("14", "17.4").
///
/// iOS reports "Version 17.4.1 (Build 21E236)". On Android the string depends on the device and
/// Dart version (a release number, a build id like "UP1A.231005.007", or a kernel string); what
/// can't be read is returned trimmed (at most 100 characters). Null for an empty string.
String? cleanOsVersion(String raw, TargetPlatform platform) {
  final s = raw.trim();
  if (s.isEmpty) return null;
  if (platform == TargetPlatform.iOS || platform == TargetPlatform.macOS) {
    final m = RegExp(r'(?:^|Version\s+)(\d+(?:\.\d+)*)').firstMatch(s);
    if (m != null) return _majorMinor(m.group(1));
  } else if (platform == TargetPlatform.android) {
    final named = RegExp(r'Android\s+(\d+(?:\.\d+)?)', caseSensitive: false).firstMatch(s);
    if (named != null) return _majorMinor(named.group(1));
    final plain = RegExp(r'^(\d+(?:\.\d+){0,2})$').firstMatch(s);
    if (plain != null) return _majorMinor(plain.group(1));
    final build = RegExp(r'^([A-Z])[A-Z](\d)[A-Z]\.\d{6}').firstMatch(s);
    if (build != null) {
      final letter = build.group(1)!;
      // 2024 builds start with A: AP1A/AP2A are Android 14 QPRs, AP3A and later Android 15.
      if (letter == 'A') return int.parse(build.group(2)!) <= 2 ? '14' : '15';
      final v = _androidLetters[letter];
      if (v != null) return v;
    }
    final miui = RegExp(r'^(?:V|OS)\d+(?:\.\d+)+\.([A-Z])[A-Z]{5,6}$').firstMatch(s);
    if (miui != null) {
      final v = _androidLetters[miui.group(1)!];
      if (v != null) return v;
    }
  } else {
    final m = RegExp(r'\d+(?:\.\d+)*').firstMatch(s);
    if (m != null) return _majorMinor(m.group(0));
  }
  return _clip(s);
}

/// The device properties sent with identify, screen_view and session_start (unknown ones left
/// out, each at most 100 characters).
///
/// [shortestSide] is the shortest side of the app's first view in logical pixels: 600 or more is
/// a tablet. On iOS the brand is Apple, and without a [deviceModel] the model is "iPhone" or "iPad".
Map<String, String> buildDeviceProps({
  required TargetPlatform platform,
  String? osVersionRaw,
  String? osVersion,
  String? locale,
  double? shortestSide,
  String? appVersion,
  String? deviceModel,
  String? deviceBrand,
}) {
  final out = <String, String>{};
  void put(String k, String? v) {
    final c = _clip(v);
    if (c != null) out[k] = c;
  }

  final (String? platformName, String? os) = switch (platform) {
    TargetPlatform.android => ('android', 'Android'),
    TargetPlatform.iOS => ('ios', 'iOS'),
    TargetPlatform.macOS => (null, 'macOS'),
    TargetPlatform.windows => (null, 'Windows'),
    TargetPlatform.linux => (null, 'Linux'),
    TargetPlatform.fuchsia => (null, null),
  };
  String? type;
  if (shortestSide != null && shortestSide > 0) {
    type = shortestSide >= 600 ? 'tablet' : 'mobile';
  }
  var model = deviceModel;
  var brand = deviceBrand;
  if (platform == TargetPlatform.iOS) {
    brand ??= 'Apple';
    if (model == null && type != null) model = type == 'tablet' ? 'iPad' : 'iPhone';
  }
  put('platform', platformName);
  put('device_type', type);
  put('os', os);
  put('os_version', osVersion ?? (osVersionRaw == null ? null : cleanOsVersion(osVersionRaw, platform)));
  put('device_brand', brand);
  put('device_model', model);
  put('app_version', appVersion);
  put('locale', locale);
  return out;
}

/// Decides which session an event at [now] belongs to: the current one ([sid]) while [last]
/// activity was less than [timeout] ago, else a new one from [newId]. A clock that went backwards
/// keeps the session.
({String sid, bool started}) nextSession(
    DateTime now, DateTime? last, String? sid, String Function() newId,
    {Duration timeout = sessionTimeout}) {
  if (sid != null && sid.isNotEmpty && last != null && now.difference(last) < timeout) {
    return (sid: sid, started: false);
  }
  return (sid: newId(), started: true);
}
