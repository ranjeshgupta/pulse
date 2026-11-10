import 'dart:async';

import 'package:flutter/material.dart';

import 'inapp_message.dart';

/// How a shown in-app message ended.
class InAppOutcome {
  /// The person tapped [button] (null: closed it some other way). [expired]: a banner hid itself.
  const InAppOutcome({this.button, this.expired = false});

  /// The tapped button, or null.
  final PulseInAppButton? button;

  /// Whether a banner timed out without being touched.
  final bool expired;
}

/// Parses `#rrggbb` (null for anything else).
Color? parseHexColor(String? s) {
  if (s == null || !RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(s)) return null;
  return Color(0xFF000000 | int.parse(s.substring(1), radix: 16));
}

/// Shows [m] with [navigator] and completes when it is closed.
Future<InAppOutcome> presentInApp(NavigatorState navigator, PulseInAppMessage m,
    {Duration bannerDuration = const Duration(seconds: 8)}) async {
  switch (m.layout) {
    case 'banner':
      return _showBanner(navigator, m, bannerDuration);
    case 'fullscreen':
      final b = await navigator.push<PulseInAppButton>(PageRouteBuilder(
        fullscreenDialog: true,
        settings: RouteSettings(name: 'pulse_inapp_${m.id}'),
        pageBuilder: (_, __, ___) => _FullscreenView(m),
        transitionsBuilder: (_, a, __, child) => FadeTransition(opacity: a, child: child),
      ));
      return InAppOutcome(button: b);
    default: // modal
      final b = await navigator.push<PulseInAppButton>(RawDialogRoute(
        barrierDismissible: true,
        barrierLabel: 'Dismiss',
        barrierColor: const Color(0x8A000000),
        settings: RouteSettings(name: 'pulse_inapp_${m.id}'),
        pageBuilder: (_, __, ___) => _ModalView(m),
        transitionBuilder: (_, a, __, child) => FadeTransition(opacity: a, child: child),
      ));
      return InAppOutcome(button: b);
  }
}

Future<InAppOutcome> _showBanner(NavigatorState navigator, PulseInAppMessage m, Duration duration) {
  final overlay = navigator.overlay;
  if (overlay == null) return Future.value(const InAppOutcome());
  final done = Completer<InAppOutcome>();
  late final OverlayEntry entry;
  Timer? timer;
  void finish(InAppOutcome o) {
    if (done.isCompleted) return;
    timer?.cancel();
    entry.remove();
    done.complete(o);
  }

  entry = OverlayEntry(
      builder: (_) => _BannerView(m, onDone: (b) => finish(InAppOutcome(button: b))));
  overlay.insert(entry);
  timer = Timer(duration, () => finish(const InAppOutcome(expired: true)));
  return done.future;
}

class _Colors {
  _Colors(BuildContext context, PulseInAppContent c) {
    final scheme = Theme.of(context).colorScheme;
    background = parseHexColor(c.background) ?? scheme.surface;
    text = parseHexColor(c.textColor) ?? scheme.onSurface;
    button = parseHexColor(c.buttonColor) ?? scheme.primary;
    onButton = ThemeData.estimateBrightnessForColor(button) == Brightness.dark
        ? Colors.white
        : Colors.black;
  }

  late final Color background, text, button, onButton;
}

Widget _image(String? url, {double? height, BoxFit fit = BoxFit.cover}) => url == null
    ? const SizedBox.shrink()
    : Image.network(url,
        height: height,
        width: double.infinity,
        fit: fit,
        errorBuilder: (_, __, ___) => const SizedBox.shrink());

List<Widget> _texts(PulseInAppContent c, _Colors col, TextTheme t, {bool center = false}) => [
      if (c.title.isNotEmpty)
        Text(c.title,
            textAlign: center ? TextAlign.center : TextAlign.start,
            style: t.titleLarge?.copyWith(color: col.text, fontWeight: FontWeight.w600)),
      if (c.title.isNotEmpty && c.body.isNotEmpty) const SizedBox(height: 8),
      if (c.body.isNotEmpty)
        Text(c.body,
            textAlign: center ? TextAlign.center : TextAlign.start,
            style: t.bodyMedium?.copyWith(color: col.text)),
    ];

