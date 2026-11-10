/// An error the app has to fix, such as a missing or wrong user hash for the inbox.
///
/// Network failures, rate limiting and server errors are not thrown: the SDK logs them and returns an
/// empty result.
class PulseException implements Exception {
  /// Creates an exception with [message] and the HTTP [statusCode] that caused it, if any.
  const PulseException(this.message, {this.statusCode});

  /// What went wrong and how to fix it.
  final String message;

  /// The server's HTTP status, or null when the SDK refused before sending.
  final int? statusCode;

  @override
  String toString() =>
      'PulseException${statusCode == null ? '' : ' ($statusCode)'}: $message';
}
