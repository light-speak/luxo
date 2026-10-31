import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/element/element.dart';

import 'select_analyzer.dart';

/// Edits resolved call sites, never the original file or an API-wide hint table.
String rewriteSelections(
  CompilationUnit unit,
  String source, {
  Uri? clientLibrary,
  LibraryElement? library,
  String Function(LibraryElement, MethodElement)? hiddenFactory,
}) {
  final analyzer = SelectAnalyzer({}, clientLibrary: clientLibrary);
  unit.accept(analyzer);
  final edits = <({int offset, String text})>[];
  final plans = StringBuffer();
  for (final entry in analyzer.callSelections.entries) {
    final call = entry.key;
    final method = call.methodName.element;
    if (method is! MethodElement ||
        !method.formalParameters.any((p) => p.isNamed && p.name == 'select')) {
      continue;
    }
    if (call.argumentList.arguments.any(
      (a) =>
          a is NamedArgument &&
          (a.name.lexeme == 'select' || a.name.lexeme == r'$selectionPlan'),
    )) {
      continue;
    }
    final select = entry.value.toSelectString();
    if (select.isEmpty || analyzer.escapedCalls.contains(call)) {
      throw StateError(
        'Cannot infer selection for ${call.methodName.name} at offset ${call.offset}; '
        'select fields locally or provide an explicit selection.',
      );
    }
    final closing = call.argumentList.rightParenthesis;
    final previous = closing.previous!;
    final separator = previous.lexeme == '(' || previous.lexeme == ','
        ? ''
        : ',';
    // Schema field names are identifiers; no source text is interpolated.
    var argument = "$separator select: '$select'";
    if (library != null &&
        method.formalParameters.any(
          (p) => p.isNamed && p.name == r'$selectionPlan',
        )) {
      final name = _hoistPlan(
        unit,
        source,
        library,
        method,
        call,
        select,
        plans,
        hiddenFactory,
      );
      argument += ', \$selectionPlan: $name';
    }
    edits.add((offset: closing.offset, text: argument));
  }
  if (plans.isNotEmpty) {
    edits.add((offset: unit.declarations.first.offset, text: plans.toString()));
  }
  edits.sort((a, b) => b.offset.compareTo(a.offset));
  for (final edit in edits) {
    source = source.replaceRange(edit.offset, edit.offset, edit.text);
  }
  return source;
}

String _selectionFactory(
  LibraryElement library,
  MethodElement method,
  String Function(LibraryElement, MethodElement)? hiddenFactory,
) {
  final owner = method.enclosingElement;
  if (owner == null) throw StateError('Missing generated client owner');
  if (library == method.library) return '${owner.name}.\$prepareSelection';
  for (final fragment in library.fragments) {
    for (final imported in fragment.libraryImports) {
      final prefix = imported.prefix?.element.name;
      final visible = prefix == null
          ? imported.namespace.get2(owner.name!)
          : imported.namespace.getPrefixed2(prefix, owner.name!);
      if (visible != owner) continue;
      return '${prefix == null ? '' : '$prefix.'}${owner.name}.\$prepareSelection';
    }
  }
  if (hiddenFactory != null) return hiddenFactory(library, method);
  throw StateError('Generated client is not visible for selection preparation');
}

String _hoistPlan(
  CompilationUnit unit,
  String source,
  LibraryElement library,
  MethodElement method,
  MethodInvocation call,
  String select,
  StringBuffer plans,
  String Function(LibraryElement, MethodElement)? hiddenFactory,
) {
  final factory = _selectionFactory(library, method, hiddenFactory);
  final fragment = library.fragments.indexWhere(
    (f) => f == unit.declaredFragment,
  );
  if (fragment < 0) throw StateError('Missing selection source fragment');
  var name = '\$luxoSelection${fragment}_${call.offset}';
  while (source.contains(name) || library.exportNamespace.get2(name) != null) {
    name += '_';
  }
  plans.write(" final $name = $factory('$select');");
  return name;
}
