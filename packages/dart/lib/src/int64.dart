/// A single signed 64-bit API with platform-specific, lossless storage.
export 'int64_native.dart'
    if (dart.library.js_interop) 'int64_web.dart'
    show LuxoInt64;
