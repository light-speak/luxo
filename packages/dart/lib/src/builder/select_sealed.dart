part of 'select_analyzer.dart';

typedef _SealedProjection = ({
  String owner,
  String? variant,
  List<String> cases,
});

extension _SealedSelection on SelectAnalyzer {
  bool _isSealedSelectable(InterfaceType type) {
    final projection = _sealedProjection(type);
    return projection != null &&
        (projection.variant != null || projection.cases.isNotEmpty);
  }

  String _selectionFieldName(InterfaceType type, String field) {
    if (_sealedProjection(type)?.variant == null) return wireIdentifier(field);
    final getter = type.getGetter(field);
    if (getter != null) {
      for (final annotation in getter.variable.metadata.annotations) {
        final value = annotation.computeConstantValue();
        final type = value?.type;
        if (type is InterfaceType &&
            type.element.name == 'LuxoSelectionFieldProjection' &&
            type.element.library.uri.toString() ==
                'package:luxo_client/src/types.dart') {
          final name = value!.getField('name')?.toStringValue();
          if (name != null) return name;
        }
      }
    }
    return wireIdentifier(field);
  }

  _SealedProjection? _sealedProjection(InterfaceType type) {
    if (type.element.library.uri.scheme == 'dart' ||
        type.element.metadata.annotations.isEmpty)
      return null;
    final cache = _sealedProjections ??= {};
    return cache.putIfAbsent(type.element, () {
      for (final annotation in type.element.metadata.annotations) {
        final value = annotation.computeConstantValue();
        final annotationType = value?.type;
        if (annotationType is! InterfaceType ||
            annotationType.element.library.uri.toString() !=
                'package:luxo_client/src/types.dart')
          continue;
        final name = annotationType.element.name;
        if (name != 'LuxoSealedProjection' &&
            name != 'LuxoSealedCaseProjection')
          continue;
        final owner = value!.getField('owner')?.toStringValue();
        if (owner == null) continue;
        return (
          owner: owner,
          variant: value.getField('variant')?.toStringValue(),
          cases: [
            for (final item in value.getField('variants')?.toListValue() ?? [])
              if (item.toStringValue() case final String name) name,
          ],
        );
      }
      return null;
    });
  }

  _SelectionSource? _narrowSealedSource(
    _SelectionSource source,
    DartType? target,
  ) {
    final original = source.type;
    if (original is! InterfaceType || target is! InterfaceType) return null;
    final from = _sealedProjection(original);
    final to = _sealedProjection(target);
    if (from == null || to?.variant == null || from.owner != to!.owner)
      return null;
    if (original.element == target.element) return _withType(source, target);
    if (from.variant != null ||
        !target.allSupertypes.any((type) => type.element == original.element))
      return null;
    return (
      api: source.api,
      call: source.call,
      type: target,
      path: [...source.path, to.variant!],
    );
  }

  List<_SelectionSource> _castSealedSources(
    List<_SelectionSource> sources,
    DartType? target,
  ) => [
    for (final source in sources) _narrowSealedSource(source, target) ?? source,
  ];

  void _recordSealedCases(List<_SelectionSource> sources, DartType? target) {
    for (final source in sources) {
      final narrowed = _narrowSealedSource(source, target);
      if (narrowed == null || narrowed.path.isEmpty) continue;
      _recordSealedPath(source, narrowed.path);
    }
  }

  void _recordSealedPath(_SelectionSource source, List<String> path) {
    var api = hints.putIfAbsent(source.api, FieldNode.root);
    var call = callSelections.putIfAbsent(source.call, FieldNode.root);
    for (final field in path) {
      api = api.addChild(field);
      call = call.addChild(field);
    }
  }

  void _recordSealedPresence(_SelectionSource source) {
    final type = _valueType(source.type);
    if (type is! InterfaceType) return;
    final projection = _sealedProjection(type);
    if (projection == null) return;
    if (projection.variant != null || projection.cases.isEmpty) {
      if (source.path.isNotEmpty) _recordSealedPath(source, source.path);
    } else {
      for (final name in projection.cases) {
        _recordSealedPath(source, [...source.path, name]);
      }
    }
  }

  List<_SelectionSource> _patternSources(ObjectPattern node) {
    AstNode? ancestor = node.parent;
    while (ancestor != null) {
      switch (ancestor) {
        case SwitchStatement():
          return _sources(ancestor.expression);
        case SwitchExpression():
          return _sources(ancestor.expression);
        case IfStatement():
          return _sources(ancestor.expression);
        case PatternVariableDeclaration():
          return _sources(ancestor.expression);
        case ObjectPattern():
          return const [];
      }
      ancestor = ancestor.parent;
    }
    return const [];
  }

  void _bindSealedPattern(ObjectPattern node) {
    final sources = _patternSources(node);
    if (sources.isEmpty) return;
    final type = node.type.type;
    _recordSealedCases(sources, type);
    for (final source in sources) {
      final narrowed = _narrowSealedSource(source, type);
      if (narrowed == null) continue;
      for (final field in node.fields) {
        final name = field.effectiveName;
        final getter = field.element;
        if (name == null || getter is! GetterElement) {
          _escapeSource(narrowed);
          continue;
        }
        final member = _property(narrowed, name, type, getter.returnType);
        if (member != null) {
          _bindProjectionPattern(field.pattern, member);
        } else {
          _escapeSource(narrowed);
        }
      }
    }
  }

  void _bindProjectionPattern(DartPattern pattern, _SelectionSource source) {
    _record([source]);
    switch (pattern) {
      case DeclaredVariablePattern():
        _bind(pattern.declaredFragment?.element, [source]);
      case ObjectPattern():
        final narrowed =
            _narrowSealedSource(source, pattern.type.type) ?? source;
        _recordSealedCases([source], pattern.type.type);
        for (final field in pattern.fields) {
          final getter = field.element;
          final name = field.effectiveName;
          final member = name != null && getter is GetterElement
              ? _property(narrowed, name, pattern.type.type, getter.returnType)
              : null;
          if (member == null) {
            _escapeSource(narrowed);
          } else {
            _bindProjectionPattern(field.pattern, member);
          }
        }
      case ParenthesizedPattern():
        _bindProjectionPattern(pattern.pattern, source);
      case CastPattern():
        _bindProjectionPattern(
          pattern.pattern,
          _narrowSealedSource(source, pattern.type.type) ?? source,
        );
      case NullAssertPattern():
        _bindProjectionPattern(pattern.pattern, source);
      case NullCheckPattern():
        _bindProjectionPattern(pattern.pattern, source);
      case WildcardPattern() || ConstantPattern() || RelationalPattern():
        break;
      default:
        _escapeSource(source);
    }
  }
}
