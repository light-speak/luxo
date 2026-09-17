import 'dart:js_interop';

extension type _StringMethods(JSString value) implements JSString {
  external bool isWellFormed();
}

/// Use the engine's Unicode check; never repair text or serialize to validate it.
bool hasCompleteUnicode(String value) =>
    _StringMethods(value.toJS).isWellFormed();
