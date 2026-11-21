import 'unicode_native.dart'
    if (dart.library.js_interop) 'unicode_web.dart'
    as platform;

/// Reject before a platform UTF-8 encoder can silently replace a character.
String requireUnicode(String value) {
  if (!platform.hasCompleteUnicode(value)) {
    throw const FormatException(
      'String must contain complete Unicode characters',
    );
  }
  return value;
}
