## 1.0.0-beta.1 (candidate)

- Align the first candidate with the Luxo core and companion SDKs; not yet released.
- Preserve exact Int64/Duration, nanosecond DateTime, and raw generic JSON numbers.
- Enforce finite Float, valid Unicode, explicit selection, and checked schema IDs.
- Add explicit unknown enum values and isolate stream decode failures and terminals.
- Require Dart 3.13+, including validated Native, JavaScript, and Wasm paths.

## 0.1.2

- Complete README with Luxo origin story, architecture diagram, comparison table
- Chinese documentation links

## 0.1.1

- Improved README with full documentation
- Updated package description

## 0.1.0

- Initial release
- HTTP + WebSocket transport with JSON and binary modes
- Binary codec (varint, svarint, fixed64, string, bool, bytes)
- Compile-time field tracking via `package:analyzer`
- Typed client code generation from schema introspection
- 401 auto-refresh with token callback
- WebSocket auto-reconnect with exponential backoff
- Request timeout support
