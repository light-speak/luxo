import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/element/element.dart';

import 'select_analyzer.dart';

/// Edits resolved call sites, never the original file or an API-wide hint table.
String rewriteSelections(
  CompilationUnit unit,
  String source, {
  Uri? clientLibrary,
}) {
  final analyzer = SelectAnalyzer({}, clientLibrary: clientLibrary);
  unit.accept(analyzer);
  final edits = <({int offset, String text})>[];
  for (final entry in analyzer.callSelections.entries) {
    final call = entry.key;
    final method = call.methodName.element;
    if (method is! MethodElement ||
        !method.formalParameters.any((p) => p.isNamed && p.name == 'select')) {
      continue;
    }
    if (call.argumentList.arguments.any(
      (a) => a is NamedArgument && a.name.lexeme == 'select',
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
    edits.add((offset: closing.offset, text: "$separator select: '$select'"));
  }
  edits.sort((a, b) => b.offset.compareTo(a.offset));
  for (final edit in edits) {
    source = source.replaceRange(edit.offset, edit.offset, edit.text);
  }
  return source;
}
