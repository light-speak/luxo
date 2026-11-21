import 'dart:convert';
import 'dart:io';

import 'types.dart';
import 'json.dart';
import 'identifier.dart';

part 'codegen_enum.dart';
part 'codegen_sealed.dart';
part 'codegen_request.dart';
part 'codegen_query.dart';

/// Generate typed client from Luxo schema introspection.
///
/// Usage:
///   dart run luxo_client:generate --endpoint http://localhost:4000/luvia --key YOUR_KEY --out lib/src/luxo/
Future<void> generate({
  required String endpoint,
  required String key,
  String outDir = 'lib/src/luxo',
}) async {
  final client = HttpClient();
  try {
    final base = Uri.parse(endpoint);
    final uri = base.replace(
      queryParameters: {
        ...base.queryParametersAll,
        r'$schema': [''],
      },
      fragment: '',
    );
    final request = await client.getUrl(uri);
    request.followRedirects = false;
    request.headers.set('X-Introspection-Key', key);
    final response = await request.close();
    if (response.statusCode != 200) {
      throw Exception('introspection failed: HTTP ${response.statusCode}');
    }
    final body = await response.transform(utf8.decoder).join();

    final Map<String, dynamic> json;
    try {
      json = decodeLuxoJSON(body) as Map<String, dynamic>;
    } catch (e) {
      throw Exception('introspection returned invalid JSON: $e');
    }

    final schema = LuxoSchema.fromJson(json);
    schema.validateIDs();
    _validateSchemaReferences(schema);
    final queries = generateQueryBuilders(schema);

    final dir = Directory(outDir);
    if (!dir.existsSync()) dir.createSync(recursive: true);

    File('$outDir/types.dart').writeAsStringSync(_genTypes(schema));
    File('$outDir/schema.dart').writeAsStringSync(_genSchemaMap(schema));
    File('$outDir/client.dart').writeAsStringSync(_genClient(schema));
    File('$outDir/queries.dart').writeAsStringSync(queries);
    print('[luxo] Generated types -> $outDir/');
  } finally {
    client.close();
  }
}

String _luxoTypeToDart(String type) {
  switch (type) {
    case 'Int':
    case 'Duration':
      return 'LuxoInt64';
    case 'Float':
      return 'double';
    case 'DateTime':
    case 'String':
    case 'UUID':
    case 'Decimal':
    case 'Enum':
      return 'String';
    case 'Boolean':
      return 'bool';
    case 'Bytes':
      return 'Uint8List';
    case 'JSON':
      return 'Object';
    default:
      return dartTypeIdentifier(type); // model reference
  }
}

bool _isScalar(String type) => const {
  'Int',
  'Float',
  'String',
  'Boolean',
  'DateTime',
  'Duration',
  'UUID',
  'Bytes',
  'Decimal',
  'Enum',
  'JSON',
}.contains(type);

String _dartParamType(LuxoParam param, _TypeUsages usages) {
  final schemaType = param.typeName ?? param.type;
  final type = usages.containsKey(schemaType)
      ? _inputTypeName(schemaType, usages)
      : param.type == 'Enum' && param.typeName != null
      ? dartTypeIdentifier(param.typeName!)
      : _luxoTypeToDart(param.type);
  return param.isList ? _dartListType(param.type, type) : type;
}

// A JSON array element may be the JSON null literal, independent of list nullability.
String _dartListType(String schemaType, String elementType) =>
    'List<${schemaType == 'JSON' ? 'Object?' : elementType}>';

/// Dart field type for a model field, wrapping list fields in List<T>.
/// Scalar lists use the Dart scalar element type; relation/nested-model lists
/// use the model type name as the element.
String _dartFieldType(
  LuxoField f,
  LuxoSchema schema, {
  required _TypeUsages usages,
  bool input = false,
}) {
  final typeName = f.typeName ?? f.type;
  final structured =
      schema.models.containsKey(typeName) ||
      schema.types.containsKey(typeName) ||
      schema.sealeds.containsKey(typeName);
  final base = structured && input
      ? _inputTypeName(typeName, usages)
      : f.type == 'Enum' && f.typeName != null
      ? dartTypeIdentifier(f.typeName!)
      : f.type == 'Model' || structured
      ? dartTypeIdentifier(typeName)
      : _luxoTypeToDart(f.type);
  if (f.isList) return _dartListType(f.type, base);
  return base;
}

// JSON null is a value even when the containing Schema field is required.
String _dartFieldValueType(
  LuxoField field,
  LuxoSchema schema, {
  required _TypeUsages usages,
  bool input = false,
}) {
  final base = _dartFieldType(field, schema, usages: usages, input: input);
  return field.nullable || (field.type == 'JSON' && !field.isList)
      ? '$base?'
      : base;
}

bool _hasInputUsage(TypeUsage usage) =>
    usage == TypeUsage.input || usage == TypeUsage.inputOutput;

bool _hasOutputUsage(TypeUsage usage) =>
    usage == TypeUsage.output || usage == TypeUsage.inputOutput;

// Only generation keeps this catalog; emitted codecs contain no name lookup.
final class _TypeUsages {
  final Map<String, TypeUsage> _states;
  final Map<String, String> inputAliases;
  _TypeUsages(this._states, this.inputAliases);
  TypeUsage? operator [](String name) => _states[name];
  bool containsKey(String name) => _states.containsKey(name);
  Iterable<TypeUsage> get values => _states.values;
}

Map<String, String> _planInputAliases(
  LuxoSchema schema,
  Map<String, TypeUsage> states,
) {
  List<String>? collisions;
  for (final entry in states.entries) {
    if (entry.value != TypeUsage.inputOutput) continue;
    final preferred = '${entry.key}Input';
    if ((states[preferred] ?? TypeUsage.unused) != TypeUsage.unused ||
        schema.enums.containsKey(preferred)) {
      (collisions ??= []).add(entry.key);
    }
  }
  if (collisions == null) return const {};
  final reserved = <String>{...schema.enums.keys};
  for (final entry in states.entries) {
    if (entry.value != TypeUsage.unused) reserved.add(entry.key);
    if (entry.value == TypeUsage.inputOutput) reserved.add('${entry.key}Input');
  }
  final aliases = <String, String>{};
  collisions.sort();
  for (final name in collisions) {
    final fields =
        schema.models[name]?.fields ??
        schema.types[name]?.fields ??
        const <LuxoField>[];
    final members = {for (final field in fields) field.name};
    final base = 'Luxo${name}Input';
    var candidate = base;
    var ordinal = 2;
    while (reserved.contains(candidate) || members.contains(candidate)) {
      candidate = '$base${ordinal++}';
    }
    reserved.add(candidate);
    aliases[name] = candidate;
  }
  return aliases;
}

String _inputTypeName(String name, _TypeUsages usages) =>
    dartTypeIdentifier(usages.inputAliases[name] ?? name) +
    (usages.inputAliases.containsKey(name) ||
            usages[name] != TypeUsage.inputOutput
        ? ''
        : 'Input');

TypeUsage _mergeUsage(TypeUsage current, TypeUsage next) {
  if (current == TypeUsage.unused) return next;
  if (current == next || current == TypeUsage.inputOutput) return current;
  return TypeUsage.inputOutput;
}

