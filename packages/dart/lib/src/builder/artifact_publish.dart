import 'dart:convert';
import 'dart:ffi';
import 'dart:io';

import 'native_uri.dart';

/// A destination whose directory stays open until publication or close.
class PreparedOutput {
  final File destination;
  final String _name;
  _OutputDirectory? _directory;
  bool _published = false;

  PreparedOutput._(this.destination, this._name, this._directory);

  static Future<PreparedOutput> prepare(
    Directory project,
    String output,
  ) async {
    final relative = _projectRelativeUri(output);
    final root = nativeFileUri(project.absolute.uri.normalizePath());
    final resolved = root.resolveUri(relative);
    final destination = File.fromUri(resolved);
    if (await FileSystemEntity.type(destination.path, followLinks: false) !=
        FileSystemEntityType.notFound) {
      throw StateError('Output already exists: $output');
    }
    final rootCount = root.pathSegments.where((p) => p.isNotEmpty).length;
    final parts = resolved.pathSegments.skip(rootCount).toList();
    if (parts.isEmpty || parts.last.isEmpty) {
      throw ArgumentError('Expected an output file: $output');
    }
    final name = parts.removeLast();
    final parent = _prepareDirectory(project.absolute.path, parts);
    try {
      if (parent.containsEntry(name)) {
        throw StateError('Output already exists: $output');
      }
      return PreparedOutput._(destination, name, parent);
    } catch (_) {
      parent.close();
      rethrow;
    }
  }

  void _publish(File artifact) {
    final directory = _directory;
    if (directory == null) throw StateError('Prepared output is closed');
    if (_published) throw StateError('Prepared output is already published');
    final error = directory.publish(artifact, _name);
    if (error != null) {
      if (_isExists(error)) {
        throw StateError(
          'Output appeared during compilation: ${destination.path}',
        );
      }
      throw FileSystemException(
        'Could not publish the compiled executable',
        destination.path,
        OSError('Atomic executable publication failed', error),
      );
    }
    _published = true;
  }

  void close() {
    final directory = _directory;
    _directory = null;
    directory?.close();
  }
}

/// Publish only through the directory acquired before compilation.
void publishBuildArtifact(File artifact, PreparedOutput destination) =>
    destination._publish(artifact);

void validateProjectRelativePath(String path) {
  _projectRelativeUri(path);
}

Uri _projectRelativeUri(String path) {
  final uri = nativeFileUri(Uri.parse(path));
  if (uri.hasScheme ||
      uri.hasAuthority ||
      uri.hasQuery ||
      uri.hasFragment ||
      path.startsWith('/') ||
      path.contains('\\') ||
      uri.pathSegments.any((p) => p == '..') ||
      path.isEmpty) {
    throw ArgumentError('Expected a project-relative path: $path');
  }
  return uri;
}

const _renameNoReplaceFlag = 1;
const _darwinExclusiveFlag = 4;
const _directoryAccess =
    0x001000a0; // SYNCHRONIZE | FILE_TRAVERSE | FILE_READ_ATTRIBUTES
const _entryAccess = 0x00100080; // SYNCHRONIZE | FILE_READ_ATTRIBUTES
const _synchronousDeleteAndReadAttributes =
    0x00110080; // SYNCHRONIZE | DELETE | FILE_READ_ATTRIBUTES
const _shareAll = 7;
const _openExisting = 3;
const _backupSemantics = 0x02000000;
const _openReparsePoint = 0x00200000;
const _fileOpen = 1;
const _fileCreate = 2;
const _directoryFile = 1;
const _synchronousIo = 0x20;
const _caseInsensitive = 0x40;
const _attributeTagInfo = 9;
const _fileRenameInformation = 10;
const _attributeDirectory = 0x10;

abstract class _OutputDirectory {
  factory _OutputDirectory.open(String path) {
    if (Platform.isWindows) return _WindowsDirectory.open(path);
    if (Platform.isLinux || Platform.isMacOS) return _PosixDirectory.open(path);
    throw UnsupportedError('Atomic executable publication is unavailable');
  }
  _OutputDirectory openChild(String name);
  void createChild(String name);
  bool isWithin(_OutputDirectory root);
  bool containsEntry(String name);
  int? publish(File artifact, String name);
  void close();
}

