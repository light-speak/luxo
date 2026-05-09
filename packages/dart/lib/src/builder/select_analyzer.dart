import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
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

  /// Variable name → API name
  final Map<String, String> _varToAPI = {};

  /// Variable name → type name
  final Map<String, String> _varToType = {};

  /// Known model fields: type → { fieldName → fieldTypeName }
  final Map<String, Map<String, String>> modelFields;

  SelectAnalyzer(this.modelFields);

  /// Build the $select string from collected hints.
  Map<String, String> buildSelectStrings() {
    final result = <String, String>{};
    for (final entry in hints.entries) {
      final selectStr = entry.value.toSelectString();
      if (selectStr.isNotEmpty) {
        result[entry.key] = selectStr;
      }
    }
    return result;
  }

  @override
  void visitVariableDeclarationStatement(VariableDeclarationStatement node) {
    for (final decl in node.variables.variables) {
      final init = decl.initializer;
      if (init == null) continue;

      final expr = init is AwaitExpression ? init.expression : init;

      final apiName = _extractAPICall(expr);
      if (apiName != null) {
        final varName = decl.name.lexeme;
        _varToAPI[varName] = apiName;
        hints.putIfAbsent(apiName, () => FieldNode.root());

        final typeName = _extractReturnType(expr);
        if (typeName != null) {
          _varToType[varName] = typeName;
        }
      }
    }
    super.visitVariableDeclarationStatement(node);
  }

  @override
  void visitPropertyAccess(PropertyAccess node) {
    _handlePropertyChain(node);
    super.visitPropertyAccess(node);
  }

  @override
  void visitPrefixedIdentifier(PrefixedIdentifier node) {
    final varName = node.prefix.name;
    final field = node.identifier.name;
    _recordFieldAccess(varName, [field]);
    super.visitPrefixedIdentifier(node);
  }

  /// Handle nested property chains: post.user.name, post.comments[0].user.id
  void _handlePropertyChain(PropertyAccess node) {
    final chain = <String>[];
    Expression? current = node;

    // Walk up the property chain to collect field names
    while (current is PropertyAccess) {
      chain.insert(0, current.propertyName.name);
      current = current.target;
    }

    // Handle index access: items[0].field
    if (current is IndexExpression) {
      current = current.target;
    }

    // Handle prefix: post.field
    if (current is PrefixedIdentifier) {
      chain.insert(0, current.identifier.name);
      final varName = current.prefix.name;
      _recordFieldAccess(varName, chain);
    } else if (current is SimpleIdentifier) {
      final varName = current.name;
      _recordFieldAccess(varName, chain);
    }
  }

  /// Record a field access chain: ["comments", "user", "name"]
  /// Generates nested $select: "comments{user{name}}"
  void _recordFieldAccess(String varName, List<String> chain) {
    final apiName = _varToAPI[varName];
    if (apiName == null || chain.isEmpty) return;

    final root = hints[apiName];
    if (root == null) return;

    // Walk the chain, validating each level against model fields
    var currentType = _varToType[varName];
    var currentNode = root;

    for (final field in chain) {
      // Validate this field exists on the current type
      if (currentType != null && modelFields.containsKey(currentType)) {
        final fields = modelFields[currentType]!;
        if (!fields.containsKey(field)) break; // not a model field, stop

        // Add to tree
        currentNode = currentNode.addChild(field);

        // Resolve next type for deeper traversal
        currentType = fields[field];
      } else {
        // Unknown type — just add field without validation
        currentNode = currentNode.addChild(field);
        currentType = null;
      }
    }
  }

  String? _extractAPICall(Expression expr) {
    if (expr is! MethodInvocation) return null;
    final target = expr.target;
    if (target == null) return null;

    final targetType = target.staticType;
    if (targetType == null) return null;

    if (targetType is InterfaceType) {
      final className = targetType.element.name;
      if (!className.contains('LuxoClient') && !className.contains('Client')) {
        return null;
      }
    }

    return expr.methodName.name;
  }

  String? _extractReturnType(Expression expr) {
    if (expr is! MethodInvocation) return null;
    final returnType = expr.staticType;
    if (returnType == null) return null;

    if (returnType is InterfaceType && returnType.isDartAsyncFuture) {
      final typeArgs = returnType.typeArguments;
      if (typeArgs.isNotEmpty) {
        final inner = typeArgs.first;
        if (inner is InterfaceType) {
          return inner.element.name;
        }
      }
    }

    if (returnType is InterfaceType) {
      return returnType.element.name;
    }
    return null;
  }
}

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

  /// Build $select string: "name,email,comments{content,user{id}}"
  String toSelectString() {
    if (children.isEmpty) return '';
    final parts = <String>[];
    for (final child in children.values) {
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