_TypeUsages _inferTypeUsages(LuxoSchema schema) {
  final fields = <String, List<LuxoField>>{
    for (final model in schema.models.values) model.name: model.fields,
    for (final type in schema.types.values) type.name: type.fields,
    for (final owner in schema.sealeds.values)
      owner.name: [
        for (final variant in owner.variants)
          ...variant.fields.map(_sealedField),
      ],
  };
  final declared = <String, TypeUsage?>{
    for (final model in schema.models.values) model.name: model.usage,
    for (final type in schema.types.values) type.name: type.usage,
    for (final owner in schema.sealeds.values) owner.name: owner.usage,
  };
  final usages = <String, TypeUsage>{
    for (final name in fields.keys) name: TypeUsage.unused,
  };
  final visited = <String>{};

  void mark(String? name, TypeUsage usage) {
    if (name == null || !fields.containsKey(name)) return;
    final visit = '${usage.name}:$name';
    if (!visited.add(visit)) return;
    usages[name] = _mergeUsage(usages[name]!, usage);
    for (final field in fields[name]!) {
      mark(field.typeName ?? field.type, usage);
    }
  }

  for (final api in schema.apis.values) {
    mark(api.returnType, TypeUsage.output);
    for (final param in api.params) {
      mark(param.typeName ?? param.type, TypeUsage.input);
    }
  }
  for (final entry in declared.entries) {
    final usage = entry.value;
    if (usage == TypeUsage.input || usage == TypeUsage.inputOutput) {
      mark(entry.key, TypeUsage.input);
    }
    if (usage == TypeUsage.output || usage == TypeUsage.inputOutput) {
      mark(entry.key, TypeUsage.output);
    }
    if (usage == null && usages[entry.key] == TypeUsage.unused) {
      mark(entry.key, TypeUsage.output);
    }
  }
  return _TypeUsages(usages, _planInputAliases(schema, usages));
}

String _genTypes(LuxoSchema schema) {
  final usages = _inferTypeUsages(schema);
  final generated = usages.values.any((usage) => usage != TypeUsage.unused);
  bool needsConvert(String name, List<LuxoField> fields) =>
      usages[name] != TypeUsage.unused &&
      fields.any((field) => field.type == 'Bytes' || field.type == 'JSON');
  final convert =
      schema.models.values.any(
        (model) => needsConvert(model.name, model.fields),
      ) ||
      schema.types.values.any((type) => needsConvert(type.name, type.fields)) ||
      schema.sealeds.values.any(
        (owner) => needsConvert(owner.name, [
          for (final variant in owner.variants)
            ...variant.fields.map(_sealedField),
        ]),
      );
  final b = StringBuffer();
  b.writeln('// Auto-generated by luxo_client. DO NOT EDIT.\n');
  if (convert) {
    b.writeln("import 'dart:convert';");
  }
  bool needsBytes(String name, List<LuxoField> fields) =>
      usages[name] != TypeUsage.unused &&
      fields.any((field) => field.type == 'Bytes');
  if (usages.values.any(_hasOutputUsage) ||
      schema.models.values.any(
        (model) => needsBytes(model.name, model.fields),
      ) ||
      schema.types.values.any((type) => needsBytes(type.name, type.fields))) {
    b.writeln("import 'dart:typed_data';");
  }
  if (schema.enums.isNotEmpty || generated) {
    b.writeln("import 'package:luxo_client/luxo_client.dart';");
    if (generated) {
      b.writeln(r"import 'package:luxo_client/src/json.dart' as $jsonCodec;");
    }
    b.writeln();
  }
  b.writeln("export 'package:luxo_client/src/types.dart' show Page;\n");

  _writeEnums(b, schema);

  for (final t in schema.types.values) {
    _writeDartDeclarations(b, t.name, t.fields, schema, usages);
  }
  for (final m in schema.models.values) {
    _writeDartDeclarations(b, m.name, m.fields, schema, usages);
  }

  _writeDartSealeds(b, schema, usages);
  return b.toString();
}

void _writeDartDeclarations(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  final usage = usages[name] ?? TypeUsage.unused;
  if (_hasOutputUsage(usage)) {
    _writeDartDeclaration(
      b,
      dartTypeIdentifier(name),
      fields,
      schema,
      usages,
      output: true,
    );
    _writeDartDecoders(b, dartTypeIdentifier(name), fields, schema, usages);
  }
  if (_hasInputUsage(usage)) {
    _writeDartDeclaration(
      b,
      _inputTypeName(name, usages),
      fields,
      schema,
      usages,
      output: false,
    );
  }
}

void _validateDartMembers(
  String name,
  List<LuxoField> fields, {
  required bool output,
}) {
  const common = {
    'toJson',
    'fromJson',
    'hashCode',
    'runtimeType',
    'toString',
    'noSuchMethod',
    r'_$json',
    r'_$toJson',
    r'$writeJSON',
    r'_$writeJSON',
  };
  const inputMembers = {'writeLuxo', 'toLuxoParameters', r'_$writeLuxo'};
  const outputMembers = {r'$fromJsonItem'};
  final members = output ? outputMembers : inputMembers;
  for (final field in fields) {
    if (field.name == name ||
        common.contains(field.name) ||
        members.contains(field.name)) {
      throw FormatException(
        'generated Dart member conflicts with field: $name.${field.name}',
      );
    }
  }
}

void _writeDartDeclaration(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages, {
  required bool output,
}) {
  _validateDartMembers(name, fields, output: output);
  b.writeln(
    'class $name implements ${output ? r'$jsonCodec.LuxoJSONWritable' : r'LuxoBinaryEncodable, LuxoJSONParameters, $jsonCodec.LuxoJSONWritable'} {',
  );
  for (final f in fields) {
    final value = _dartFieldValueType(
      f,
      schema,
      usages: usages,
      input: !output,
    );
    final type = output ? 'Selected<$value>' : value;
    b.writeln('  final $type ${_dartMember(f)};');
  }
  b.writeln();
  b.writeln('  const $name({');
  for (final f in fields) {
    if (output) {
      final value = _dartFieldValueType(f, schema, usages: usages);
      b.writeln(
        '    this.${_dartMember(f)} = const Selected<$value>.unselected(),',
      );
    } else {
      b.writeln(
        f.nullable
            ? '    this.${_dartMember(f)},'
            : '    required this.${_dartMember(f)},',
      );
    }
  }
  b.writeln('  });\n');
  _writeDartJSONFactory(b, name, fields, schema, usages, output: output);
  _writeDartJSONProjection(b, fields, schema, output: output);
  _writeDartNativeJSONWriter(b, fields, schema, output: output);
  if (!output) {
    _writeDartJSONProjection(
      b,
      fields,
      schema,
      output: false,
      parameters: true,
    );
    _writeDartBinaryEncoder(b, fields, schema);
  }
  b.writeln('}\n');
}

void _writeDartJSONFactory(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages, {
  required bool output,
}) {
  if (output) {
    _writeDartSelectedJSONFactory(b, name, fields, schema, usages);
    return;
  }
  b.writeln(
    '  factory $name.fromJson(Map<String, dynamic> json) => $name._\$json(json, 0);',
  );
  b.writeln(
    '  factory $name._\$json(Map<String, dynamic> json, int \$depth) {',
  );
  _writeDartModelDepthGuard(b, '    ');
  for (final (index, field) in fields.indexed) {
    final read = _writeDartJSONRead(
      b,
      field,
      schema,
      usages,
      index,
      input: true,
    );
    b.writeln('    final \$field$index = $read;');
  }
  b.writeln('    return $name(');
  for (final (index, field) in fields.indexed) {
    b.writeln('      ${_dartMember(field)}: \$field$index,');
  }
  b.writeln('    );\n  }');
}

void _writeDartModelDepthGuard(StringBuffer b, String indent) {
  b.writeln(
    "${indent}if (\$depth >= 64) throw const FormatException('Model nesting exceeds 64 levels');",
  );
}

