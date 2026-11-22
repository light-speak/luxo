import 'dart:convert';
import 'dart:ffi';
import 'dart:io';

/// Publish a finished executable with the no-replace operation of the filesystem.
void publishBuildArtifact(File artifact, File destination) {
  if (!Platform.isWindows && !Platform.isMacOS && !Platform.isLinux) {
    throw UnsupportedError('Atomic executable publication is unavailable');
  }
  final from = _nativePath(artifact.absolute.path);
  try {
    final to = _nativePath(destination.absolute.path);
    try {
      final error = Platform.isWindows
          ? _moveWindows(from.cast(), to.cast())
          : _movePosix(from, to);
      if (error == null) return;

      final exists = Platform.isWindows
          ? error == _windowsFileExists || error == _windowsAlreadyExists
          : error == _fileExists;
      if (exists) {
        throw StateError(
          'Output appeared during compilation: ${destination.path}',
        );
      }
      throw FileSystemException(
        'Could not publish the compiled executable',
        destination.path,
        OSError('Atomic executable publication failed', error),
      );
    } finally {
      _free(to);
    }
  } finally {
    _free(from);
  }
}

String _windowsPath(String path) {
  final normalized = path.replaceAll('/', r'\');
  const extended = r'\\?\';
  if (normalized.length < 260 ||
      normalized.startsWith(extended) ||
      normalized.startsWith(r'\\.\')) {
    return normalized;
  }
  if (normalized.startsWith(r'\\')) {
    return '$extended${r'UNC\'}${normalized.substring(2)}';
  }
  return '$extended$normalized';
}

Pointer<Uint8> _nativePath(String path) {
  if (path.contains('\x00')) throw ArgumentError('Path contains a NUL');
  final wide = Platform.isWindows;
  final units = wide ? _windowsPath(path).codeUnits : utf8.encode(path);
  final length = units.length + 1;
  final memory = _malloc(wide ? 2 * length : length);
  if (memory == nullptr) throw StateError('Cannot allocate native path');
  final buffer = wide
      ? memory.cast<Uint16>().asTypedList(length)
      : memory.asTypedList(length);
  buffer.setAll(0, units);
  buffer[units.length] = 0;
  return memory;
}

const _fileExists = 17;
const _windowsFileExists = 80;
const _windowsAlreadyExists = 183;
const _currentDirectory = -100; // Linux AT_FDCWD
const _linuxNoReplace = 1; // RENAME_NOREPLACE
const _darwinExclusive = 4; // RENAME_EXCL
const _invalidArgument = 22;
const _linuxNotImplemented = 38;
const _linuxNotSupported = 95;
const _darwinNotSupported = 45;

int? _movePosix(Pointer<Uint8> from, Pointer<Uint8> to) {
  final errno = _errnoLocation();
  if (Platform.isMacOS) {
    if (_renameExclusive(from, to, _darwinExclusive) == 0) return null;
    if (errno.value != _darwinNotSupported) return errno.value;
  } else {
    final rename = _renameNoReplace;
    if (rename != null) {
      final result = rename(
        _currentDirectory,
        from,
        _currentDirectory,
        to,
        _linuxNoReplace,
      );
      if (result == 0) return null;
      final error = errno.value;
      if (error != _linuxNotImplemented &&
          error != _linuxNotSupported &&
          error != _invalidArgument) {
        return error;
      }
    }
  }
  // Some libcs, kernels and filesystems lack no-replace rename.
  // Linking creates the name atomically. The snapshot still owns the original.
  return _link(from, to) == 0 ? null : errno.value;
}

int? _moveWindows(Pointer<Uint16> from, Pointer<Uint16> to) {
  final lastError = _windowsLastError;
  // Leave both MOVEFILE_REPLACE_EXISTING and MOVEFILE_COPY_ALLOWED clear.
  return _moveFile(from, to, 0) == 0 ? lastError() : null;
}

typedef _LinuxRenameNative = Int32 Function(
  Int32,
  Pointer<Uint8>,
  Int32,
  Pointer<Uint8>,
  Uint32,
);
typedef _LinuxRename = int Function(
  int,
  Pointer<Uint8>,
  int,
  Pointer<Uint8>,
  int,
);

late final _libc = Platform.isWindows
    ? DynamicLibrary.open('msvcrt.dll')
    : DynamicLibrary.process();
late final _malloc = _libc
    .lookupFunction<
      Pointer<Uint8> Function(Size),
      Pointer<Uint8> Function(int)
    >('malloc');
late final _free = _libc
    .lookupFunction<
      Void Function(Pointer<Uint8>),
      void Function(Pointer<Uint8>)
    >('free');
late final _errnoLocation = _libc
    .lookupFunction<Pointer<Int32> Function(), Pointer<Int32> Function()>(
      Platform.isMacOS ? '__error' : '__errno_location',
    );
late final _renameExclusive = _libc
    .lookupFunction<
      Int32 Function(Pointer<Uint8>, Pointer<Uint8>, Uint32),
      int Function(Pointer<Uint8>, Pointer<Uint8>, int)
    >('renamex_np');
late final _renameNoReplace = _libc.providesSymbol('renameat2')
    ? _libc.lookupFunction<_LinuxRenameNative, _LinuxRename>('renameat2')
    : null;
late final _link = _libc
    .lookupFunction<
      Int32 Function(Pointer<Uint8>, Pointer<Uint8>),
      int Function(Pointer<Uint8>, Pointer<Uint8>)
    >('link');
late final _kernel = DynamicLibrary.open('kernel32.dll');
late final _moveFile = _kernel
    .lookupFunction<
      Int32 Function(Pointer<Uint16>, Pointer<Uint16>, Uint32),
      int Function(Pointer<Uint16>, Pointer<Uint16>, int)
    >('MoveFileExW');
late final _windowsLastError = _kernel
    .lookupFunction<Uint32 Function(), int Function()>('GetLastError');
