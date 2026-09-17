import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/element/element.dart';
import 'package:analyzer/dart/element/type.dart';

/// Analyzes Dart source AST to find LuxoClient API calls and trace
/// which fields are accessed on the returned objects.
///
/// Supports nested field tracking:
///   post.user.name        → "user{name}"
///   post.comments[0].content → "comments{content}"
///   post.comments[0].user.id → "comments{user{id}}"
///
/// Result: map of API name → nested select string.
class SelectAnalyzer extends RecursiveAstVisitor<void> {
  /// API name → field tree (nested)
  final Map<String, FieldNode> hints = {};

  /// Resolved symbols keep aliases, callbacks and shadowed variables separate.
  final Map<Element, List<_SelectionSource>> _bindings = {};
  final Map<DartType, bool> _modelTypes = {};

  /// Known model fields: type → { fieldName → fieldTypeName }
  final Map<String, Map<String, String>> modelFields;
  final Uri? clientLibrary;

  SelectAnalyzer(this.modelFields, {this.clientLibrary});

  static const maxNestingDepth = 5;

  /// Build the $select string from collected hints.
  /// Warns if nesting exceeds maxNestingDepth.
  Map<String, String> buildSelectStrings() {
    final result = <String, String>{};
    for (final entry in hints.entries) {
      final depth = entry.value.maxDepth();
      if (depth > maxNestingDepth) {
        print(
          '[luxo] Warning: ${entry.key} has $depth-level nested field selection '
          '(max recommended: $maxNestingDepth). Consider restructuring your query.',
        );
      }
      final selectStr = entry.value.toSelectString();
      if (selectStr.isNotEmpty) {
        result[entry.key] = selectStr;
      }
    }
    return result;
  }

  @override
  void visitVariableDeclaration(VariableDeclaration node) {
    _bind(node.declaredFragment?.element, _sources(node.initializer));
    super.visitVariableDeclaration(node);
  }

  @override
  void visitAssignmentExpression(AssignmentExpression node) {
    final target = node.leftHandSide;
    if (target is SimpleIdentifier && node.operator.lexeme == '=') {
      _bind(target.element, _sources(node.rightHandSide));
    }
    super.visitAssignmentExpression(node);
  }

  @override
  void visitForEachPartsWithDeclaration(ForEachPartsWithDeclaration node) {
    _bind(
      node.loopVariable.declaredFragment?.element,
      _iterated(_sources(node.iterable)),
    );
    super.visitForEachPartsWithDeclaration(node);
  }

  @override
  void visitMethodInvocation(MethodInvocation node) {
    _bindCallback(node);
    super.visitMethodInvocation(node);
  }

  @override
  void visitPropertyAccess(PropertyAccess node) {
    _record(_sources(node));
    super.visitPropertyAccess(node);
  }

  @override
  void visitPrefixedIdentifier(PrefixedIdentifier node) {
    _record(_sources(node));
    super.visitPrefixedIdentifier(node);
  }

  void _bind(Element? element, List<_SelectionSource> sources) {
    if (element == null || sources.isEmpty) return;
    final existing = _bindings.putIfAbsent(element, () => []);
    for (final source in sources) {
      if (!existing.any((s) => _sameSource(s, source))) {
        existing.add(source);
      }
    }
  }

  bool _sameSource(_SelectionSource a, _SelectionSource b) {
    if (a.api != b.api || a.type != b.type || a.path.length != b.path.length) {
      return false;
    }
    for (var i = 0; i < a.path.length; i++) {
      if (a.path[i] != b.path[i]) return false;
    }
    return true;
  }

  List<_SelectionSource> _sources(Expression? expression) {
    return switch (expression) {
      SimpleIdentifier() => _bindings[expression.element] ?? [],
      AwaitExpression() => _retype(
        _sources(expression.expression),
        expression.staticType,
      ),
      ParenthesizedExpression() => _sources(expression.expression),
      AsExpression() => _sources(expression.expression),
      PostfixExpression() when expression.operator.lexeme == '!' => _sources(
        expression.operand,
      ),
      IndexExpression() => _retype(
        _sources(expression.target),
        expression.staticType,
      ),
      PrefixedIdentifier() => _properties(
        _sources(expression.prefix),
        expression.identifier.name,
      ),
      PropertyAccess() => _properties(
        _sources(expression.realTarget),
        expression.propertyName.name,
      ),
      ConditionalExpression() => [
        ..._sources(expression.thenExpression),
        ..._sources(expression.elseExpression),
      ],
      MethodInvocation() => _callSources(expression),
      _ => [],
    };
  }

  List<_SelectionSource> _callSources(MethodInvocation call) {
    final target = call.realTarget?.staticType;
    final result = call.staticType;
    if (target is! InterfaceType || result == null) return [];
    final isClient = [target, ...target.allSupertypes].any(
      (type) =>
          type.element.name == 'LuxoClient' &&
          (clientLibrary == null || type.element.library.uri == clientLibrary),
    );
    if (!isClient || call.methodName.name.startsWith('_')) return [];
    return [(api: call.methodName.name, type: result, path: <String>[])];
  }