// '$' helper names cannot collide with DSL fields. List validation shares the
// original field pass, while the public unary constructor keeps its signature.
void _writeDartSelectedJSONFactory(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages, {
  bool sealedPayload = false,
}) {
  b.writeln(
    '  factory $name.fromJson(Map<String, dynamic> json) => $name._\$json(json, false, 0);',
  );
  b.writeln(
    '  static $name \$fromJsonItem(Map<String, dynamic> json) => $name._\$json(json, true, 0);',
  );
  b.writeln(
    '  factory $name._\$json(Map<String, dynamic> json, bool \$requireField, int \$depth) {',
  );
  _writeDartModelDepthGuard(b, '    ');
  if (sealedPayload) _writeSealedJSONKeys(b, fields);
  b.writeln('    var \$hasField = false;');
  for (final (index, field) in fields.indexed) {
    final value = _dartFieldValueType(field, schema, usages: usages);
    b.writeln('    var \$field$index = const Selected<$value>.unselected();');
    b.writeln("    if (json.containsKey('${field.name}')) {");
    b.writeln('      \$hasField = true;');
    final read = _writeDartJSONRead(
      b,
      field,
      schema,
      usages,
      index,
      input: false,
    );
    b.writeln('      \$field$index = Selected<$value>.value($read);');
    b.writeln('    }');
  }
  b.writeln(
    "    if (\$requireField && !\$hasField) throw const FormatException('${wireIdentifier(name).replaceAll(r'$', r'\$')} list item requires at least one Schema field');",
  );
  b.writeln('    return $name(');
  for (final (index, field) in fields.indexed) {
    b.writeln('      ${_dartMember(field)}: \$field$index,');
  }
  b.writeln('    );\n  }');
}

String _writeDartJSONRead(
  StringBuffer b,
  LuxoField field,
  LuxoSchema schema,
  _TypeUsages usages,
  int index, {
  required bool input,
}) {
  if (!field.isList || !_isNested(field, schema))
    return _jsonCast(field, schema, usages, input: input);
  final name = field.typeName ?? field.type;
  final type = input ? _inputTypeName(name, usages) : dartTypeIdentifier(name);
  final raw = '\$raw$index', items = '\$items$index';
  final indent = input ? '    ' : '      ';
  b.writeln(
    "${indent}final $raw = json['${field.name}'] as List${field.nullable ? '?' : ''};",
  );
  b.writeln('${indent}final List<$type>${field.nullable ? '?' : ''} $items;');
  if (field.nullable) {
    b.writeln('${indent}if ($raw == null) {');
    b.writeln('$indent  $items = null;');
    b.writeln('$indent} else {');
  }
  final nestedIndent = field.nullable ? '$indent  ' : indent;
  final args = input ? '\$depth + 1' : 'true, \$depth + 1';
  _writeDartJSONItems(b, raw, items, type, args, nestedIndent);
  if (field.nullable) b.writeln('$indent}');
  return items;
}

void _writeDartJSONItems(
  StringBuffer b,
  String raw,
  String items,
  String type,
  String args,
  String indent,
) {
  _writeDartTypedItems(
    b,
    raw,
    items,
    type,
    '$type._\$json($raw[\$index] as Map<String, dynamic>, $args)',
    indent,
  );
}

void _writeDartTypedItems(
  StringBuffer b,
  String raw,
  String items,
  String type,
  String read,
  String indent,
) {
  final values = '${items}Values';
  b.writeln('${indent}List<$type>? $values;');
  b.writeln(
    '${indent}for (var \$index = 0; \$index < $raw.length; \$index++) {',
  );
  b.writeln('$indent  final \$item = $read;');
  b.writeln('$indent  if ($values == null) {');
  b.writeln(
    '$indent    $values = List<$type>.filled($raw.length, \$item, growable: true);',
  );
  b.writeln('$indent  } else {');
  b.writeln('$indent    $values[\$index] = \$item;');
  b.writeln('$indent  }');
  b.writeln('$indent}');
  b.writeln('$indent$items = $values ?? <$type>[];');
}

void _writeDartJSONProjection(
  StringBuffer b,
  List<LuxoField> fields,
  LuxoSchema schema, {
  required bool output,
  bool parameters = false,
}) {
  final method = parameters ? 'toLuxoParameters' : 'toJson';
  b.writeln('  @override');
  if (!parameters) {
    b.writeln('  Map<String, Object?> toJson() => _\$toJson(0);');
    b.writeln('  Map<String, Object?> _\$toJson(int \$depth) {');
    _writeDartModelDepthGuard(b, '    ');
    b.writeln('    final \$json = <String, Object?>{};');
    for (final (index, field) in fields.indexed) {
      final indent = output ? '      ' : '    ';
      if (output) b.writeln('    if (${_dartMember(field)}.isSelected) {');
      final value = output ? '${_dartMember(field)}.value' : _dartMember(field);
      final read = _writeDartProjectionValue(
        b,
        field,
        schema,
        value,
        index,
        indent,
      );
      b.writeln("$indent\$json['${field.name}'] = $read;");
      if (output) b.writeln('    }');
    }
    b.writeln('    return \$json;\n  }');
    return;
  }
  b.writeln('  Map<String, Object?> $method() => {');
  for (final f in fields) {
    final fieldValue = output ? '${_dartMember(f)}.value' : _dartMember(f);
    final condition = output ? 'if (${_dartMember(f)}.isSelected) ' : '';
    b.writeln(
      "    $condition'${f.name}': ${_jsonWrite(f, schema, fieldValue, parameters: parameters)},",
    );
  }
  b.writeln('  };');
}

String _writeDartProjectionValue(
  StringBuffer b,
  LuxoField field,
  LuxoSchema schema,
  String value,
  int index,
  String indent,
) {
  if (!field.isList || !_isNested(field, schema))
    return _jsonWrite(field, schema, value);
  final raw = '\$raw$index', items = '\$items$index';
  b.writeln('${indent}final $raw = $value;');
  b.writeln(
    '${indent}final List<Map<String, Object?>>${field.nullable ? '?' : ''} $items;',
  );
  if (field.nullable) {
    b.writeln('${indent}if ($raw == null) {');
    b.writeln('$indent  $items = null;');
    b.writeln('$indent} else {');
  }
  _writeDartTypedItems(
    b,
    raw,
    items,
    'Map<String, Object?>',
    '$raw[\$index]._\$toJson(\$depth + 1)',
    field.nullable ? '$indent  ' : indent,
  );
  if (field.nullable) b.writeln('$indent}');
  return items;
}

void _writeDartNativeJSONWriter(
  StringBuffer b,
  List<LuxoField> fields,
  LuxoSchema schema, {
  required bool output,
}) {
  b.writeln('  @override');
  b.writeln(
    '  void \$writeJSON(\$jsonCodec.LuxoJSONOutput \$out) => _\$writeJSON(\$out, 0);',
  );
  b.writeln(
    '  void _\$writeJSON(\$jsonCodec.LuxoJSONOutput \$out, int \$depth) {',
  );
  _writeDartModelDepthGuard(b, '    ');
  _writeDartJSONRaw(b, '{', '    ');
  if (output && fields.length > 1) b.writeln('    var \$first = true;');
  for (final (index, field) in fields.indexed) {
    final indent = output ? '      ' : '    ';
    if (output) {
      b.writeln('    if (${_dartMember(field)}.isSelected) {');
      if (index != 0) b.writeln("      if (!\$first) \$out.writeRaw(',');");
      if (index + 1 < fields.length) b.writeln('      \$first = false;');
    }
    final prefix = !output && index != 0 ? ',' : '';
    _writeDartJSONRaw(b, '$prefix${jsonEncode(field.name)}:', indent);
    final value = output ? '${_dartMember(field)}.value' : _dartMember(field);
    _writeDartNativeJSONValue(b, field, schema, value, indent);
    if (output) b.writeln('    }');
  }
  _writeDartJSONRaw(b, '}', '    ');
  b.writeln('  }');
}

void _writeDartJSONRaw(StringBuffer b, String value, String indent) {
  final literal = jsonEncode(value).replaceAll(r'$', r'\$');
  b.writeln('$indent\$out.writeRaw($literal);');
}

