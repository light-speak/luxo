import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/element/type.dart';

/// Analyzes Dart source AST to find LuxoClient API calls and trace
/// which fields are accessed on the returned objects.
///
/// Result: map of API name → set of accessed field names.
///
/// Example:
///   final user = await client.getUser(1);
///   print(user.name);    // ← traces "name"
///   print(user.email);   // ← traces "email"
///   → {"getUser": {"name", "email"}}
class SelectAnalyzer extends RecursiveAstVisitor<void> {
  /// API name → accessed fields
  final Map<String, Set<String>> hints = {};

  /// Variable name → API name (tracks which var came from which API call)
  final Map<String, String> _varToAPI = {};

  /// Variable name → type name (for model field validation)
  final Map<String, String> _varToType = {};

  /// Known model field names (type → field names), populated from generated types
  final Map<String, Set<String>> modelFields;

  SelectAnalyzer(this.modelFields);

  @override
  void visitVariableDeclarationStatement(VariableDeclarationStatement node) {
    for (final decl in node.variables.variables) {
      final init = decl.initializer;
      if (init == null) continue;

      // Unwrap await: `final user = await client.getUser(1)`
      final expr = init is AwaitExpression ? init.expression : init;

      final apiName = _extractAPICall(expr);
      if (apiName != null) {
        final varName = decl.name.lexeme;
        _varToAPI[varName] = apiName;
        hints.putIfAbsent(apiName, () => {});

        // Try to get the return type name
        final typeName = _extractReturnType(expr);
        if (typeName != null) {
          _varToType[varName] = typeName;
        }
      }
    }
    super.visitVariableDeclarationStatement(node);
  }

  @override
  void visitPrefixedIdentifier(PrefixedIdentifier node) {
    // user.name → prefix=user, identifier=name
    final varName = node.prefix.name;
    final field = node.identifier.name;
    _recordAccess(varName, field);
    super.visitPrefixedIdentifier(node);
  }

  @override
  void visitPropertyAccess(PropertyAccess node) {
    // user.name (when target is complex expression)
    final target = node.target;
    if (target is SimpleIdentifier) {
      _recordAccess(target.name, node.propertyName.name);
    }
    super.visitPropertyAccess(node);
  }

  @override
  void visitIndexExpression(IndexExpression node) {
    // items[0].name — trace the items variable
    super.visitIndexExpression(node);
  }

  void _recordAccess(String varName, String fieldName) {
    final apiName = _varToAPI[varName];
    if (apiName == null) return;

    // Validate: is this actually a model field?
    final typeName = _varToType[varName];
    if (typeName != null && modelFields.containsKey(typeName)) {
      if (!modelFields[typeName]!.contains(fieldName)) return; // not a model field
    }

    hints[apiName]?.add(fieldName);
  }

  /// Extracts API name from a method call on LuxoClient.
  /// Matches: client.getUser(...), client.listPosts(...), etc.
  String? _extractAPICall(Expression expr) {
    if (expr is! MethodInvocation) return null;
    final target = expr.target;
    if (target == null) return null;

    // Check if target's static type is or extends LuxoClient
    final targetType = target.staticType;
    if (targetType == null) return null;

    // Check class name contains "LuxoClient" or "Client"
    // (generated client class is always named LuxoClient)
    if (targetType is InterfaceType) {
      final className = targetType.element.name;
      if (!className.contains('LuxoClient') && !className.contains('Client')) {
        return null;
      }
    }

    return expr.methodName.name;
  }

  /// Tries to extract the return model type name from the method call.
  String? _extractReturnType(Expression expr) {
    if (expr is! MethodInvocation) return null;
    final returnType = expr.staticType;
    if (returnType == null) return null;

    // Unwrap Future<T> → T
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