  List<_SelectionSource> _retype(
    List<_SelectionSource> sources,
    DartType? type,
  ) => type == null
      ? []
      : [for (final s in sources) (api: s.api, type: type, path: s.path)];

  List<_SelectionSource> _properties(
    List<_SelectionSource> sources,
    String field,
  ) {
    return [
      for (final source in sources)
        if (_property(source, field) case final result?) result,
    ];
  }

  _SelectionSource? _property(_SelectionSource source, String field) {
    final type = source.type;
    if (type is! InterfaceType) return null;
    if (_isSDKType(type, 'Selected')) {
      return field == 'value'
          ? _withType(source, type.typeArguments.single)
          : null;
    }
    if (_isCollection(type) &&
        const {'first', 'last', 'single'}.contains(field)) {
      return _withType(source, type.typeArguments.single);
    }
    final getter = type.getGetter(field);
    if (getter == null) return null;
    if (_isSDKType(type, 'Page')) {
      return field == 'items' ? _withType(source, getter.returnType) : null;
    }
    final known = modelFields[type.element.name];
    if (known != null
        ? !known.containsKey(field)
        : !_isSDKType(getter.returnType, 'Selected')) {
      return null;
    }
    return (
      api: source.api,
      type: getter.returnType,
      path: [...source.path, field],
    );
  }

  _SelectionSource _withType(_SelectionSource source, DartType type) =>
      (api: source.api, type: type, path: source.path);

  bool _isSDKType(DartType type, String name) =>
      type is InterfaceType &&
      type.element.name == name &&
      type.element.library.uri.toString() ==
          'package:luxo_client/src/types.dart';

  bool _isCollection(DartType type) =>
      type.isDartCoreList || type.isDartCoreIterable;

  List<_SelectionSource> _iterated(List<_SelectionSource> sources) => [
    for (final source in sources)
      if (source.type case InterfaceType type when _isCollection(type))
        _withType(source, type.typeArguments.single),
  ];

  void _bindCallback(MethodInvocation call) {
    if (call.argumentList.arguments.isEmpty) return;
    final callback = call.argumentList.arguments.first;
    if (callback is! FunctionExpression) return;
    final parameter = callback.parameters?.parameters.firstOrNull;
    if (parameter == null) return;
    final sources = _sources(call.realTarget);
    final values = <_SelectionSource>[];
    for (final source in sources) {
      final type = source.type;
      if (type is! InterfaceType) continue;
      final method = call.methodName.name;
      if ((type.isDartAsyncFuture && method == 'then') ||
          (_isCollection(type) &&
              const {
                'map',
                'where',
                'forEach',
                'any',
                'every',
                'firstWhere',
                'lastWhere',
                'singleWhere',
              }.contains(method))) {
        values.add(_withType(source, type.typeArguments.first));
      }
    }
    _bind(parameter.declaredFragment?.element, values);
  }

  /// Only terminal schema fields form a valid selection, never wrapper members.
  void _record(List<_SelectionSource> sources) {
    for (final source in sources) {
      if (source.path.isEmpty || _isModel(_valueType(source.type))) continue;
      var node = hints.putIfAbsent(source.api, FieldNode.root);
      for (final field in source.path) {
        node = node.addChild(field);
      }
    }
  }

  DartType _valueType(DartType type) {
    while (type is InterfaceType &&
        (_isSDKType(type, 'Selected') || _isCollection(type))) {
      type = type.typeArguments.single;
    }
    return type;
  }

  bool _isModel(DartType type) => _modelTypes.putIfAbsent(
    type,
    () =>
        type is InterfaceType &&
        (modelFields.containsKey(type.element.name) ||
            type.getters.any(
              (getter) => _isSDKType(getter.returnType, 'Selected'),
            )),
  );
}

typedef _SelectionSource = ({String api, DartType type, List<String> path});

/// Tree node for nested field selection.
/// Root → { user → { name, email }, comments → { content, user → { id } } }
class FieldNode {
  final String name;
  final Map<String, FieldNode> children;

  FieldNode(this.name) : children = {};
  FieldNode.root() : name = '', children = {};

  FieldNode addChild(String fieldName) {
    return children.putIfAbsent(fieldName, () => FieldNode(fieldName));
  }

  /// Merge a selection without sharing mutable subtrees between analyses.
  void merge(FieldNode other) {
    for (final child in other.children.values) {
      addChild(child.name).merge(child);
    }
  }

  /// Max depth of the tree
  int maxDepth() {
    if (children.isEmpty) return 0;
    int max = 0;
    for (final child in children.values) {
      final d = child.maxDepth();
      if (d > max) max = d;
    }
    return max + 1;
  }

  /// Build $select string: "name,email,comments{content,user{id}}"
  String toSelectString() {
    if (children.isEmpty) return '';
    final parts = <String>[];
    final names = children.keys.toList()..sort();
    for (final name in names) {
      final child = children[name]!;
      final nested = child.toSelectString();
      if (nested.isNotEmpty) {
        parts.add('${child.name}{$nested}');
      } else {
        parts.add(child.name);
      }
    }
    return parts.join(',');
  }
}