void _writeDartNativeJSONValue(
  StringBuffer b,
  LuxoField field,
  LuxoSchema schema,
  String value,
  String indent,
) {
  if (field.nullable) {
    b.writeln('${indent}if ($value == null) {');
    _writeDartJSONRaw(b, 'null', '$indent  ');
    b.writeln('$indent} else {');
  }
  final bodyIndent = field.nullable ? '$indent  ' : indent;
  final nonNull = field.nullable ? '$value!' : value;
  if (field.isList) {
    _writeDartJSONRaw(b, '[', bodyIndent);
    b.writeln(
      '${bodyIndent}for (var \$index = 0; \$index < $nonNull.length; \$index++) {',
    );
    b.writeln("$bodyIndent  if (\$index != 0) \$out.writeRaw(',');");
    _writeDartNativeJSONScalar(
      b,
      field,
      schema,
      '$nonNull[\$index]',
      '$bodyIndent  ',
    );
    b.writeln('$bodyIndent}');
    _writeDartJSONRaw(b, ']', bodyIndent);
  } else {
    _writeDartNativeJSONScalar(b, field, schema, nonNull, bodyIndent);
  }
  if (field.nullable) b.writeln('$indent}');
}

void _writeDartNativeJSONScalar(
  StringBuffer b,
  LuxoField field,
  LuxoSchema schema,
  String value,
  String indent,
) {
  if (_isNested(field, schema)) {
    b.writeln('$indent$value._\$writeJSON(\$out, \$depth + 1);');
    return;
  }
  final encoded = field.type == 'Bytes'
      ? 'base64Encode($value)'
      : field.type == 'Enum' && field.typeName != null
      ? '$value.rawValue'
      : value;
  b.writeln('$indent\$out.writeJSON($encoded);');
}

void _writeDartBinaryEncoder(
  StringBuffer b,
  List<LuxoField> fields,
  LuxoSchema schema,
) {
  final arenaFields = fields.where(
    (field) =>
        !field.isList && (field.type == 'String' || field.type == 'Enum'),
  );
  final arenaTerms = arenaFields.map((field) {
    final value = _dartBinaryFieldReference(field);
    return field.nullable
        ? "($value == null ? 0 : luxoUtf8Length(${_enumWireValue(field.type, field.typeName, '$value!')}))"
        : 'luxoUtf8Length(${_enumWireValue(field.type, field.typeName, value)})';
  });
  b.writeln('\n  @override');
  b.writeln(
    '  void writeLuxo(LuxoEncoder encoder) => _\$writeLuxo(encoder, 0);',
  );
  b.writeln('  void _\$writeLuxo(LuxoEncoder encoder, int \$depth) {');
  _writeDartModelDepthGuard(b, '    ');
  b.writeln(
    '    encoder.writeVarint(${arenaTerms.isEmpty ? '0' : arenaTerms.join(' + ')});',
  );
  for (final field in fields) {
    _writeDartBinaryField(b, field, schema);
  }
  b.writeln('    encoder.writeEnd();');
  b.writeln('  }');
}

String _dartBinaryFieldReference(LuxoField field) =>
    field.name == 'encoder' ? 'this.encoder' : _dartMember(field);

void _writeDartBinaryField(StringBuffer b, LuxoField field, LuxoSchema schema) {
  final value = _dartBinaryFieldReference(field);
  b.writeln('    encoder.writeVarint(${field.id});');
  if (!field.nullable) {
    _writeDartBinaryValue(b, field, value, schema, '    ');
    return;
  }
  b.writeln('    if ($value == null) {');
  b.writeln('      encoder.writeNull();');
  b.writeln('    } else {');
  b.writeln('      encoder.writePresent();');
  _writeDartBinaryValue(b, field, '$value!', schema, '      ');
  b.writeln('    }');
}

void _writeDartBinaryValue(
  StringBuffer b,
  LuxoField field,
  String value,
  LuxoSchema schema,
  String indent,
) {
  if (field.isList) {
    b.writeln('${indent}encoder.writeArrayHeader($value.length);');
    b.writeln('${indent}for (final item in $value) {');
    _writeDartBinaryScalar(b, field, 'item', schema, '$indent  ');
    b.writeln('$indent}');
    return;
  }
  _writeDartBinaryScalar(b, field, value, schema, indent);
}

void _writeDartBinaryScalar(
  StringBuffer b,
  LuxoField field,
  String value,
  LuxoSchema schema,
  String indent,
) {
  if (schema.sealeds.containsKey(field.typeName ?? field.type)) {
    b.writeln(
      '${indent}encoder.writeDelimited((\$child) => $value._\$writeLuxo(\$child, \$depth + 1));',
    );
    return;
  }
  if (_isNested(field, schema)) {
    b.writeln('${indent}$value._\$writeLuxo(encoder, \$depth + 1);');
    return;
  }
  switch (field.type) {
    case 'Int' || 'Duration':
      b.writeln('${indent}encoder.writeSvarint($value);');
    case 'Float':
      b.writeln('${indent}encoder.writeFixed64($value);');
    case 'String' || 'Enum' || 'Decimal':
      b.writeln(
        '${indent}encoder.writeString(${_enumWireValue(field.type, field.typeName, value)});',
      );
    case 'Boolean':
      b.writeln('${indent}encoder.writeBool($value);');
    case 'DateTime':
      b.writeln('${indent}encoder.writeDateTime($value);');
    case 'UUID':
      b.writeln('${indent}encoder.writeUuid($value);');
    case 'Bytes':
      b.writeln('${indent}encoder.writeBytes($value);');
    case 'JSON':
      b.writeln(
        '${indent}encoder.writeBytes(utf8.encode(encodeLuxoJSON($value, opaque: true)));',
      );
  }
}

void _writeDartDecoders(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages, {
  bool rowOnly = false,
}) {
  b.writeln('$name decode$name(LuxoDecoder dec) => _\$decode$name(dec, 0);');
  b.writeln('$name _\$decode$name(LuxoDecoder dec, int \$depth) {');
  _writeDartModelDepthGuard(b, '  ');
  for (final f in fields) {
    final value = _dartFieldValueType(f, schema, usages: usages);
    b.writeln(
      '  Selected<$value> _${_dartMember(f)} = const Selected<$value>.unselected();',
    );
  }
  b.writeln('  dec.skipArenaHeader();');
  b.writeln('  while (dec.nextField()) {');
  b.writeln('    switch (dec.fieldID) {');
  for (final f in fields) {
    b.writeln(
      '      case ${f.id}: _${_dartMember(f)} = Selected.value(${_binaryRead(f, schema)}); break;',
    );
  }
  b.writeln(
    "      default: throw FormatException('unknown ${wireIdentifier(name).replaceAll(r'$', r'\$')} field ID \${dec.fieldID}');",
  );
  b.writeln('    }');
  b.writeln('  }');
  b.writeln('  dec.checkComplete();');
  b.writeln('  return $name(');
  for (final f in fields) {
    b.writeln('    ${_dartMember(f)}: _${_dartMember(f)},');
  }
  b.writeln('  );');
  b.writeln('}\n');

  if (rowOnly) return;
  _writeDartColumnarItem(b, name, fields, schema, usages);
  _writeColumnarDecoder(b, name, fields, schema, usages, false);
  _writeColumnarDecoder(b, name, fields, schema, usages, true);
}

void _writeDartColumnarItem(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  b.writeln('$name _\$columnar$name(int i, int \$depth,');
  for (final field in fields) {
    b.writeln(
      '  ${_columnStorageType(field, schema, usages)}? _${_dartMember(field)},',
    );
  }
  b.writeln(') {');
  _writeDartModelDepthGuard(b, '  ');
  b.writeln('  return $name(');
  for (final field in fields) {
    b.writeln(
      '    ${_dartMember(field)}: ${_columnValue(field, schema, usages)},',
    );
  }
  b.writeln('  );\n}\n');
}

