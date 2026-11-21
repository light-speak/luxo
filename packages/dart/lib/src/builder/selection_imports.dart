import 'dart:convert';

import 'package:analyzer/dart/analysis/utilities.dart';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/element/element.dart';

/// Imports needed only by static selection factories in the build snapshot.
class SelectionImports {
  final _bindings = <LibraryElement, Map<LibraryElement, String>>{};

  String factory(LibraryElement library, MethodElement method) {
    final bindings = _bindings.putIfAbsent(library, () => {});
    final prefix = bindings.putIfAbsent(method.library, () {
      var candidate = r'$luxoSelectionClient' + bindings.length.toString();
      while (library.fragments.any(
            (fragment) => fragment.source.contents.data.contains(candidate),
          ) ||
          bindings.containsValue(candidate)) {
        candidate += '_';
      }
      return candidate;
    });
    return '$prefix.${method.enclosingElement!.name}.\$prepareSelection';
  }

  void apply(Map<String, List<int>> prepared, Uri root) {
    for (final entry in _bindings.entries) {
      final uri = entry.key.firstFragment.source.uri;
      // Source URIs may be package URIs; fullName identifies the owning file.
      final path = Uri.file(entry.key.firstFragment.source.fullName);
      if (!path.toString().startsWith(root.toString())) {
        throw StateError('Selection library is outside the build: $uri');
      }
      final relative = path.toString().substring(root.toString().length);
      final bytes = prepared[relative];
      if (bytes == null) throw StateError('Missing selection library: $uri');
      final source = utf8.decode(bytes);
      final unit = parseString(content: source).unit;
      final offset =
          unit.directives.whereType<PartDirective>().firstOrNull?.offset ??
          unit.declarations.firstOrNull?.offset ??
          unit.end;
      final imports = StringBuffer();
      for (final binding in entry.value.entries) {
        final target = binding.key.uri.toString();
        // URI literals cannot introduce source characters or interpolation.
        final escaped = target
            .replaceAll('\\', '\\\\')
            .replaceAll("'", "\\'")
            .replaceAll(r'$', r'\$')
            .replaceAll('\n', r'\n')
            .replaceAll('\r', r'\r');
        imports.write("import '$escaped' as ${binding.value}; ");
      }
      prepared[relative] = utf8.encode(
        source.replaceRange(offset, offset, imports.toString()),
      );
    }
  }
}
