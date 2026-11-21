import '../identifier.dart';

import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/element/element.dart';
import 'package:analyzer/dart/element/type.dart';

part 'select_sealed.dart';

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

  /// Each invocation owns its own selection, even when API names are equal.
  final Map<MethodInvocation, FieldNode> callSelections = {};
  final Set<MethodInvocation> escapedCalls = {};

  /// Resolved symbols keep aliases, callbacks and shadowed variables separate.
  final Map<Element, List<_SelectionSource>> _bindings = {};
  final Map<DartType, bool> _modelTypes = {};
  Map<Element, List<Expression>>? _containers;
  Map<InterfaceElement, _SealedProjection?>? _sealedProjections;

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
    super.visitVariableDeclaration(node);
    _bindValue(node.declaredFragment?.element, node.initializer);
  }

  @override
  void visitAssignmentExpression(AssignmentExpression node) {
    super.visitAssignmentExpression(node);
    final target = node.leftHandSide;
    if (target is SimpleIdentifier &&
        const {'=', '??='}.contains(node.operator.lexeme)) {
      _bindValue(target.element, node.rightHandSide);
    } else {
      _escape(_sources(node.rightHandSide));
    }
  }

  @override
  void visitForEachPartsWithDeclaration(ForEachPartsWithDeclaration node) {
    final element = node.loopVariable.declaredFragment?.element;
    final sources = _iterated(_sources(node.iterable));
    _bind(element, sources);
    if (sources.isNotEmpty)
      _bindContainers(element, _iterationContainers(node.iterable));
    super.visitForEachPartsWithDeclaration(node);
  }

  @override
  void visitMethodInvocation(MethodInvocation node) {
    _callSources(node);
    if (!_bindCallback(node) &&
        !_isCoreCollectionMethod(node, const {'toList'})) {
      _escape(_sources(node.realTarget));
    }
    super.visitMethodInvocation(node);
  }

  @override
  void visitArgumentList(ArgumentList node) {
    super.visitArgumentList(node);
    for (final argument in node.arguments) {
      _escape(_sources(argument.argumentExpression));
    }
  }

  @override
  void visitReturnStatement(ReturnStatement node) {
    super.visitReturnStatement(node);
    _escape(_sources(node.expression));
  }

  @override
  void visitExpressionFunctionBody(ExpressionFunctionBody node) {
    super.visitExpressionFunctionBody(node);
    _escape(_sources(node.expression));
  }

  @override
  void visitInterpolationExpression(InterpolationExpression node) {
    super.visitInterpolationExpression(node);
    _escape(_sources(node.expression));
  }

  @override
  void visitThrowExpression(ThrowExpression node) {
    super.visitThrowExpression(node);
    _escape(_sources(node.expression));
  }

  void _escape(List<_SelectionSource> sources) {
    if (sources.isEmpty) return;
    for (final source in sources) {
      _escapeSource(source);
    }
  }

  void _escapeSource(_SelectionSource source) {
    if (_isModel(_valueType(source.type))) escapedCalls.add(source.call);
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

  @override
  void visitIsExpression(IsExpression node) {
    _recordSealedCases(_sources(node.expression), node.type.type);
    super.visitIsExpression(node);
  }

  @override
  void visitObjectPattern(ObjectPattern node) {
    _bindSealedPattern(node);
    super.visitObjectPattern(node);
  }

  @override
  void visitBinaryExpression(BinaryExpression node) {
    if (const {'==', '!='}.contains(node.operator.lexeme)) {
      final value = node.leftOperand is NullLiteral
          ? node.rightOperand
          : node.rightOperand is NullLiteral
          ? node.leftOperand
          : null;
      if (value != null) {
        for (final source in _sources(value)) _recordSealedPresence(source);
      }
    }
    super.visitBinaryExpression(node);
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
    if (a.call != b.call ||
        a.type != b.type ||
        a.path.length != b.path.length) {
      return false;
    }
    for (var i = 0; i < a.path.length; i++) {
      if (a.path[i] != b.path[i]) return false;
    }
    return true;
  }

  void _bindContainers(Element? element, List<Expression> values) {
    if (element == null || values.isEmpty) return;
    final bindings = _containers ??= {};
    final existing = bindings.putIfAbsent(element, () => []);
    for (final value in values) {
      if (!existing.contains(value)) existing.add(value);
    }
  }

  void _bindValue(Element? element, Expression? value) {
    final sources = _sources(value);
    _bind(element, sources);
    if (sources.isEmpty) return;
    _bindContainers(element, _containerRoots(value));
    if (element is! LocalVariableElement &&
        element is! FormalParameterElement) {
      _escape(sources);
    }
  }

  List<Expression> _containerRoots(Expression? expression) {
    return switch (expression) {
      ListLiteral() || SetOrMapLiteral() || RecordLiteral() => [expression!],
      SimpleIdentifier() => _containers?[expression.element] ?? const [],
      ParenthesizedExpression() => _containerRoots(expression.expression),
      AsExpression() => _containerRoots(expression.expression),
      PostfixExpression() when expression.operator.lexeme == '!' =>
        _containerRoots(expression.operand),
      ConditionalExpression() => _joinedContainers(
        expression.thenExpression,
        expression.elseExpression,
      ),
      BinaryExpression() when expression.operator.lexeme == '??' =>
        _joinedContainers(expression.leftOperand, expression.rightOperand),
      IndexExpression() => _indexedContainers(expression),
      PrefixedIdentifier() => _memberContainers(
        expression.prefix,
        expression.identifier.name,
      ),
      PropertyAccess() => _memberContainers(
        expression.realTarget,
        expression.propertyName.name,
      ),
      MethodInvocation()
          when _isCoreCollectionMethod(expression, const {'where', 'toList'}) =>
        _containerRoots(expression.realTarget),
      MethodInvocation()
          when _isCoreCollectionMethod(expression, const {
            'firstWhere',
            'lastWhere',
            'singleWhere',
          }) =>
        _iterationContainers(expression.realTarget),
      _ => const [],
    };
  }

  List<Expression> _joinedContainers(
    Expression leftExpression,
    Expression rightExpression,
  ) {
    final left = _containerRoots(leftExpression);
    final right = _containerRoots(rightExpression);
    if (left.isEmpty) return right;
    if (right.isEmpty) return left;
    return [...left, ...right];
  }

  List<Expression> _indexedContainers(IndexExpression expression) {
    final roots = _containerRoots(expression.target);
    if (roots.isEmpty) return const [];
    return [
      for (final root in roots)
        for (final value in _indexedExpressions(root, expression.index))
          ..._containerRoots(value),
    ];
  }

  List<Expression> _memberContainers(Expression receiver, String name) {
    final roots = _containerRoots(receiver);
    if (roots.isEmpty) return const [];
    return [
      for (final root in roots)
        for (final value in _containerMembers(root, name))
          ..._containerRoots(value),
    ];
  }

  List<Expression> _iterationContainers(Expression? receiver) {
    final roots = _containerRoots(receiver);
    if (roots.isEmpty) return const [];
    return [
      for (final root in roots)
        if (root is ListLiteral)
          for (final value in _elementExpressions(root.elements))
            ..._containerRoots(value)
        else if (root is SetOrMapLiteral && root.isSet)
          for (final value in _elementExpressions(root.elements))
            ..._containerRoots(value),
    ];
  }

  Iterable<CollectionElement> _flatElements(
    Iterable<CollectionElement> elements, [
    Set<Expression>? expanded,
  ]) sync* {
    for (final element in elements) {
      if (element is SpreadElement) {
        final roots = _containerRoots(element.expression);
        if (roots.isEmpty) {
          yield element;
          continue;
        }
        expanded ??= {};
        for (final root in roots) {
          if (!expanded.add(root)) {
            yield element;
            continue;
          }
          if (root is ListLiteral)
            yield* _flatElements(root.elements, expanded);
          if (root is SetOrMapLiteral)
            yield* _flatElements(root.elements, expanded);
          expanded.remove(root);
        }
      } else if (element is IfElement) {
        yield* _flatElements([
          element.thenElement,
          if (element.elseElement case final other?) other,
        ], expanded);
      } else if (element is ForElement) {
        yield* _flatElements([element.body], expanded);
      } else {
        yield element;
      }
    }
  }

  Iterable<Expression> _containerMembers(Expression root, String name) sync* {
    if (root is RecordLiteral) {
      var positional = 0;
      for (final field in root.fields) {
        final fieldName = field is RecordLiteralNamedField
            ? field.name.lexeme
            : '\$${++positional}';
        if (fieldName == name) yield field.fieldExpression;
      }
    } else if (root is ListLiteral &&
        const {'first', 'last', 'single'}.contains(name)) {
      yield* _elementExpressions(root.elements);
    } else if (root is SetOrMapLiteral) {
      if (root.isMap && const {'keys', 'values'}.contains(name)) {
        yield* _elementExpressions(root.elements, keys: name == 'keys');
      } else if (root.isSet &&
          const {'first', 'last', 'single'}.contains(name)) {
        yield* _elementExpressions(root.elements);
      }
    }
  }

  Iterable<Expression> _elementExpressions(
    Iterable<CollectionElement> elements, {
    bool keys = false,
  }) sync* {
    for (final element in _flatElements(elements)) {
      switch (element) {
        case Expression():
          yield element;
        case MapLiteralEntry():
          yield keys ? element.key : element.value;
        case SpreadElement():
          yield element.expression;
        case NullAwareElement():
          yield element.value;
        default:
          throw StateError('Cannot infer selection for collection element');
      }
    }
  }

  List<_SelectionSource> _elementSources(
    Iterable<CollectionElement> elements,
  ) => [
    for (final element in elements)
      ...switch (element) {
        Expression() => _sources(element),
        MapLiteralEntry() => [
          ..._sources(element.key),
          ..._sources(element.value),
        ],
        SpreadElement() =>
          element.expression.staticType?.isDartCoreMap == true
              ? _sources(element.expression)
              : _iterated(_sources(element.expression)),
        IfElement() => _elementSources([
          element.thenElement,
          if (element.elseElement case final other?) other,
        ]),
        ForElement() => _forElementSources(element),
        NullAwareElement() => _sources(element.value),
        _ => throw StateError('Cannot infer selection for collection element'),
      },
  ];

  List<_SelectionSource> _forElementSources(ForElement element) {
    final parts = element.forLoopParts;
    if (parts is ForEachPartsWithDeclaration) {
      final binding = parts.loopVariable.declaredFragment?.element;
      _bind(binding, _iterated(_sources(parts.iterable)));
      _bindContainers(binding, _iterationContainers(parts.iterable));
    } else if (parts is ForEachParts) {
      _escape(_sources(parts.iterable));
    }
    return _elementSources([element.body]);
  }

  List<_SelectionSource> _wrapCollection(
    List<_SelectionSource> sources,
    DartType? type,
  ) {
    if (sources.isEmpty || type is! InterfaceType || !_isCollection(type))
      return const [];
    return [
      for (final source in sources)
        _withType(
          source,
          type.element.instantiate(
            typeArguments: [source.type],
            nullabilitySuffix: type.nullabilitySuffix,
          ),
        ),
    ];
  }

  Iterable<Expression> _indexedExpressions(
    Expression root,
    Expression index,
  ) sync* {
    if (root is ListLiteral) {
      yield* _elementExpressions(root.elements);
    } else if (root is SetOrMapLiteral && root.isMap) {
      for (final element in _flatElements(root.elements)) {
        if (element is MapLiteralEntry) {
          final key = element.key;
          if (key is StringLiteral && index is StringLiteral) {
            final keyValue = key.stringValue;
            final indexValue = index.stringValue;
            if (keyValue != null &&
                indexValue != null &&
                keyValue != indexValue)
              continue;
          }
          if (key is IntegerLiteral &&
              index is IntegerLiteral &&
              key.value != index.value)
            continue;
          yield element.value;
        } else {
          yield* _elementExpressions([element]);
        }
      }
    }
  }

  List<_SelectionSource> _indexedSources(IndexExpression expression) {
    final containers = _containerRoots(expression.target);
    if (containers.isNotEmpty) {
      if (expression.target?.staticType?.isDartCoreList == true) {
        return _iterated(_sources(expression.target));
      }
      return [
        for (final root in containers)
          for (final value in _indexedExpressions(root, expression.index))
            ..._sources(value),
      ];
    }
    final sources = _sources(expression.target);
    return expression.target?.staticType?.isDartCoreMap == true
        ? _retype(sources, expression.staticType)
        : _iterated(sources);
  }

  List<_SelectionSource> _sources(Expression? expression) {
    return switch (expression) {
      SimpleIdentifier() => _bindings[expression.element] ?? const [],
      AwaitExpression() => _awaited(_sources(expression.expression)),
      ParenthesizedExpression() => _sources(expression.expression),
      AsExpression() => _castSealedSources(
        _sources(expression.expression),
        expression.type.type,
      ),
      PostfixExpression() when expression.operator.lexeme == '!' => _sources(
        expression.operand,
      ),
      ListLiteral() => _wrapCollection(
        _elementSources(expression.elements),
        expression.staticType,
      ),
      SetOrMapLiteral() =>
        expression.isMap
            ? _elementSources(expression.elements)
            : _wrapCollection(
                _elementSources(expression.elements),
                expression.staticType,
              ),
      RecordLiteral() => [
        for (final field in expression.fields)
          ..._sources(field.fieldExpression),
      ],
      IndexExpression() => _indexedSources(expression),
      PrefixedIdentifier() => _memberSources(
        expression.prefix,
        expression.identifier,
        expression,
      ),
      PropertyAccess() => _memberSources(
        expression.realTarget,
        expression.propertyName,
        expression,
      ),
      ConditionalExpression() => _joinedSources(
        expression.thenExpression,
        expression.elseExpression,
      ),
      BinaryExpression() when expression.operator.lexeme == '??' =>
        _joinedSources(expression.leftOperand, expression.rightOperand),
      MethodInvocation() => _callSources(expression),
      _ => const [],
    };
  }

  List<_SelectionSource> _joinedSources(
    Expression leftExpression,
    Expression rightExpression,
  ) {
    final left = _sources(leftExpression);
    final right = _sources(rightExpression);
    if (left.isEmpty) return right;
    if (right.isEmpty) return left;
    return [...left, ...right];
  }

  List<_SelectionSource> _callSources(MethodInvocation call) {
    final target = call.realTarget?.staticType;
    final result = call.staticType;
    if (target is! InterfaceType || result == null) return const [];
    if (_isCollection(target)) {
      if (_isCoreCollectionMethod(call, const {
        'where',
        'toList',
        'firstWhere',
        'lastWhere',
        'singleWhere',
      })) {
        return _collectionResult(_sources(call.realTarget), result);
      }
      // Exact core List/Iterable types cannot be generated clients.
      return const [];
    }
    if (_clientMethod(target, call) == null ||
        call.methodName.name.startsWith('_'))
      return const [];
    callSelections.putIfAbsent(call, FieldNode.root);
    return [
      (
        api: wireIdentifier(call.methodName.name),
        call: call,
        type: result,
        path: const <String>[],
      ),
    ];
  }

  InterfaceType? _clientType(InterfaceType type) {
    if (_isClientDeclaration(type)) return type;
    for (final parent in type.allSupertypes) {
      if (_isClientDeclaration(parent)) return parent;
    }
    return null;
  }

  List<_SelectionSource> _collectionResult(
    List<_SelectionSource> sources,
    DartType result,
  ) {
    if (sources.isEmpty) return const [];
    final collection = result is InterfaceType && _isCollection(result);
    final element = collection ? result.typeArguments.single : result;
    var exact = true;
    for (final source in sources) {
      final type = source.type;
      if (type is! InterfaceType ||
          !_isCollection(type) ||
          type.typeArguments.single != element) {
        exact = false;
        break;
      }
    }
    if (exact) return _retype(sources, result);
    final values = _iterated(sources);
    return collection ? _wrapCollection(values, result) : values;
  }

  /// The generated declaration owns selection metadata, including for overrides.
  MethodElement? apiMethod(MethodInvocation call) {
    final type = call.realTarget?.staticType;
    return type is InterfaceType ? _clientMethod(type, call) : null;
  }

  MethodElement? _clientMethod(InterfaceType type, MethodInvocation call) {
    final client = _clientType(type);
    if (client == null) return null;
    final method = call.methodName.element;
    if (method is MethodElement && method.enclosingElement == client.element)
      return method;
    final declared = client.getMethod(call.methodName.name);
    if (declared != null || clientLibrary != null) return declared;
    // Unconfigured analysis is used by isolated fixtures and legacy consumers.
    return method is MethodElement ? method : null;
  }

  bool _isClientDeclaration(InterfaceType type) =>
      type.element.name == 'LuxoClient' &&
      (clientLibrary == null || type.element.library.uri == clientLibrary);

  bool _isCoreCollectionMethod(MethodInvocation call, Set<String> names) {
    final type = call.realTarget?.staticType;
    return type != null &&
        _isCollection(type) &&
        names.contains(call.methodName.name) &&
        call.methodName.element?.library?.uri.toString() == 'dart:core';
  }

  List<_SelectionSource> _retype(
    List<_SelectionSource> sources,
    DartType? type,
  ) => type == null || sources.isEmpty
      ? const []
      : [
          for (final s in sources)
            (api: s.api, call: s.call, type: type, path: s.path),
        ];

  /// Await unwraps the source, preserving its shape through widened aliases.
  List<_SelectionSource> _awaited(List<_SelectionSource> sources) {
    if (sources.isEmpty) return const [];
    return [
      for (final source in sources)
        if (source.type case InterfaceType type
            when type.isDartAsyncFuture || type.isDartAsyncFutureOr)
          _withType(source, type.typeArguments.single)
        else
          source,
    ];
  }

  List<_SelectionSource> _memberSources(
    Expression receiver,
    SimpleIdentifier property,
    Expression expression,
  ) {
    final containers = _containerRoots(receiver);
    if (containers.isNotEmpty) {
      if (receiver.staticType case final type?
          when _isMetadata(type, property)) {
        return const [];
      }
      if (receiver.staticType case final type?
          when _isCollection(type) &&
              const {'first', 'last', 'single'}.contains(property.name)) {
        return _iterated(_sources(receiver));
      }
      final fields = [
        for (final container in containers)
          ..._containerMembers(container, property.name),
      ];
      if (fields.isNotEmpty) {
        final sources = [for (final field in fields) ..._sources(field)];
        return receiver.staticType?.isDartCoreMap == true &&
                const {'keys', 'values'}.contains(property.name)
            ? _wrapCollection(sources, expression.staticType)
            : sources;
      }
      _escape(_sources(receiver));
      return const [];
    }
    final sources = _sources(receiver);
    if (sources.isEmpty) return const [];
    final getter = property.element;
    final resolvedType =
        getter is GetterElement && getter.enclosingElement is InterfaceElement
        ? expression.staticType
        : null;
    final result = <_SelectionSource>[];
    for (final source in sources) {
      final member = _property(
        source,
        property.name,
        receiver.staticType,
        resolvedType,
      );
      if (member != null) {
        result.add(member);
      } else if (_isMetadata(source.type, property)) {
        if (const {'length', 'isEmpty', 'isNotEmpty'}.contains(property.name)) {
          _recordSealedPresence(source);
        }
      } else {
        _escapeSource(source);
      }
    }
    return result;
  }

  bool _isMetadata(DartType type, SimpleIdentifier property) {
    final getter = property.element;
    if (getter is! GetterElement) return false;
    final name = property.name;
    if (getter.library.uri.toString() == 'dart:core') {
      return const {
        'length',
        'isEmpty',
        'isNotEmpty',
        'hashCode',
        'runtimeType',
      }.contains(name);
    }
    if (type is! InterfaceType || getter.enclosingElement != type.element)
      return false;
    return (_isSDKType(type, 'Selected') && name == 'isSelected') ||
        (_isSDKType(type, 'Page') &&
            const {'total', 'page', 'pageSize'}.contains(name));
  }

  _SelectionSource? _property(
    _SelectionSource source,
    String field,
    DartType? receiverType,
    DartType? resolvedType,
  ) {
    source = _narrowSealedSource(source, receiverType) ?? source;
    final type = source.type;
    if (type is! InterfaceType) return null;
    if (receiverType is RecordType || receiverType?.isDartCoreMap == true) {
      return null;
    }
    if (_isSDKType(type, 'Selected')) {
      return field == 'value'
          ? _withType(source, type.typeArguments.single)
          : null;
    }
    if (_isCollection(type) &&
        const {'first', 'last', 'single'}.contains(field)) {
      return _withType(source, type.typeArguments.single);
    }
    // Reuse the resolved getter type only when it describes this exact source.
    final fieldType =
        (type == receiverType ? resolvedType : null) ??
        type.getGetter(field)?.returnType;
    if (fieldType == null) return null;
    if (_isSDKType(type, 'Page')) {
      return field == 'items' ? _withType(source, fieldType) : null;
    }
    final wireField = _selectionFieldName(type, field);
    final known = modelFields[wireIdentifier(type.element.name!)];
    if (known != null
        ? !known.containsKey(wireField)
        : !_isSDKType(fieldType, 'Selected')) {
      return null;
    }
    return (
      api: source.api,
      call: source.call,
      type: fieldType,
      path: [...source.path, wireField],
    );
  }

  _SelectionSource _withType(_SelectionSource source, DartType type) =>
      (api: source.api, call: source.call, type: type, path: source.path);

  bool _isSDKType(DartType type, String name) =>
      type is InterfaceType &&
      type.element.name == name &&
      type.element.library.uri.toString() ==
          'package:luxo_client/src/types.dart';

  bool _isCollection(DartType type) =>
      type.isDartCoreList || type.isDartCoreIterable || type.isDartCoreSet;

  List<_SelectionSource> _iterated(List<_SelectionSource> sources) {
    if (sources.isEmpty) return const [];
    return [
      for (final source in sources)
        if (source.type case InterfaceType type when _isCollection(type))
          _withType(source, type.typeArguments.single),
    ];
  }

  bool _bindCallback(MethodInvocation call) {
    if (_bindSubscription(call)) return true;
    if (call.argumentList.arguments.isEmpty) return false;
    final callback = _inlineCallback(
      call.argumentList.arguments.first.argumentExpression,
    );
    if (callback == null) return false;
    final parameter = callback.parameters?.parameters.firstOrNull;
    if (parameter == null) return false;
    final sources = _sources(call.realTarget);
    final values = <_SelectionSource>[];
    for (final source in sources) {
      final type = source.type;
      if (type is! InterfaceType) continue;
      final method = call.methodName.name;
      if ((type.isDartAsyncFuture &&
              method == 'then' &&
              call.methodName.element?.library?.uri.toString() ==
                  'dart:async') ||
          _isCoreCollectionMethod(call, const {
            'map',
            'where',
            'forEach',
            'any',
            'every',
            'firstWhere',
            'lastWhere',
            'singleWhere',
          })) {
        values.add(_withType(source, type.typeArguments.first));
      }
    }
    _bind(parameter.declaredFragment?.element, values);
    if (values.isNotEmpty &&
        _isCoreCollectionMethod(call, const {
          'map',
          'where',
          'forEach',
          'any',
          'every',
          'firstWhere',
          'lastWhere',
          'singleWhere',
        })) {
      _bindContainers(
        parameter.declaredFragment?.element,
        _iterationContainers(call.realTarget),
      );
    }
    final body = callback.body;
    if (body is ExpressionFunctionBody) {
      _escape(_sources(body.expression));
    }
    return values.isNotEmpty;
  }

  bool _bindSubscription(MethodInvocation call) {
    if (!callSelections.containsKey(call)) return false;
    for (final argument in call.argumentList.arguments) {
      if (argument.correspondingParameter?.name != 'onData') continue;
      final callback = _inlineCallback(argument.argumentExpression);
      if (callback == null) return false;
      final element = callback
          .parameters
          ?.parameters
          .firstOrNull
          ?.declaredFragment
          ?.element;
      if (element is! FormalParameterElement) return false;
      _bind(element, [
        (
          api: wireIdentifier(call.methodName.name),
          call: call,
          type: element.type,
          path: const <String>[],
        ),
      ]);
      return true;
    }
    return false;
  }

  /// Syntax wrappers do not change the callback's resolved parameter identity.
  FunctionExpression? _inlineCallback(Expression expression) {
    while (true) {
      switch (expression) {
        case FunctionExpression():
          return expression;
        case ParenthesizedExpression():
          expression = expression.expression;
        case AsExpression():
          expression = expression.expression;
        case PostfixExpression() when expression.operator.lexeme == '!':
          expression = expression.operand;
        default:
          return null;
      }
    }
  }

  /// Only terminal schema fields form a valid selection, never wrapper members.
  void _record(List<_SelectionSource> sources) {
    if (sources.isEmpty) return;
    for (final source in sources) {
      if (source.path.isEmpty || _isModel(_valueType(source.type))) continue;
      var node = hints.putIfAbsent(source.api, FieldNode.root);
      var callNode = callSelections.putIfAbsent(source.call, FieldNode.root);
      for (final field in source.path) {
        node = node.addChild(field);
        callNode = callNode.addChild(field);
      }
    }
  }

  DartType _valueType(DartType type) {
    while (type is InterfaceType &&
        (_isSDKType(type, 'Selected') ||
            _isSDKType(type, 'Page') ||
            _isCollection(type) ||
            type.isDartAsyncFuture)) {
      type = type.typeArguments.single;
    }
    return type;
  }

  bool _isModel(DartType type) => _modelTypes.putIfAbsent(
    type,
    () =>
        type is InterfaceType &&
        (modelFields.containsKey(wireIdentifier(type.element.name!)) ||
            type.getters.any(
              (getter) => _isSDKType(getter.returnType, 'Selected'),
            ) ||
            _isSealedSelectable(type)),
  );
}

typedef _SelectionSource = ({
  String api,
  MethodInvocation call,
  DartType type,
  List<String> path,
});

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
