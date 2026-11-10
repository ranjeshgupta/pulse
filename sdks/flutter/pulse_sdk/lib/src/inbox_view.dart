import 'dart:async';

import 'package:flutter/material.dart';

import 'exception.dart';
import 'pulse.dart';

/// A ready-made inbox list: unread dot, image, title, body and time; pull to refresh, swipe to
/// delete, and older messages loaded at the end of the list.
///
/// Tapping a message marks it read, records a click and calls [onOpen] (open `message.url` there).
/// Colours and text styles come from the app's `Theme`; pass [itemBuilder] to draw rows yourself.
class PulseInboxView extends StatefulWidget {
  /// Creates the view for `Pulse.instance.inbox`.
  const PulseInboxView({
    super.key,
    this.onOpen,
    this.itemBuilder,
    this.empty,
    this.unreadColor,
    this.padding,
  });

  /// Called after a message is tapped (and marked read).
  final void Function(PulseInboxMessage message)? onOpen;

  /// Draws one row instead of the default `ListTile`.
  final Widget Function(BuildContext context, PulseInboxMessage message)? itemBuilder;

  /// Shown when the inbox is empty (default: "No messages").
  final Widget? empty;

  /// Colour of the unread dot (default: the theme's primary colour).
  final Color? unreadColor;

  /// Padding of the list.
  final EdgeInsetsGeometry? padding;

  @override
  State<PulseInboxView> createState() => _PulseInboxViewState();
}

class _PulseInboxViewState extends State<PulseInboxView> {
  final _messages = <PulseInboxMessage>[];
  bool _loading = true;
  bool _hasMore = false;
  bool _loadingMore = false;
  String? _error;

  PulseInbox get _inbox => Pulse.instance.inbox;

  @override
  void initState() {
    super.initState();
    unawaited(_reload());
  }

  Future<void> _reload() async {
    try {
      final page = await _inbox.list();
      if (!mounted) return;
      setState(() {
        _messages
          ..clear()
          ..addAll(page.messages);
        _hasMore = page.hasMore;
        _error = null;
        _loading = false;
      });
    } on PulseException catch (e) {
      if (mounted) {
        setState(() {
          _error = e.message;
          _loading = false;
        });
      }
    }
  }

  Future<void> _more() async {
    if (_loadingMore || !_hasMore || _messages.isEmpty) return;
    _loadingMore = true;
    try {
      final page = await _inbox.list(before: _messages.last.id);
      if (!mounted) return;
      setState(() {
        _messages.addAll(page.messages);
        _hasMore = page.hasMore;
      });
    } on PulseException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      _loadingMore = false;
    }
  }

  Future<void> _open(int index) async {
    final m = _messages[index];
    if (!m.isRead) setState(() => _messages[index] = m.asRead());
    widget.onOpen?.call(m);
    try {
      if (!m.isRead) await _inbox.markRead([m.id]);
      await _inbox.click(m.id);
    } on PulseException catch (e) {
      debugPrint('[pulse] $e');
    }
  }

  Future<void> _delete(PulseInboxMessage m) async {
    setState(() => _messages.removeWhere((x) => x.id == m.id));
    try {
      await _inbox.delete([m.id]);
    } on PulseException catch (e) {
      debugPrint('[pulse] $e');
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    final theme = Theme.of(context);
    final Widget body;
    if (_error != null && _messages.isEmpty) {
      body = _centered(Text(_error!, textAlign: TextAlign.center, style: theme.textTheme.bodyMedium));
    } else if (_messages.isEmpty) {
      body = _centered(widget.empty ??
          Text('No messages',
              style: theme.textTheme.bodyLarge?.copyWith(color: theme.hintColor)));
    } else {
      body = ListView.separated(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: widget.padding,
        itemCount: _messages.length + (_hasMore ? 1 : 0),
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (context, i) {
          if (i == _messages.length) {
            unawaited(_more());
            return const Padding(
              padding: EdgeInsets.all(16),
              child: Center(child: CircularProgressIndicator()),
            );
          }
          final m = _messages[i];
          return Dismissible(
            key: ValueKey(m.id),
            direction: DismissDirection.endToStart,
            background: Container(
              color: theme.colorScheme.error,
              alignment: Alignment.centerRight,
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: Icon(Icons.delete_outline, color: theme.colorScheme.onError),
            ),
            onDismissed: (_) => _delete(m),
            child: InkWell(
              onTap: () => _open(i),
              child: widget.itemBuilder?.call(context, m) ?? _tile(context, m),
            ),
          );
        },
      );
    }
    return RefreshIndicator(onRefresh: _reload, child: body);
  }

  Widget _centered(Widget child) => LayoutBuilder(
        builder: (context, c) => SingleChildScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          child: SizedBox(
            height: c.maxHeight,
            child: Center(
                child: Padding(padding: const EdgeInsets.all(24), child: child)),
          ),
        ),
      );

  Widget _tile(BuildContext context, PulseInboxMessage m) {
    final theme = Theme.of(context);
    final unread = !m.isRead;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 12, 16, 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 8,
            height: 8,
            margin: const EdgeInsets.only(top: 6, right: 8),
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: unread
                  ? (widget.unreadColor ?? theme.colorScheme.primary)
                  : Colors.transparent,
            ),
          ),
          if (m.imageUrl != null)
            Padding(
              padding: const EdgeInsets.only(right: 12),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(8),
                child: Image.network(m.imageUrl!,
                    width: 56,
                    height: 56,
                    fit: BoxFit.cover,
                    errorBuilder: (_, __, ___) => const SizedBox(width: 56, height: 56)),
              ),
            ),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(m.title,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.titleSmall?.copyWith(
                              fontWeight: unread ? FontWeight.w700 : FontWeight.w500)),
                    ),
                    const SizedBox(width: 8),
                    Text(_ago(m.createdAt),
                        style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor)),
                  ],
                ),
                if (m.body.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(m.body,
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.bodyMedium),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

String _ago(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inMinutes < 1) return 'now';
  if (d.inHours < 1) return '${d.inMinutes}m';
  if (d.inDays < 1) return '${d.inHours}h';
  if (d.inDays < 7) return '${d.inDays}d';
  final l = t.toLocal();
  return '${l.year}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')}';
}
