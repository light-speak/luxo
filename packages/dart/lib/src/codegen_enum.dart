part of 'codegen.dart';

String _enumWireValue(String type, String? name, String value) =>
    type == 'Enum' && name != null ? '$value.rawValue' : value;

void _validateSchemaReferences(LuxoSchema schema) {
  _validateEnumDeclarations(schema);
  _validateSealedDeclarations(schema);
  for (final entry in schema.models.entries) {
    final model = entry.value;
    _validateDeclarationName(entry.key, model.name);
    if (schema.types.containsKey(entry.key) ||
        schema.enums.containsKey(entry.key) ||
        schema.sealeds.containsKey(entry.key)) {
      throw FormatException('duplicate type ${entry.key}');
    }
    _validateFieldReferences(schema, model.name, model.fields);
  }
  for (final entry in schema.types.entries) {
    final type = entry.value;
    _validateDeclarationName(entry.key, type.name);
    if (schema.enums.containsKey(entry.key) ||
        schema.sealeds.containsKey(entry.key)) {
      throw FormatException('duplicate type ${entry.key}');
    }
    _validateFieldReferences(schema, type.name, type.fields);
  }
  for (final entry in schema.apis.entries) {
    final api = entry.value;
    _validateDeclarationName(entry.key, api.name, internal: api.internal);
    _validateReturnType(schema, api);
    final names = <String>{};
    for (final param in api.params) {
      _validateMemberName(param.name, names);
      _validateTypeReference(
        schema,
        param.type,
        param.typeName,
        '${api.name}.${param.name}',
      );
    }
  }
}

void _validateDeclarationName(
  String key,
  String name, {
  bool internal = false,
}) {
  if (name.isEmpty)
    throw const FormatException('declaration name must not be empty');
  if (!internal && !isDSLIdentifier(name))
    throw FormatException('invalid declaration identifier: $name');
  if (key != name) {
    throw FormatException('declaration name $name does not match key $key');
  }
}

void _validateMemberName(String name, Set<String> names) {
  if (name.isEmpty)
    throw const FormatException('member name must not be empty');
  if (!isDSLIdentifier(name))
    throw FormatException('invalid member identifier: $name');
  if (!names.add(name)) throw FormatException('duplicate name $name');
}

void _validateReturnType(LuxoSchema schema, LuxoAPI api) {
  final type = api.returnType;
  if (type == 'Enum') {
    throw FormatException('API ${api.name} requires a named enum return type');
  }
  if (type == null ||
      (!_isScalar(type) &&
          !schema.enums.containsKey(type) &&
          !schema.models.containsKey(type) &&
          !schema.types.containsKey(type) &&
          !schema.sealeds.containsKey(type))) {
    throw FormatException(
      'API ${api.name} has an undeclared return type $type',
    );
  }
}

void _validateFieldReferences(
  LuxoSchema schema,
  String owner,
  List<LuxoField> fields,
) {
  final names = <String>{};
  for (final field in fields) {
    _validateMemberName(field.name, names);
    _validateTypeReference(
      schema,
      field.type,
      field.typeName,
      '$owner.${field.name}',
    );
  }
}

void _validateTypeReference(
  LuxoSchema schema,
  String type,
  String? name,
  String path,
) {
  if (type == 'Sealed' && !schema.sealeds.containsKey(name)) {
    throw FormatException('$path requires a declared sealed type');
  }
  if (type == 'Enum' && !schema.enums.containsKey(name)) {
    throw FormatException('$path requires a declared enum type');
  }
  if (type == 'Model' &&
      !schema.models.containsKey(name) &&
      !schema.types.containsKey(name)) {
    throw FormatException('$path requires a declared model or type');
  }
  if (type != 'Model' &&
      type != 'Enum' &&
      type != 'Sealed' &&
      !_isScalar(type) &&
      !schema.models.containsKey(type) &&
      !schema.types.containsKey(type) &&
      !schema.enums.containsKey(type) &&
      !schema.sealeds.containsKey(type)) {
    throw FormatException('$path has an undeclared type $type');
  }
}

void _validateEnumDeclarations(LuxoSchema schema) {
  final names = {
    ...schema.models.keys,
    ...schema.types.keys,
    ...schema.enums.keys,
    ...schema.sealeds.keys,
  };
  for (final entry in schema.enums.entries) {
    final declaration = entry.value;
    final name = declaration.name;
    _validateDeclarationName(entry.key, name);
    if (declaration.values.toSet().length != declaration.values.length) {
      throw FormatException('enum $name requires distinct members');
    }
    for (final member in declaration.values) {
      if (!isDSLIdentifier(member))
        throw FormatException('invalid enum identifier: $member');
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
  final name = dartTypeIdentifier(declaration.name);
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
  final name = dartTypeIdentifier(declaration.name);
  if (declaration.values.isEmpty) {
    b.writeln('abstract final class ${name}Known implements $name {');
    b.writeln('  static const List<${name}Known> values = [];\n}\n');
  } else {
    _writeKnownEnum(b, declaration, name);
  }
  b.writeln('final class ${name}Unknown extends $name {');
  b.writeln('  const ${name}Unknown._(this.rawValue);');
  b.writeln('  @override\n  final String rawValue;');
  b.writeln(
    '  @override\n  bool operator ==(Object other) => other is ${name}Unknown && other.rawValue == rawValue;',
  );
  b.writeln('  @override\n  int get hashCode => rawValue.hashCode;\n}\n');
}

void _writeKnownEnum(StringBuffer b, LuxoEnum declaration, String name) {
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
}

String _enumMember(String member, String name) {
  member = dartSourceIdentifier(member);
  // '$' is unavailable in DSL names and prevents escaping from colliding.
  if (member.startsWith('_')) return 'value\$$member';
  if (_enumReserved.contains(member) || member == '${name}Known')
    return '$member\$';
  return dartSourceIdentifier(member);
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