String _jsonWrite(
  LuxoField field,
  LuxoSchema schema,
  String fieldValue, {
  bool parameters = false,
}) {
  if (parameters &&
      (_isNested(field, schema) ||
          field.type == 'Int' ||
          field.type == 'Duration')) {
    return fieldValue;
  }
  final value = field.nullable ? '$fieldValue!' : fieldValue;
  String encoded;
  if (_isNested(field, schema)) {
    encoded = '$value._\$toJson(\$depth + 1)';
  } else if (field.type == 'JSON' && !parameters) {
    encoded = field.isList
        ? '\$jsonCodec.normalizeLuxoJSONList($value)'
        : 'normalizeLuxoJSON($value)';
  } else if (field.type == 'Bytes') {
    encoded = field.isList
        ? '$value.map(base64Encode).toList()'
        : 'base64Encode($value)';
  } else if (field.type == 'Int' || field.type == 'Duration') {
    encoded = field.isList
        ? '$value.map((item) => item.toBigInt()).toList()'
        : '$value.toBigInt()';
  } else if (field.type == 'Enum' && field.typeName != null) {
    encoded = field.isList
        ? '${dartTypeIdentifier(field.typeName!)}.\$rawList($value)'
        : '$value.rawValue';
  } else {
    encoded = value;
  }
  return field.nullable ? '$fieldValue == null ? null : $encoded' : encoded;
}

void _writeColumnarDecoder(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  _TypeUsages usages,
  bool paginated,
) {
  final returnType = paginated ? 'Page<$name>' : 'List<$name>';
  final function = paginated ? 'decodePaginated$name' : 'decodeColumnar$name';
  b.writeln('$returnType $function(Uint8List data) => _\$$function(data, 0);');
  b.writeln('$returnType _\$$function(Uint8List data, int \$depth) {');
  b.writeln('  final dec = ColumnarDecoder(data);');
  for (final f in fields) {
    b.writeln(
      '  ${_columnStorageType(f, schema, usages)}? _${_dartMember(f)};',
    );
  }
  b.writeln('  while (dec.nextColumn()) {');
  b.writeln('    switch (dec.fieldID) {');
  for (final f in fields) {
    b.writeln(
      '      case ${f.id}: _${_dartMember(f)} = ${_columnRead(f, schema)}; break;',
    );
  }
  b.writeln(
    "      default: throw FormatException('unknown ${wireIdentifier(name).replaceAll(r'$', r'\$')} column ID \${dec.fieldID}');",
  );
  b.writeln('    }');
  b.writeln('  }');
  if (paginated) {
    b.writeln('  final total = dec.readSvarint();');
    b.writeln('  final page = dec.readSvarint();');
    b.writeln('  final pageSize = dec.readSvarint();');
  }
  final context = paginated ? 'pagination' : 'columnar';
  b.writeln(
    "  if (dec.error != null || dec.offset != data.length) throw FormatException(dec.error ?? 'trailing $context data');",
  );
  final args = fields.map((field) => '_${_dartMember(field)}').join(', ');
  final suffix = args.isEmpty ? '' : ', $args';
  b.writeln('  final items = dec.count == 0 ? <$name>[] : List<$name>.filled(');
  b.writeln(
    '    dec.count, _\$columnar$name(0, \$depth$suffix), growable: true,',
  );
  b.writeln('  );');
  b.writeln('  for (var i = 1; i < dec.count; i++) {');
  b.writeln('    items[i] = _\$columnar$name(i, \$depth$suffix);');
  b.writeln('  }');
  if (paginated) {
    b.writeln(
      '  return Page(items: items, total: total, page: page, pageSize: pageSize);',
    );
  } else {
    b.writeln('  return items;');
  }
  b.writeln('}\n');
}

bool _isNested(LuxoField f, LuxoSchema schema) {
  final name = f.typeName ?? f.type;
  return f.relation ||
      schema.models.containsKey(name) ||
      schema.types.containsKey(name) ||
      schema.sealeds.containsKey(name);
}

String _columnStorageType(LuxoField f, LuxoSchema schema, _TypeUsages usages) {
  if (_isNested(f, schema) ||
      f.isList ||
      f.type == 'Bytes' ||
      f.type == 'JSON') {
    return f.nullable ? 'List<Uint8List?>' : 'List<Uint8List>';
  }
  final base = _dartFieldType(f, schema, usages: usages);
  if (f.type == 'Enum' && f.typeName != null) {
    return f.nullable ? 'List<String?>' : 'List<String>';
  }
  return f.nullable ? 'List<$base?>' : 'List<$base>';
}

String _columnRead(LuxoField f, LuxoSchema schema) {
  if (_isNested(f, schema) ||
      f.isList ||
      f.type == 'Bytes' ||
      f.type == 'JSON') {
    return f.nullable ? 'dec.readColumnBytesPtr()' : 'dec.readColumnBytes()';
  }
  return switch (f.type) {
    'Int' ||
    'Duration' => f.nullable ? 'dec.readColumnIntPtr()' : 'dec.readColumnInt()',
    'Float' =>
      f.nullable ? 'dec.readColumnFloatPtr()' : 'dec.readColumnFloat()',
    'Boolean' =>
      f.nullable ? 'dec.readColumnBoolPtr()' : 'dec.readColumnBool()',
    'DateTime' =>
      f.nullable ? 'dec.readColumnDateTimePtr()' : 'dec.readColumnDateTime()',
    'UUID' => f.nullable ? 'dec.readColumnUuidPtr()' : 'dec.readColumnUuid()',
    _ => f.nullable ? 'dec.readColumnStringPtr()' : 'dec.readColumnString()',
  };
}

String _columnValue(LuxoField f, LuxoSchema schema, _TypeUsages usages) {
  final column = '_${_dartMember(f)}';
  final typeName = dartTypeIdentifier(f.typeName ?? f.type);
  final valueType = _dartFieldValueType(f, schema, usages: usages);
  final absent = 'const Selected<$valueType>.unselected()';
  final element = f.nullable ? '$column[i]!' : '$column[i]';
  String value;
  if (_isNested(f, schema)) {
    if (f.isList) {
      final decoded = schema.sealeds.containsKey(f.typeName ?? f.type)
          ? '_\$decodeSealedList$typeName($element, \$depth + 1)'
          : '_\$decodeColumnar$typeName($element, \$depth + 1)';
      value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
    } else {
      final decoded = '_\$decode$typeName(LuxoDecoder($element), \$depth + 1)';
      value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
    }
  } else if (f.isList) {
    final decoded = f.type == 'Enum' && f.typeName != null
        ? '${dartTypeIdentifier(f.typeName!)}.\$readArray(LuxoDecoder($element), complete: true)'
        : _binaryScalarRead(
            f.type,
            'LuxoDecoder($element)',
            array: true,
            checked: true,
          );
    value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
  } else if (f.type == 'Enum' && f.typeName != null) {
    final decoded = '${dartTypeIdentifier(f.typeName!)}.fromValue($element)';
    value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
  } else if (f.type == 'JSON') {
    final decoded = 'decodeLuxoJSONBytes($element)';
    value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
  } else {
    value = '$column[i]';
  }
  return '$column == null ? $absent : Selected<$valueType>.value($value)';
}

String _binaryScalarRead(
  String type,
  String dec, {
  bool array = false,
  bool checked = false,
}) {
  final method = switch (type) {
    'Int' || 'Duration' => 'readInt',
    'Float' => 'readFloat',
    'Boolean' => 'readBool',
    'DateTime' => 'readDateTime',
    'UUID' => 'readUuid',
    'Bytes' || 'JSON' => 'readBytes',
    _ => 'readString',
  };
  final read =
      '$dec.$method${array ? 'Array' : ''}${checked ? 'Checked' : ''}()';
  if (type != 'JSON') return read;
  return array
      ? '$read.map((v) => decodeLuxoJSONBytes(v)).toList()'
      : 'decodeLuxoJSONBytes($read)';
}

