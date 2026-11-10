import 'package:shared_preferences/shared_preferences.dart';

/// Small key-value store the SDK keeps its ids and event queue in.
abstract class PulseStorage {
  /// Returns the value stored under [key], or null.
  Future<String?> read(String key);

  /// Stores [value] under [key].
  Future<void> write(String key, String value);

  /// Removes [key].
  Future<void> delete(String key);
}

/// The default storage, backed by `shared_preferences`.
class SharedPreferencesPulseStorage implements PulseStorage {
  SharedPreferencesPulseStorage._(this._prefs);

  final SharedPreferences _prefs;

  /// Opens the app's shared preferences.
  static Future<SharedPreferencesPulseStorage> open() async =>
      SharedPreferencesPulseStorage._(await SharedPreferences.getInstance());

  @override
  Future<String?> read(String key) async => _prefs.getString(key);

  @override
  Future<void> write(String key, String value) => _prefs.setString(key, value);

  @override
  Future<void> delete(String key) => _prefs.remove(key);
}

/// Storage that lives only in memory (tests, or apps that must not persist anything).
class InMemoryPulseStorage implements PulseStorage {
  /// Creates an empty store, or one holding [initial].
  InMemoryPulseStorage([Map<String, String>? initial]) : values = {...?initial};

  /// The stored values.
  final Map<String, String> values;

  @override
  Future<String?> read(String key) async => values[key];

  @override
  Future<void> write(String key, String value) async => values[key] = value;

  @override
  Future<void> delete(String key) async => values.remove(key);
}