_OutputDirectory _prepareDirectory(String path, List<String> parts) {
  final root = _OutputDirectory.open(path);
  var current = root;
  var transferred = false;
  try {
    for (final part in parts.where((p) => p.isNotEmpty && p != '.')) {
      final previous = current;
      current = _openOrCreateDirectory(current, root, part);
      if (!identical(previous, root)) previous.close();
    }
    _requireWithin(current, root);
    if (!identical(current, root)) root.close();
    transferred = true;
    return current;
  } finally {
    if (!transferred) {
      try {
        if (!identical(current, root)) current.close();
      } finally {
        root.close();
      }
    }
  }
}

_OutputDirectory _openOrCreateDirectory(
  _OutputDirectory parent,
  _OutputDirectory root,
  String name,
) {
  try {
    return parent.openChild(name);
  } on FileSystemException catch (error) {
    if (!_isMissing(error.osError?.errorCode)) rethrow;
  }
  _requireWithin(parent, root);
  try {
    parent.createChild(name);
  } on FileSystemException catch (error) {
    if (!_isExists(error.osError?.errorCode)) rethrow;
  }
  return parent.openChild(name);
}

void _requireWithin(_OutputDirectory directory, _OutputDirectory root) {
  if (!directory.isWithin(root))
    throw ArgumentError('Output escapes the project');
}

bool _isMissing(int? error) => error == 2 || Platform.isWindows && error == 3;
bool _isExists(int? error) =>
    Platform.isWindows ? error == 80 || error == 183 : error == 17;
FileSystemException _fileError(String message, String path, int error) =>
    FileSystemException(message, path, OSError(message, error));

class _PosixDirectory implements _OutputDirectory {
  int? _fd;
  final String path;
  _PosixDirectory._(this._fd, this.path);

  static _PosixDirectory open(String path) => _open(_cwd, path, path);
  int get fd => _fd ?? (throw StateError('Output directory is closed'));
  static int get _cwd => Platform.isMacOS ? -2 : -100;
  static int get _flags => Platform.isMacOS
      ? 0x40100000 |
            0x01000000 // O_SEARCH | O_CLOEXEC
      : 0x00200000 | 0x00080000; // O_PATH | O_CLOEXEC

  static _PosixDirectory _open(int parent, String name, String path) {
    final native = _nativePath(name);
    try {
      final errno = _errnoLocation();
      final result = _openAt(parent, native, _flags);
      if (result < 0)
        throw _fileError('Cannot open output directory', path, errno.value);
      final directory = _PosixDirectory._(result, path);
      try {
        if ((directory._stat().mode & 0xf000) != 0x4000) {
          throw _fileError('Output parent is not a directory', path, 20);
        }
        return directory;
      } catch (_) {
        directory.close();
        rethrow;
      }
    } finally {
      _free(native);
    }
  }

  @override
  _PosixDirectory openChild(String name) => _open(fd, name, '$path/$name');

  @override
  void createChild(String name) {
    final native = _nativePath(name);
    try {
      final errno = _errnoLocation();
      if (_mkdirAt(fd, native, 0x1ff) != 0) {
        throw _fileError(
          'Cannot create output directory',
          '$path/$name',
          errno.value,
        );
      }
    } finally {
      _free(native);
    }
  }

  ({int device, int inode, int mode}) _stat() {
    final memory = _allocate(256);
    try {
      final errno = _errnoLocation();
      if (_fstat(fd, memory) != 0) {
        throw _fileError('Cannot identify output directory', path, errno.value);
      }
      // Match the public stat64/time64 redirects from the libc headers.
      // Legacy Linux ARM32 keeps inode64 at byte 96; time64 and 64-bit
      // Unix place it at byte 8. Darwin's dev_t remains 32-bit.
      final legacyArm =
          Platform.isLinux && sizeOf<IntPtr>() == 4 && !_linuxTime64;
      final device = Platform.isMacOS
          ? memory.cast<Uint32>().value
          : memory.cast<Uint64>().value;
      final inode = (memory + (legacyArm ? 96 : 8)).cast<Uint64>().value;
      final mode = Platform.isMacOS
          ? (memory + 4).cast<Uint16>().value
          : (memory + (Abi.current() == Abi.linuxX64 ? 24 : 16))
                .cast<Uint32>()
                .value;
      return (device: device, inode: inode, mode: mode);
    } finally {
      _free(memory);
    }
  }