String _jsonCast(
  LuxoField f,
  LuxoSchema schema,
  _TypeUsages usages, {
  required bool input,
}) {
  final n = f.name;
  final q = f.nullable;
  if (f.type == 'Enum' && f.typeName != null) {
    final read = f.isList
        ? '${dartTypeIdentifier(f.typeName!)}.\$fromJSONList(json[\'$n\'])'
        : '${dartTypeIdentifier(f.typeName!)}.fromValue(luxoStringFromJSON(json[\'$n\']))';
    return q ? "json['$n'] == null ? null : $read" : read;
  }
  // Scalar array field ([T]) — JSON value is a List.
  if (f.isList && _isScalar(f.type)) {
    final cast = switch (f.type) {
      'Float' =>
        "(json['$n'] as List).map((e) => luxoDoubleFromJSON(e)).toList()",
      'Bytes' =>
        "(json['$n'] as List).map((e) => base64Decode(e as String)).toList()",
      'Int' ||
      'Duration' => "(json['$n'] as List).map(luxoInt64FromJSON).toList()",
      'JSON' => "(json['$n'] as List).map(normalizeLuxoJSON).toList()",
      _ => _jsonCheckedScalarList(f.type, "json['$n']"),
    };
    return q ? "json['$n'] != null ? $cast : null" : cast;
  }
  switch (f.type) {
    case 'Int':
    case 'Duration':
      return q
          ? "json['$n'] == null ? null : luxoInt64FromJSON(json['$n'])"
          : "luxoInt64FromJSON(json['$n'])";
    case 'Float':
      return q
          ? "json['$n'] == null ? null : luxoDoubleFromJSON(json['$n'])"
          : "luxoDoubleFromJSON(json['$n'])";
    case 'Boolean':
      return q ? "json['$n'] as bool?" : "json['$n'] as bool";
    case 'Bytes':
      return q
          ? "json['$n'] == null ? null : base64Decode(json['$n'] as String)"
          : "base64Decode(json['$n'] as String)";
    case 'JSON':
      final read = "normalizeLuxoJSON(json['$n'])";
      return input && !q
          ? "json.containsKey('$n') ? $read : (throw const FormatException('Missing required JSON field $n'))"
          : read;
    case 'DateTime':
      return q
          ? "json['$n'] == null ? null : luxoDateTimeFromJSON(json['$n'])"
          : "luxoDateTimeFromJSON(json['$n'])";
    case 'String':
    case 'UUID':
    case 'Decimal':
    case 'Enum':
      return q
          ? "json['$n'] == null ? null : luxoStringFromJSON(json['$n'])"
          : "luxoStringFromJSON(json['$n'])";
    default:
      // Relation / nested-model field.
      final rawTypeName = f.typeName ?? f.type;
      final typeName =
          input &&
              (schema.models.containsKey(rawTypeName) ||
                  schema.types.containsKey(rawTypeName) ||
                  schema.sealeds.containsKey(rawTypeName))
          ? _inputTypeName(rawTypeName, usages)
          : dartTypeIdentifier(rawTypeName);
      final arguments = input ? '\$depth + 1' : 'false, \$depth + 1';
      final read =
          "$typeName._\$json(json['$n'] as Map<String, dynamic>, $arguments)";
      if (q) return "json['$n'] != null ? $read : null";
      return read;
  }
}

String _binaryRead(LuxoField f, LuxoSchema schema) {
  if (schema.sealeds.containsKey(f.typeName ?? f.type)) {
    final name = dartTypeIdentifier(f.typeName ?? f.type);
    final read = f.isList
        ? '_\$readSealedList$name(dec, \$depth + 1)'
        : '_\$decode$name(LuxoDecoder(dec.readBytes()), \$depth + 1)';
    return f.nullable
        ? "dec.readCanonicalMarker('nullable') ? $read : null"
        : read;
  }
  final n = f.nullable;
  if (f.type == 'Enum' && f.typeName != null) {
    if (f.isList) {
      final read = '${dartTypeIdentifier(f.typeName!)}.\$readArray(dec)';
      return n ? 'dec.readBool() ? $read : null' : read;
    }
    return n
        ? '${dartTypeIdentifier(f.typeName!)}.\$readNullable(dec)'
        : '${dartTypeIdentifier(f.typeName!)}.fromValue(dec.readString())';
  }
  // Scalar array field ([T]) — inline [count][items...] in the row stream.
  if (f.isList && _isScalar(f.type)) {
    // DateTime arrays carry seconds/nanoseconds pairs but surface as
    // List<String> (ISO 8601) to match JSON mode.
    final read = _binaryScalarRead(f.type, 'dec', array: true);
    return n ? 'dec.readBool() ? $read : null' : read;
  }
  switch (f.type) {
    case 'Int':
    case 'Duration':
      return n ? 'dec.readIntPtr()' : 'dec.readInt()';
    case 'Float':
      return n ? 'dec.readFloatPtr()' : 'dec.readFloat()';
    case 'Boolean':
      return n ? 'dec.readBoolPtr()' : 'dec.readBool()';
    case 'UUID':
      return n ? 'dec.readUuidPtr()' : 'dec.readUuid()';
    case 'Bytes':
      return n ? 'dec.readNullable(dec.readBytes)' : 'dec.readBytes()';
    case 'JSON':
      final read = 'decodeLuxoJSONBytes(dec.readBytes())';
      return n ? "dec.readCanonicalMarker('nullable') ? $read : null" : read;
    // DateTime carries seconds/nanoseconds on the binary wire; decode to an
    // ISO 8601 string so the field type matches JSON mode (String).
    case 'DateTime':
      return n ? 'dec.readDateTimePtr()' : 'dec.readDateTime()';
    case 'String':
    case 'Decimal':
    case 'Enum':
      return n ? 'dec.readStringPtr()' : 'dec.readString()';
    default:
      // Response children have independent length-delimited boundaries.
      final tn = dartTypeIdentifier(f.typeName ?? f.type);
      final decoded = f.isList
          ? '_\$decodeColumnar$tn(dec.readBytes(), \$depth + 1)'
          : '_\$decode$tn(LuxoDecoder(dec.readBytes()), \$depth + 1)';
      return n
          ? "dec.readCanonicalMarker('nullable') ? $decoded : null"
          : decoded;
  }
}

