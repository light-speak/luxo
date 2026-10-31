part of 'codegen.dart';

String _enumWireValue(String type, String? name, String value) =>
    type == 'Enum' && name != null ? '$value.rawValue' : value;

void _validateEnums(LuxoSchema schema) {
  _validateEnumDeclarations(schema);
  for (final model in schema.models.values) {
    _validateEnumFields(schema, model.name, model.fields);
  }
  for (final type in schema.types.values) {
    _validateEnumFields(schema, type.name, type.fields);
  }
  for (final api in schema.apis.values) {
    _validateReturnType(schema, api);
    for (final param in api.params) {
      _validateEnumReference(
        schema,
        param.type,
        param.typeName,
        '${api.name}.${param.name}',
      );
    }
  }
}

void _validateReturnType(LuxoSchema schema, LuxoAPI api) {
  final type = api.returnType;
  if (type == 'Enum') {
    throw FormatException('API ${api.name} requires a named enum return type');
  }
  if (type != null &&
      !_isScalar(type) &&
      !schema.enums.containsKey(type) &&
      !schema.models.containsKey(type) &&
      !schema.types.containsKey(type)) {
    throw FormatException(
      'API ${api.name} has an undeclared return type $type',
    );
  }
}

void _validateEnumFields(
  LuxoSchema schema,
  String owner,
  List<LuxoField> fields,
) {
  for (final field in fields) {
    _validateEnumReference(
      schema,
      field.type,
      field.typeName,
      '$owner.${field.name}',
    );
  }
}

void _validateEnumReference(
  LuxoSchema schema,
  String type,
  String? name,
  String path,
) {
  if (type == 'Enum' && !schema.enums.containsKey(name)) {
    throw FormatException('$path requires a declared enum type');
  }
}

void _validateEnumDeclarations(LuxoSchema schema) {
  final names = {
    ...schema.models.keys,
    ...schema.types.keys,
    ...schema.enums.keys,
  };
  for (final entry in schema.enums.entries) {
    final declaration = entry.value;
    final name = declaration.name;
    if (entry.key != name) {
      throw FormatException(
        'enum key ${entry.key} does not match its name $name',
      );
    }
    if (declaration.values.isEmpty ||
        declaration.values.toSet().length != declaration.values.length) {
      throw FormatException('enum $name requires distinct members');
    }
    for (final suffix in ['Known', 'Unknown']) {
      if (!names.add('$name$suffix')) {
        throw FormatException(
          'generated enum type $name$suffix conflicts with Schema',
        );
      }
    }
  }
}

void _writeEnums(StringBuffer b, LuxoSchema schema) {
  for (final declaration in schema.enums.values) {
    _writeEnumBase(b, declaration);
    _writeEnumVariants(b, declaration);
  }
}

void _writeEnumBase(StringBuffer b, LuxoEnum declaration) {
  final name = declaration.name;
  b.writeln('sealed class $name {');
  b.writeln('  const $name();');
  b.writeln('  String get rawValue;');
  b.writeln('  factory $name.fromValue(String value) => switch (value) {');
  for (final member in declaration.values) {
    b.writeln(
      '    ${jsonEncode(member)} => ${name}Known.${_enumMember(member, name)},',
    );
  }
  b.writeln('    _ => ${name}Unknown._(value),\n  };');
  b.writeln('  static $name? \$readNullable(LuxoDecoder dec) {');
  b.writeln('    final value = dec.readStringPtr();');
  b.writeln('    return value == null ? null : $name.fromValue(value);\n  }');
  _writeEnumListHelpers(b, name);
  b.writeln('}\n');
}

void _writeEnumListHelpers(StringBuffer b, String name) {
  b.writeln(
    '  static List<$name> \$readArray(LuxoDecoder dec, {bool complete = false}) {',
  );
  b.writeln('    final count = dec.readArrayCount();');
  b.writeln('    final values = <$name>[];');
  b.writeln('    for (var i = 0; i < count; i++) {');
  b.writeln('      final value = dec.readString();');
  b.writeln('      if (dec.error != null) throw FormatException(dec.error!);');
  b.writeln('      values.add($name.fromValue(value));\n    }');
  b.writeln('    if (complete) dec.checkComplete();');
  b.writeln('    return values;\n  }');
  b.writeln('  static List<$name> \$fromJSONList(Object? value) {');
  b.writeln('    final items = value as List;');
  b.writeln(
    '    return List<$name>.generate(items.length, (i) => $name.fromValue(luxoStringFromJSON(items[i])));\n  }',
  );
  b.writeln('  static List<String> \$rawList(List<$name> values) =>');
  b.writeln(
    '    List<String>.generate(values.length, (index) => values[index].rawValue);',
  );
  b.writeln('  static List<String>? \$nullableRawList(List<$name>? values) =>');
  b.writeln('    values == null ? null : \$rawList(values);');
}

void _writeEnumVariants(StringBuffer b, LuxoEnum declaration) {
  final name = declaration.name;
  b.writeln('enum ${name}Known implements $name {');
  b.writeln(
    declaration.values
            .map(
              (member) =>
                  '  ${_enumMember(member, name)}(${jsonEncode(member)})',
            )
            .join(',\n') +
        ';',
  );
  b.writeln('  const ${name}Known(this.rawValue);');
  b.writeln('  @override\n  final String rawValue;\n}\n');
  b.writeln('final class ${name}Unknown extends $name {');
  b.writeln('  const ${name}Unknown._(this.rawValue);');
  b.writeln('  @override\n  final String rawValue;');
  b.writeln(
    '  @override\n  bool operator ==(Object other) => other is ${name}Unknown && other.rawValue == rawValue;',
  );
  b.writeln('  @override\n  int get hashCode => rawValue.hashCode;\n}\n');
}

String _enumMember(String member, String name) {
  // '$' is unavailable in DSL names and prevents escaping from colliding.
  if (member.startsWith('_')) return 'value\$$member';
  if (_enumReserved.contains(member) || member == '${name}Known')
    return '$member\$';
  return member;
}

const _enumReserved = {
  'abstract',
  'as',
  'assert',
  'async',
  'await',
  'base',
  'break',
  'case',
  'catch',
  'class',
  'const',
  'continue',
  'covariant',
  'default',
  'deferred',
  'do',
  'dynamic',
  'else',
  'enum',
  'export',
  'extends',
  'extension',
  'external',
  'factory',
  'false',
  'final',
  'finally',
  'for',
  'Function',
  'get',
  'hide',
  'if',
  'implements',
  'import',
  'in',
  'interface',
  'is',
  'late',
  'library',
  'mixin',
  'new',
  'null',
  'of',
  'on',
  'operator',
  'part',
  'required',
  'rethrow',
  'return',
  'sealed',
  'set',
  'show',
  'static',
  'super',
  'switch',
  'sync',
  'this',
  'throw',
  'true',
  'try',
  'type',
  'typedef',
  'var',
  'void',
  'when',
  'with',
  'while',
  'yield',
  'values',
  'index',
  'hashCode',
  'rawValue',
  'runtimeType',
  'toString',
  'noSuchMethod',
};