Widget _buttons(BuildContext context, PulseInAppContent c, _Colors col,
    void Function(PulseInAppButton) onTap) {
  if (c.buttons.isEmpty) return const SizedBox.shrink();
  return Padding(
    padding: const EdgeInsets.only(top: 16),
    child: Wrap(
      alignment: WrapAlignment.end,
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final (i, b) in c.buttons.indexed)
          i == 0 || b.isUrl
              ? FilledButton(
                  style: FilledButton.styleFrom(
                      backgroundColor: col.button, foregroundColor: col.onButton),
                  onPressed: () => onTap(b),
                  child: Text(b.text))
              : TextButton(
                  style: TextButton.styleFrom(foregroundColor: col.text),
                  onPressed: () => onTap(b),
                  child: Text(b.text)),
      ],
    ),
  );
}

Widget _close(_Colors col, VoidCallback onTap) => IconButton(
      tooltip: 'Close',
      icon: Icon(Icons.close, color: col.text),
      onPressed: onTap,
    );

class _ModalView extends StatelessWidget {
  const _ModalView(this.m);

  final PulseInAppMessage m;

  @override
  Widget build(BuildContext context) {
    final c = m.content;
    final col = _Colors(context, c);
    final nav = Navigator.of(context);
    return SafeArea(
      child: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: Material(
              color: col.background,
              elevation: 24,
              clipBehavior: Clip.antiAlias,
              borderRadius: BorderRadius.circular(16),
              child: Stack(
                children: [
                  SingleChildScrollView(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        _image(c.imageUrl, height: 180),
                        Padding(
                          padding: const EdgeInsets.fromLTRB(24, 24, 24, 16),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              ..._texts(c, col, Theme.of(context).textTheme),
                              _buttons(context, c, col, (b) => nav.pop(b)),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                  Positioned(top: 0, right: 0, child: _close(col, () => nav.pop())),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _FullscreenView extends StatelessWidget {
  const _FullscreenView(this.m);

  final PulseInAppMessage m;

  @override
  Widget build(BuildContext context) {
    final c = m.content;
    final col = _Colors(context, c);
    final nav = Navigator.of(context);
    return Material(
      color: col.background,
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Align(alignment: Alignment.topRight, child: _close(col, () => nav.pop())),
            Expanded(
              child: SingleChildScrollView(
                padding: const EdgeInsets.symmetric(horizontal: 24),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    if (c.imageUrl != null)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 24),
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(12),
                          child: _image(c.imageUrl, height: 280),
                        ),
                      ),
                    ..._texts(c, col, Theme.of(context).textTheme, center: true),
                  ],
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
              child: _buttons(context, c, col, (b) => nav.pop(b)),
            ),
          ],
        ),
      ),
    );
  }
}

class _BannerView extends StatelessWidget {
  const _BannerView(this.m, {required this.onDone});

  final PulseInAppMessage m;
  final void Function(PulseInAppButton?) onDone;

  @override
  Widget build(BuildContext context) {
    final c = m.content;
    final col = _Colors(context, c);
    final top = c.position == 'top';
    final link = c.buttons.where((b) => b.isUrl).firstOrNull;
    return Positioned(
      left: 0,
      right: 0,
      top: top ? 0 : null,
      bottom: top ? null : 0,
      child: SafeArea(
        top: top,
        bottom: !top,
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Material(
            color: col.background,
            elevation: 8,
            clipBehavior: Clip.antiAlias,
            borderRadius: BorderRadius.circular(12),
            child: InkWell(
              onTap: link == null ? null : () => onDone(link),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 12, 4, 12),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (c.imageUrl != null)
                      Padding(
                        padding: const EdgeInsets.only(right: 12),
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(8),
                          child: SizedBox(width: 48, height: 48, child: _image(c.imageUrl)),
                        ),
                      ),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          ..._texts(c, col, Theme.of(context).textTheme),
                          _buttons(context, c, col, onDone),
                        ],
                      ),
                    ),
                    _close(col, () => onDone(null)),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