String _genSchemaMap(LuxoSchema schema) {
  final b = StringBuffer();
  b.writeln('// Auto-generated by luxo_client. DO NOT EDIT.\n');
  b.writeln("import 'package:luxo_client/luxo_client.dart';\n");
  final selectionTypes = <String, LuxoModel>{
    for (final type in schema.types.values)
      type.name: LuxoModel(name: type.name, fields: type.fields),
    ...schema.models,
    for (final owner in schema.sealeds.values)
      owner.name: LuxoModel(
        name: owner.name,
        fields: [
          for (final variant in owner.variants)
            LuxoField(
              id: variant.id,
              name: variant.name,
              type: 'Model',
              typeName: '${owner.name}.${variant.name}',
            ),
        ],
      ),
    for (final owner in schema.sealeds.values)
      '@sealed:${owner.name}': LuxoModel(
        name: '@sealed:${owner.name}',
        fields: [
          for (final variant in owner.variants)
            LuxoField(
              id: variant.id,
              name: variant.name,
              type: 'Model',
              typeName: '${owner.name}.${variant.name}',
            ),
        ],
      ),
    for (final owner in schema.sealeds.values)
      for (final variant in owner.variants)
        '${owner.name}.${variant.name}': LuxoModel(
          name: '${owner.name}.${variant.name}',
          fields: variant.fields.map(_sealedField).toList(),
        ),
  };
  b.writeln(
    'final luxoSelectionTypes = <String, Map<String, SelectionFieldSchema>>{',
  );
  for (final type in selectionTypes.values) {
    b.write("  '${type.name}': {");
    for (var i = 0; i < type.fields.length; i++) {
      final field = type.fields[i];
      if (i > 0) b.write(', ');
      final typeName = field.typeName ?? field.type;
      final declaredType = field.typeName == null ? 'null' : "'$typeName'";
      b.write(
        "'${field.name}': SelectionFieldSchema(${field.id}, "
        "$declaredType, '${field.type}', ${field.isList}, ${field.nullable})",
      );
    }
    b.writeln('},');
  }
  b.writeln('};\n');
  b.writeln(
    '/// API schema map — used by binary transport for encoding requests.',
  );
  b.writeln('final luxoSchema = <String, APISchemaEntry>{');
  for (final api in schema.apis.values) {
    if (api.internal) continue;
    final returnFields = api.returnType == null
        ? const <LuxoField>[]
        : (selectionTypes[api.returnType]?.fields ?? const <LuxoField>[]);
    final needsTypes =
        selectionTypes.containsKey(api.returnType) ||
        returnFields.isNotEmpty ||
        api.params.any(
          (param) => param.type == 'Model' || param.type == 'Sealed',
        );
    b.write("  '${api.name}': APISchemaEntry(${api.id}");
    b.write(', [\n');
    for (final p in api.params) {
      b.writeln(
        "    ParamSchema(${p.id}, '${p.name}', '${p.type}', ${p.isList}, ${p.nullable}${p.typeName == null ? '' : ", '${p.typeName}'"}),",
      );
    }
    b.write('  ]');
    if (returnFields.isNotEmpty) {
      b.write(", luxoSelectionTypes['${api.returnType}']!");
    } else {
      b.write(', const {}');
    }
    if (needsTypes) {
      b.write(', luxoSelectionTypes');
    } else {
      b.write(', const {}');
    }
    final modelType = schema.sealeds.containsKey(api.returnType)
        ? "'@sealed:${api.returnType}'"
        : selectionTypes.containsKey(api.returnType)
        ? "'${api.returnType}'"
        : 'null';
    b.write(
      ', JSONResponseSchema(modelType: $modelType, '
      '${needsTypes ? 'types: luxoSelectionTypes, ' : ''}'
      'isList: ${api.returnList}, paginated: ${api.paginated})',
    );
    b.writeln('),');
  }
  b.writeln('};');
  return b.toString();
}

String _genClient(LuxoSchema schema) {
  final usages = _inferTypeUsages(schema);
  final apis = schema.apis.values.where((api) => !api.internal).toList();
  final b = StringBuffer();
  _writeClientImports(b, schema, apis, usages);
  b.writeln('class LuxoClient {');
  b.writeln('  final Transport transport;');
  b.writeln('  static final schema = luxoSchema;\n');
  b.writeln(
    r'  static PreparedSelection $prepareSelection(String text) => PreparedSelection(text);',
  );
  b.writeln('  LuxoClient(Transport transport) : transport = transport {');
  b.writeln('    transport.setSchema(luxoSchema);');
  b.writeln('  }\n');
  b.writeln('  /// Create client from URL — auto-detects HTTP vs WebSocket.');
  b.writeln(
    "  factory LuxoClient.create(String endpoint, {TransportOptions? options}) {",
  );
  b.writeln(
    "    final transport = endpoint.startsWith('ws') ? WsTransport(endpoint, options: options) : HttpTransport(endpoint, options: options);",
  );
  b.writeln('    return LuxoClient(transport);');
  b.writeln('  }\n');
  b.writeln('  void setMode(TransportMode mode) => transport.setMode(mode);');
  b.writeln('  void setToken(String token) => transport.setToken(token);');
  b.writeln('  void close() => transport.close();\n');

  for (final api in apis) {
    _genMethod(b, api, schema, usages);
  }

  b.writeln('}');
  for (final api in apis) _genRequest(b, api, schema, usages);
  return b.toString();
}

void _writeClientImports(
  StringBuffer b,
  LuxoSchema schema,
  List<LuxoAPI> apis,
  _TypeUsages usages,
) {
  final types = apis.any(
    (api) =>
        schema.enums.containsKey(api.returnType) ||
        !_isScalar(_responseType(api, schema)) ||
        api.params.any(
          (param) =>
              schema.enums.containsKey(param.typeName) ||
              usages.containsKey(param.typeName ?? param.type),
        ),
  );
  b.writeln('// Auto-generated by luxo_client. DO NOT EDIT.\n');
  if (apis.isNotEmpty) {
    b.writeln(r"import 'dart:async' as $async;");
    b.writeln("import 'dart:typed_data';");
  }
  if (apis.any(
    (api) =>
        api.returnType == 'Bytes' ||
        api.params.any((param) => param.type == 'JSON'),
  )) {
    b.writeln("import 'dart:convert';");
  }
  b.writeln("import 'package:luxo_client/luxo_client.dart';");
  if (types) b.writeln("import 'types.dart';");
  b.writeln("import 'schema.dart';");
}

bool _apiNeedsSelection(LuxoAPI api, LuxoSchema schema) {
  final owner = schema.sealeds[api.returnType];
  if (owner != null && owner.variants.isEmpty) return false;
  return api.paginated || !_isScalar(_responseType(api, schema));
}

String _responseType(LuxoAPI api, LuxoSchema schema) {
  final type = api.returnType ?? 'Int';
  return schema.enums.containsKey(type) ? 'Enum' : type;
}

void _genMethod(
  StringBuffer b,
  LuxoAPI api,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  final type = _responseType(api, schema);
  final ret = _returnType(api, type);
  if (api.stream) {
    _genStreamMethod(
      b,
      api,
      type,
      ret,
      usages,
      sealedReturn: schema.sealeds.containsKey(api.returnType),
      selection: _apiNeedsSelection(api, schema),
    );
    return;
  }

  final dec = _decoder(
    api,
    type,
    sealedReturn: schema.sealeds.containsKey(api.returnType),
  );
  final selection = _apiNeedsSelection(api, schema);
  final declarations = _unaryDeclarations(api, usages, selection: selection);
  final args = declarations.isEmpty ? '' : '{${declarations.join(', ')}}';
  b.writeln(
    "  \$async.Future<$ret> ${dartSourceIdentifier(api.name)}($args) async {",
  );
  if (selection) {
    b.writeln(r'    final sel = select ?? $selectionPlan?.text;');
  }
  b.writeln(
    "    return transport.call('${api.name}', request: ${_requestCall(api, selection: selection)}, decoder: $dec);",
  );
  b.writeln('  }\n');
}

const _paginationNames = ['page', 'pageSize'];

List<String> _unaryDeclarations(
  LuxoAPI api,
  _TypeUsages usages, {
  required bool selection,
}) {
  // If API has explicit params, always use them (no guessing by name prefix).
  // page/pageSize are always optional for paginated APIs.
  final result = api.params
      .map(
        (param) => _dartParamDeclaration(
          param,
          usages,
          forcedOptional:
              api.paginated && _paginationNames.contains(param.name),
        ),
      )
      .toList();
  if (selection) {
    result.add('String? select');
    result.add(r'PreparedSelection? $selectionPlan');
  }
  if (api.paginated)
    result.addAll(['List<LuxoFilter>? filters', 'List<LuxoSorter>? sorters']);
  return result;
}