  @override
  bool isWithin(_OutputDirectory root) {
    final anchor = (root as _PosixDirectory)._stat();
    final expected = (anchor.device, anchor.inode);
    final visited = <(int, int)>{};
    var current = openChild('.');
    try {
      while (true) {
        final stat = current._stat();
        final identity = (stat.device, stat.inode);
        if (identity == expected) return true;
        if (!visited.add(identity)) return false;
        final previous = current;
        current = previous.openChild('..');
        previous.close();
      }
    } finally {
      current.close();
    }
  }

  @override
  bool containsEntry(String name) {
    final arena = _NativeArena();
    try {
      final native = arena.path(name);
      final memory = arena.allocate(256);
      final errno = _errnoLocation();
      if (_fstatAt(fd, native, memory, Platform.isMacOS ? 0x20 : 0x100) == 0)
        return true;
      final error = errno.value;
      if (error == 2) return false;
      throw _fileError('Cannot inspect output', '$path/$name', error);
    } finally {
      arena.close();
    }
  }

  @override
  int? publish(File artifact, String name) {
    final from = _PosixDirectory.open(artifact.parent.absolute.path);
    try {
      return _movePosix(
        from.fd,
        nativeFileUri(artifact.uri).pathSegments.last,
        fd,
        name,
      );
    } finally {
      from.close();
    }
  }

  @override
  void close() {
    final descriptor = _fd;
    _fd = null;
    if (descriptor == null) return;
    final errno = _errnoLocation();
    if (_closeFd(descriptor) != 0) {
      throw _fileError('Cannot close output directory', path, errno.value);
    }
  }
}

int? _movePosix(
  int fromDirectory,
  String fromName,
  int toDirectory,
  String toName,
) {
  final from = _nativePath(fromName);
  try {
    final to = _nativePath(toName);
    try {
      final errno = _errnoLocation();
      if (Platform.isMacOS) {
        if (_renameExclusive(
              fromDirectory,
              from,
              toDirectory,
              to,
              _darwinExclusiveFlag,
            ) ==
            0)
          return null;
        if (errno.value != 45) return errno.value;
      } else {
        final rename = _renameNoReplace;
        if (rename != null) {
          if (rename(
                fromDirectory,
                from,
                toDirectory,
                to,
                _renameNoReplaceFlag,
              ) ==
              0)
            return null;
          final error = errno.value;
          if (error != 38 && error != 95 && error != 22) return error;
        }
      }
      // Some libcs, kernels and filesystems lack no-replace rename.
      // Linking creates the name atomically; the snapshot owns the original.
      return _linkAt(fromDirectory, from, toDirectory, to, 0) == 0
          ? null
          : errno.value;
    } finally {
      _free(to);
    }
  } finally {
    _free(from);
  }
}

class _WindowsDirectory implements _OutputDirectory {
  Pointer<Void>? _handle;
  final String path;
  _WindowsDirectory._(this._handle, this.path);
  Pointer<Void> get handle =>
      _handle ?? (throw StateError('Output directory is closed'));

  static _WindowsDirectory open(String path) {
    final native = _nativePath(path);
    try {
      final lastError = _windowsLastError;
      final result = _createFile(
        native.cast(),
        _directoryAccess,
        _shareAll,
        nullptr,
        _openExisting,
        _backupSemantics,
        nullptr,
      );
      final error = lastError();
      if (result == Pointer<Void>.fromAddress(-1)) {
        throw _fileError('Cannot open output directory', path, error);
      }
      return _checked(result, path);
    } finally {
      _free(native);
    }
  }

  static _WindowsDirectory _checked(Pointer<Void> handle, String path) {
    final directory = _WindowsDirectory._(handle, path);
    try {
      if ((directory._attributes() & _attributeDirectory) == 0) {
        throw _fileError('Output parent is not a directory', path, 267);
      }
      return directory;
    } catch (_) {
      directory.close();
      rethrow;
    }
  }

  int _attributes() {
    final memory = _allocate(8);
    try {
      final lastError = _windowsLastError;
      if (_getFileInformation(handle, _attributeTagInfo, memory.cast(), 8) ==
          0) {
        throw _fileError('Cannot inspect output directory', path, lastError());
      }
      return memory.cast<Uint32>().value;
    } finally {
      _free(memory);
    }
  }

  @override
  _WindowsDirectory openChild(String name) =>
      _checked(_relativeWindowsOpen(handle, name, _fileOpen), '$path\\$name');

  @override
  void createChild(String name) {
    final child = _checked(
      _relativeWindowsOpen(handle, name, _fileCreate),
      '$path\\$name',
    );
    child.close();
  }

