import 'dart:async';

import 'package:flutter/material.dart';

import 'content_model.dart';
import 'pulse.dart';

/// Draws the content of a slot in a custom way (see [PulseSlot.builder]).
typedef PulseSlotBuilder = Widget Function(BuildContext context, PulseSlotContent content);

/// Shows the content Pulse chose for the current person in app content slot [id].
///
/// ```dart
/// const PulseSlot(id: 'home_banner')
/// ```
///
/// The slot is fetched with `Pulse.instance.content.load` (the slots of every `PulseSlot` built in
/// the same frame go in one request) and redrawn when its content changes, e.g. after `identify`.
/// By default a Material card is drawn from the content's card (image, title, body, button); with
/// [builder] the app draws the content itself, which is the only way to show its `html` or `data`.
/// [placeholder] (default: nothing) is shown while loading, when the slot is empty, and before
/// `Pulse.init`.
///
/// When content is first shown, `app_content_viewed` `{slot, variant}` is recorded. Tapping the
/// default card (when it has a deep link) or its button records `app_content_clicked`
/// `{slot, variant, link}` and opens the deep link through `PulseConfig.onInAppAction`.
class PulseSlot extends StatefulWidget {
  /// Creates a slot view.
  const PulseSlot({super.key, required this.id, this.builder, this.placeholder, this.padding});

  /// The slot id, as created in Pulse (e.g. `home_banner`).
  final String id;

  /// Draws the content instead of the default card. Record taps with
  /// `Pulse.instance.content.recordClick(content)`.
  final PulseSlotBuilder? builder;

  /// Shown while loading and when there is nothing to show (default: `SizedBox.shrink()`).
  final Widget? placeholder;

  /// Space around the content (not around the placeholder).
  final EdgeInsetsGeometry? padding;

  @override
  State<PulseSlot> createState() => _PulseSlotState();
}

class _PulseSlotState extends State<PulseSlot> {
  Pulse? _pulse;
  PulseSlotContent? _content;
  PulseSlotContent? _viewed;

  @override
  void initState() {
    super.initState();
    _attach();
  }

  void _attach() {
    if (!Pulse.isInitialized) return;
    final p = _pulse = Pulse.instance;
    p.content.addListener(_changed);
    _content = p.content.cached(widget.id);
    unawaited(p.content.load(widget.id).then((_) => _changed()));
  }

  void _detach() {
    _pulse?.content.removeListener(_changed);
    _pulse = null;
  }

  void _changed() {
    if (!mounted) return;
    final c = _pulse?.content.cached(widget.id);
    if (c != _content) setState(() => _content = c);
  }

  @override
  void didUpdateWidget(PulseSlot old) {
    super.didUpdateWidget(old);
    if (old.id != widget.id || (_pulse == null && Pulse.isInitialized)) {
      _detach();
      _content = null;
      _viewed = null;
      _attach();
    }
  }

  @override
  void dispose() {
    _detach();
    super.dispose();
  }

  void _tap(PulseSlotContent c, {String? link}) =>
      unawaited(_pulse?.content.recordClick(c, link: link));

  @override
  Widget build(BuildContext context) {
    final c = _content;
    final placeholder = widget.placeholder ?? const SizedBox.shrink();
    if (c == null) return placeholder;
    final Widget child;
    final builder = widget.builder;
    if (builder != null) {
      child = builder(context, c);
    } else {
      final card = c.card;
      if (card == null || card.isEmpty) return placeholder; // e.g. html-only: needs a builder
      child = _SlotCard(card, onTap: (link) => _tap(c, link: link));
    }
    if (_viewed != c) {
      _viewed = c;
      final p = _pulse;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (p != null && mounted) unawaited(p.content.recordView(c));
      });
    }
    final padding = widget.padding;
    return padding == null ? child : Padding(padding: padding, child: child);
  }
}

class _SlotCard extends StatelessWidget {
  const _SlotCard(this.card, {required this.onTap});

  final PulseSlotCard card;
  final void Function(String? link) onTap;

  @override
  Widget build(BuildContext context) {
    final t = Theme.of(context).textTheme;
    final link = card.deepLink;
    final title = card.title, body = card.body, image = card.imageUrl, button = card.buttonText;
    return Card(
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: link == null ? null : () => onTap(link),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            if (image != null)
              Image.network(image,
                  height: 160,
                  width: double.infinity,
                  fit: BoxFit.cover,
                  errorBuilder: (_, __, ___) => const SizedBox.shrink()),
            if (title != null || body != null || button != null)
              Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (title != null)
                      Text(title, style: t.titleMedium?.copyWith(fontWeight: FontWeight.w600)),
                    if (title != null && body != null) const SizedBox(height: 6),
                    if (body != null) Text(body, style: t.bodyMedium),
                    if (button != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 12),
                        child: Align(
                          alignment: Alignment.centerRight,
                          child: FilledButton(onPressed: () => onTap(link), child: Text(button)),
                        ),
                      ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}