void _writeUnaryParams(StringBuffer b, LuxoAPI api, {required bool selection}) {
  // Generic params — all other APIs with explicit params.
  for (final param in api.params) {
    _writeDartParamEntry(
      b,
      param,
      forcedOptional: api.paginated && _paginationNames.contains(param.name),
    );
  }
  if (selection) {
    b.writeln("      if (sel != null) r'\$select': sel,");
  }
  if (api.paginated) {
    b.writeln("      if (filters != null) r'\$filters': filters,");
    b.writeln("      if (sorters != null) r'\$sorters': sorters,");
  }
}

void _genStreamMethod(
  StringBuffer b,
  LuxoAPI api,
  String type,
  String returnType,
  _TypeUsages usages, {
  required bool sealedReturn,
  required bool selection,
}) {
  final methodName =
      'subscribe${dartSourceIdentifier(api.name[0].toUpperCase() + api.name.substring(1))}';
  final structuredReturn = selection;
  final declarations = api.params
      .map((param) => _dartParamDeclaration(param, usages))
      .toList();
  if (structuredReturn) {
    declarations.add('String? select');
    declarations.add(r'PreparedSelection? $selectionPlan');
  }
  declarations.add('required void Function($returnType) onData');
  declarations.add('void Function(LuxoError)? onError');
  declarations.add('void Function()? onDone');
  b.writeln(
    '  \$async.Future<void Function()> $methodName({${declarations.join(', ')}}) {',
  );
  b.writeln(
    "    return transport.subscribe('${api.name}', const {}, onData, request: ${_requestCall(api, selection: structuredReturn, stream: true)},",
  );
  b.writeln(
    '      decoder: ${_decoder(api, type, sealedReturn: sealedReturn)}, onError: onError, onDone: onDone);',
  );
  b.writeln('  }\n');
}

String _dartParamDeclaration(
  LuxoParam param,
  _TypeUsages usages, {
  bool forcedOptional = false,
}) {
  final type = _dartParamType(param, usages);
  if (param.nullable && param.hasDefault) {
    return 'LuxoOptional<$type> ${dartSourceIdentifier(param.name)} = const LuxoOptional<$type>.absent()';
  }
  if (forcedOptional || param.hasDefault) {
    return '$type? ${dartSourceIdentifier(param.name)}';
  }
  if (param.nullable) {
    return 'required $type? ${dartSourceIdentifier(param.name)}';
  }
  return 'required $type ${dartSourceIdentifier(param.name)}';
}

void _writeDartParamEntry(
  StringBuffer b,
  LuxoParam param, {
  bool forcedOptional = false,
}) {
  String value(String expression, bool nullable) {
    if (param.type != 'Enum' || param.typeName == null) return expression;
    if (param.isList) {
      final method = nullable ? '\$nullableRawList' : '\$rawList';
      return '${dartTypeIdentifier(param.typeName!)}.$method($expression)';
    }
    final checked = nullable ? '$expression?' : expression;
    return '$checked.rawValue';
  }

  if (param.nullable && param.hasDefault) {
    b.writeln(
      "      if (${dartSourceIdentifier(param.name)}.isPresent) '${param.name}': ${value('${dartSourceIdentifier(param.name)}.value', true)},",
    );
    return;
  }
  if (forcedOptional || param.hasDefault) {
    b.writeln(
      "      if (${dartSourceIdentifier(param.name)} != null) '${param.name}': ${value(dartSourceIdentifier(param.name), false)},",
    );
    return;
  }
  b.writeln(
    "      '${param.name}': ${value(dartSourceIdentifier(param.name), param.nullable)},",
  );
}

String _returnType(LuxoAPI api, String type) {
  final t = type == 'Enum' && api.returnType != 'Enum'
      ? dartTypeIdentifier(api.returnType!)
      : _luxoTypeToDart(type);
  final result = api.paginated
      ? 'Page<$t>'
      : api.returnList
      ? _dartListType(type, t)
      : t;
  final nullable =
      api.returnNullable ||
      (type == 'JSON' && !api.returnList && !api.paginated);
  return nullable ? '$result?' : result;
}

/// Generates a decoder lambda that handles both JSON (Map/int/etc) and binary (Uint8List).
String _decoder(LuxoAPI api, String type, {bool sealedReturn = false}) {
  if (api.returnNullable) {
    return '''(d) {
      if (d is Uint8List) {
        if (d.isEmpty || d[0] > 1 || (d[0] == 0 ? d.length != 1 : d.length == 1)) {
          throw const FormatException('invalid nullable root envelope');
        }
        if (d[0] == 0) return null;
        d = Uint8List.sublistView(d, 1);
      } else if (d == null) {
        return null;
      }
      return ${_decodeExpression(api, type, 'd', sealedReturn: sealedReturn)};
    }''';
  }
  return '(d) => ${_decodeExpression(api, type, 'd', sealedReturn: sealedReturn)}';
}

String _decodeExpression(
  LuxoAPI api,
  String t,
  String value, {
  bool sealedReturn = false,
}) {
  final sourceType = dartTypeIdentifier(t);
  if (t == 'Enum' && api.returnType != 'Enum') {
    final name = dartTypeIdentifier(api.returnType!);
    final binary = api.returnList
        ? '$name.\$readArray(LuxoDecoder($value), complete: true)'
        : '$name.fromValue(LuxoDecoder($value).readStringChecked())';
    final json = api.returnList
        ? '$name.\$fromJSONList($value)'
        : '$name.fromValue(luxoStringFromJSON($value))';
    return '$value is Uint8List ? $binary : $json';
  }
  if (_isScalar(t)) {
    final binaryRead = _binaryScalarRead(
      t,
      'LuxoDecoder($value)',
      array: api.returnList,
      checked: true,
    );
    final jsonRead = _jsonScalarResponse(t, value, array: api.returnList);
    return '$value is Uint8List ? $binaryRead : $jsonRead';
  }
  if (api.paginated) {
    return "$value is Uint8List ? decodePaginated$sourceType($value) : Page.fromJson($value as Map<String, dynamic>, $sourceType.\$fromJsonItem)";
  }
  if (api.returnList && sealedReturn) {
    return "$value is Uint8List ? decodeSealedList$sourceType($value) : ($value as List).map((e) => $sourceType.fromJson(e as Map<String, dynamic>)).toList()";
  }
  if (api.returnList) {
    return "$value is Uint8List ? decodeColumnar$sourceType($value) : ($value as List).map((e) => $sourceType.\$fromJsonItem(e as Map<String, dynamic>)).toList()";
  }
  return "$value is Uint8List ? decode$sourceType(LuxoDecoder($value)) : $sourceType.fromJson($value as Map<String, dynamic>)";
}

String _jsonScalarResponse(String type, String value, {required bool array}) {
  final dartType = _luxoTypeToDart(type);
  if (array) {
    return switch (type) {
      'Float' => '($value as List).map(luxoDoubleFromJSON).toList()',
      'Bytes' =>
        '($value as List).map((e) => base64Decode(e as String)).toList()',
      'Int' || 'Duration' => '($value as List).map(luxoInt64FromJSON).toList()',
      'JSON' => '(normalizeLuxoJSON($value) as List).cast<Object?>()',
      _ => _jsonCheckedScalarList(type, value),
    };
  }
  return switch (type) {
    'Float' => 'luxoDoubleFromJSON($value)',
    'Bytes' => 'base64Decode($value as String)',
    'Int' || 'Duration' => 'luxoInt64FromJSON($value)',
    'JSON' => 'normalizeLuxoJSON($value)',
    'DateTime' => 'luxoDateTimeFromJSON($value)',
    'String' || 'UUID' || 'Decimal' || 'Enum' => 'luxoStringFromJSON($value)',
    _ => '$value as $dartType',
  };
}

String _jsonCheckedScalarList(String type, String value) =>
    'luxo${type == 'DateTime'
        ? 'DateTime'
        : type == 'Boolean'
        ? 'Boolean'
        : 'String'}ListFromJSON($value)';