  @override
  bool isWithin(_OutputDirectory root) {
    final anchor = (root as _WindowsDirectory)._finalPath();
    final actual = _finalPath();
    return actual == anchor || actual.startsWith('$anchor\\');
  }

  String _finalPath() {
    var capacity = 512;
    final lastError = _windowsLastError;
    while (true) {
      final memory = _allocate(capacity * 2);
      try {
        final result = _getFinalPath(handle, memory.cast(), capacity, 0);
        if (result == 0)
          throw _fileError(
            'Cannot resolve output directory',
            path,
            lastError(),
          );
        if (result >= capacity) {
          capacity = result + 1;
          continue;
        }
        var value = String.fromCharCodes(
          memory.cast<Uint16>().asTypedList(result),
        );
        while (value.endsWith(r'\')) {
          value = value.substring(0, value.length - 1);
        }
        return value;
      } finally {
        _free(memory);
      }
    }
  }

  @override
  bool containsEntry(String name) {
    Pointer<Void>? entry;
    try {
      entry = _relativeWindowsOpen(
        handle,
        name,
        _fileOpen,
        directory: false,
        reparse: true,
      );
      return true;
    } on FileSystemException catch (error) {
      if (_isMissing(error.osError?.errorCode)) return false;
      rethrow;
    } finally {
      if (entry != null) _closeWindows(entry, '$path\\$name');
    }
  }

  @override
  int? publish(File artifact, String name) =>
      _moveWindows(artifact, handle, name);

  @override
  void close() {
    final value = _handle;
    _handle = null;
    if (value != null) _closeWindows(value, path);
  }
}

Pointer<Void> _relativeWindowsOpen(
  Pointer<Void> parent,
  String name,
  int disposition, {
  bool directory = true,
  bool reparse = false,
}) {
  final arena = _NativeArena();
  try {
    final text = arena.path(name, windowsAbsolute: false);
    final unicode = arena
        .allocate(sizeOf<_UnicodeString>())
        .cast<_UnicodeString>();
    final attributes = arena
        .allocate(sizeOf<_ObjectAttributes>())
        .cast<_ObjectAttributes>();
    final status = arena
        .allocate(sizeOf<_IoStatusBlock>())
        .cast<_IoStatusBlock>();
    final result = arena
        .allocate(sizeOf<Pointer<Void>>())
        .cast<Pointer<Void>>();
    final length = name.codeUnits.length * 2;
    if (length > 0xfffe) throw ArgumentError('Output component is too long');
    unicode.ref
      ..length = length
      ..maximumLength = length
      ..buffer = text.cast();
    attributes.ref
      ..length = sizeOf<_ObjectAttributes>()
      ..root = parent
      ..name = unicode
      ..attributes = _caseInsensitive
      ..security = nullptr
      ..quality = nullptr;
    result.value = nullptr;
    final code = _ntCreateFile(
      result,
      directory ? _directoryAccess : _entryAccess,
      attributes,
      status,
      nullptr,
      0,
      _shareAll,
      disposition,
      _synchronousIo |
          (directory ? _directoryFile : 0) |
          (reparse ? _openReparsePoint : 0),
      nullptr,
      0,
    );
    if (code < 0) {
      throw _fileError(
        'Cannot prepare output component',
        name,
        _ntStatusError(code),
      );
    }
    return result.value;
  } finally {
    arena.close();
  }
}

int? _moveWindows(File artifact, Pointer<Void> parent, String name) {
  final from = _nativePath(artifact.absolute.path);
  Pointer<Void>? source;
  try {
    final lastError = _windowsLastError;
    source = _createFile(
      from.cast(),
      _synchronousDeleteAndReadAttributes,
      _shareAll,
      nullptr,
      _openExisting,
      _backupSemantics | _openReparsePoint,
      nullptr,
    );
    final error = lastError();
    if (source == Pointer<Void>.fromAddress(-1)) {
      source = null;
      return error;
    }
    final offset = sizeOf<IntPtr>() == 8 ? 20 : 12;
    final units = name.codeUnits;
    final bytes = sizeOf<_FileRenameInformation>() + (units.length + 1) * 2;
    final statusBytes = sizeOf<_IoStatusBlock>();
    final memory = _allocate(statusBytes + bytes);
    try {
      memory
          .asTypedList(statusBytes + bytes)
          .fillRange(0, statusBytes + bytes, 0);
      final status = memory.cast<_IoStatusBlock>();
      final rename = memory + statusBytes;
      final info = rename.cast<_FileRenameInformation>();
      // Use the held directory and keep ReplaceIfExists false.
      info.ref
        ..flags = 0
        ..root = parent
        ..nameLength = units.length * 2;
      (rename + offset)
          .cast<Uint16>()
          .asTypedList(units.length)
          .setAll(0, units);
      // CreateFileW opened the source for synchronous I/O.
      final result = _ntSetInformationFile(
        source,
        status,
        info,
        bytes,
        _fileRenameInformation,
      );
      return result == 0 ? null : _ntStatusError(result);
    } finally {
      _free(memory);
    }
  } finally {
    try {
      if (source != null) _closeWindows(source, artifact.path);
    } finally {
      _free(from);
    }
  }
}

void _closeWindows(Pointer<Void> handle, String path) {
  final lastError = _windowsLastError;
  if (_closeHandle(handle) == 0)
    throw _fileError('Cannot close output handle', path, lastError());
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

class _NativeArena {
  final _values = <Pointer<Uint8>>[];
  Pointer<Uint8> allocate(int bytes) {
    final value = _allocate(bytes);
    return _keep(value);
  }

  Pointer<Uint8> path(String value, {bool windowsAbsolute = true}) {
    final memory = _nativePath(value, windowsAbsolute: windowsAbsolute);
    return _keep(memory);
  }

  Pointer<Uint8> _keep(Pointer<Uint8> memory) {
    try {
      _values.add(memory);
      return memory;
    } catch (_) {
      _free(memory);
      rethrow;
    }
  }

  void close() {
    for (final value in _values.reversed) {
      _free(value);
    }
    _values.clear();
  }
}

Pointer<Uint8> _allocate(int bytes) {
  final value = _malloc(bytes);
  if (value == nullptr) throw StateError('Cannot allocate native output state');
  return value;
}

Pointer<Uint8> _nativePath(String path, {bool windowsAbsolute = true}) {
  if (path.contains('\x00')) throw ArgumentError('Path contains a NUL');
  final wide = Platform.isWindows;
  final units = wide
      ? (windowsAbsolute ? _windowsPath(path) : path).codeUnits
      : utf8.encode(path);
  final memory = _allocate((units.length + 1) * (wide ? 2 : 1));
  try {
    final buffer = wide
        ? memory.cast<Uint16>().asTypedList(units.length + 1)
        : memory.asTypedList(units.length + 1);
    buffer.setAll(0, units);
    buffer[units.length] = 0;
    return memory;
  } catch (_) {
    _free(memory);
    rethrow;
  }
}

final class _UnicodeString extends Struct {
  @Uint16()
  external int length;
  @Uint16()
  external int maximumLength;
  external Pointer<Uint16> buffer;
}

final class _ObjectAttributes extends Struct {
  @Uint32()
  external int length;
  external Pointer<Void> root;
  external Pointer<_UnicodeString> name;
  @Uint32()
  external int attributes;
  external Pointer<Void> security;
  external Pointer<Void> quality;
}

final class _IoStatusBlock extends Struct {
  external Pointer<Void> status;
  @UintPtr()
  external int information;
}

final class _FileRenameInformation extends Struct {
  @Uint32()
  external int flags;
  external Pointer<Void> root;
  @Uint32()
  external int nameLength;
  @Array(1)
  external Array<Uint16> name;
}

typedef _RenameNative = Int32 Function(
  Int32,
  Pointer<Uint8>,
  Int32,
  Pointer<Uint8>,
  Uint32,
);
typedef _Rename = int Function(int, Pointer<Uint8>, int, Pointer<Uint8>, int);
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
late final _openAt = _libc
    .lookupFunction<
      Int32 Function(Int32, Pointer<Uint8>, Int32),
      int Function(int, Pointer<Uint8>, int)
    >('openat');
late final _mkdirAt = Platform.isMacOS
    ? _libc.lookupFunction<
        Int32 Function(Int32, Pointer<Uint8>, Uint16),
        int Function(int, Pointer<Uint8>, int)
      >('mkdirat')
    : _libc.lookupFunction<
        Int32 Function(Int32, Pointer<Uint8>, Uint32),
        int Function(int, Pointer<Uint8>, int)
      >('mkdirat');
late final _closeFd = _libc
    .lookupFunction<Int32 Function(Int32), int Function(int)>('close');
late final _linuxTime64 =
    Platform.isLinux &&
    sizeOf<IntPtr>() == 4 &&
    _libc.providesSymbol('__fstat64_time64') &&
    _libc.providesSymbol('__fstatat64_time64');
String _statSymbol(String base) {
  if (Platform.isMacOS && _libc.providesSymbol('$base\$INODE64'))
    return '$base\$INODE64';
  if (Platform.isLinux && sizeOf<IntPtr>() == 4) {
    return _linuxTime64 ? '__${base}64_time64' : '${base}64';
  }
  return base;
}

late final _fstat = _libc
    .lookupFunction<
      Int32 Function(Int32, Pointer<Uint8>),
      int Function(int, Pointer<Uint8>)
    >(_statSymbol('fstat'));
late final _fstatAt = _libc
    .lookupFunction<
      Int32 Function(Int32, Pointer<Uint8>, Pointer<Uint8>, Int32),
      int Function(int, Pointer<Uint8>, Pointer<Uint8>, int)
    >(_statSymbol('fstatat'));
late final _renameExclusive = _libc.lookupFunction<_RenameNative, _Rename>(
  'renameatx_np',
);
late final _renameNoReplace = _libc.providesSymbol('renameat2')
    ? _libc.lookupFunction<_RenameNative, _Rename>('renameat2')
    : null;
late final _linkAt = _libc
    .lookupFunction<
      Int32 Function(Int32, Pointer<Uint8>, Int32, Pointer<Uint8>, Int32),
      int Function(int, Pointer<Uint8>, int, Pointer<Uint8>, int)
    >('linkat');
late final _kernel = DynamicLibrary.open('kernel32.dll');
late final _ntdll = DynamicLibrary.open('ntdll.dll');
late final _createFile = _kernel
    .lookupFunction<
      Pointer<Void> Function(
        Pointer<Uint16>,
        Uint32,
        Uint32,
        Pointer<Void>,
        Uint32,
        Uint32,
        Pointer<Void>,
      ),
      Pointer<Void> Function(
        Pointer<Uint16>,
        int,
        int,
        Pointer<Void>,
        int,
        int,
        Pointer<Void>,
      )
    >('CreateFileW');
late final _closeHandle = _kernel
    .lookupFunction<Int32 Function(Pointer<Void>), int Function(Pointer<Void>)>(
      'CloseHandle',
    );
late final _getFileInformation = _kernel
    .lookupFunction<
      Int32 Function(Pointer<Void>, Int32, Pointer<Void>, Uint32),
      int Function(Pointer<Void>, int, Pointer<Void>, int)
    >('GetFileInformationByHandleEx');
late final _getFinalPath = _kernel
    .lookupFunction<
      Uint32 Function(Pointer<Void>, Pointer<Uint16>, Uint32, Uint32),
      int Function(Pointer<Void>, Pointer<Uint16>, int, int)
    >('GetFinalPathNameByHandleW');
late final _ntSetInformationFile = _ntdll
    .lookupFunction<
      Int32 Function(
        Pointer<Void>,
        Pointer<_IoStatusBlock>,
        Pointer<_FileRenameInformation>,
        Uint32,
        Int32,
      ),
      int Function(
        Pointer<Void>,
        Pointer<_IoStatusBlock>,
        Pointer<_FileRenameInformation>,
        int,
        int,
      )
    >('NtSetInformationFile');
late final _ntCreateFile = _ntdll
    .lookupFunction<
      Int32 Function(
        Pointer<Pointer<Void>>,
        Uint32,
        Pointer<_ObjectAttributes>,
        Pointer<_IoStatusBlock>,
        Pointer<Int64>,
        Uint32,
        Uint32,
        Uint32,
        Uint32,
        Pointer<Void>,
        Uint32,
      ),
      int Function(
        Pointer<Pointer<Void>>,
        int,
        Pointer<_ObjectAttributes>,
        Pointer<_IoStatusBlock>,
        Pointer<Int64>,
        int,
        int,
        int,
        int,
        Pointer<Void>,
        int,
      )
    >('NtCreateFile');
late final _ntStatusError = _ntdll
    .lookupFunction<Uint32 Function(Int32), int Function(int)>(
      'RtlNtStatusToDosError',
    );
late final _windowsLastError = _bindWindowsLastError();
int Function() _bindWindowsLastError() {
  final getLastError = _kernel
      .lookupFunction<Uint32 Function(), int Function()>(
        'GetLastError',
        isLeaf: true,
      );
  // Resolve the first invocation before an output operation sets its error.
  getLastError();
  return getLastError;
}
