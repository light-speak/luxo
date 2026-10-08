import 'dart:convert';
import 'dart:io';

import 'types.dart';
import 'json.dart';

part 'codegen_enum.dart';
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
      queryParameters: {...base.queryParameters, r'$schema': ''},
    );
    final request = await client.getUrl(uri);
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
    _validateEnums(schema);
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
      return type; // model reference
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

String _dartParamType(LuxoParam param, Map<String, TypeUsage> usages) {
  final schemaType = param.typeName ?? param.type;
  final type = usages.containsKey(schemaType)
      ? _inputTypeName(schemaType, usages)
      : param.type == 'Enum' && param.typeName != null
      ? param.typeName!
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
  required Map<String, TypeUsage> usages,
  bool input = false,
}) {
  final typeName = f.typeName ?? f.type;
  final structured =
      schema.models.containsKey(typeName) || schema.types.containsKey(typeName);
  final base = structured && input
      ? _inputTypeName(typeName, usages)
      : f.type == 'Enum' && f.typeName != null
      ? f.typeName!
      : f.type == 'Model' || structured
      ? typeName
      : _luxoTypeToDart(f.type);
  if (f.isList) return _dartListType(f.type, base);
  return base;
}

bool _hasInputUsage(TypeUsage usage) =>
    usage == TypeUsage.input || usage == TypeUsage.inputOutput;

bool _hasOutputUsage(TypeUsage usage) =>
    usage == TypeUsage.output || usage == TypeUsage.inputOutput;

String _inputTypeName(String name, Map<String, TypeUsage> usages) =>
    usages[name] == TypeUsage.inputOutput ? '${name}Input' : name;

TypeUsage _mergeUsage(TypeUsage current, TypeUsage next) {
  if (current == TypeUsage.unused) return next;
  if (current == next || current == TypeUsage.inputOutput) return current;
  return TypeUsage.inputOutput;
}

Map<String, TypeUsage> _inferTypeUsages(LuxoSchema schema) {
  final fields = <String, List<LuxoField>>{
    for (final model in schema.models.values) model.name: model.fields,
    for (final type in schema.types.values) type.name: type.fields,
  };
  final declared = <String, TypeUsage?>{
    for (final model in schema.models.values) model.name: model.usage,
    for (final type in schema.types.values) type.name: type.usage,
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
  return usages;
}

String _genTypes(LuxoSchema schema) {
  final usages = _inferTypeUsages(schema);
  bool needsConvert(String name, List<LuxoField> fields) =>
      usages[name] != TypeUsage.unused &&
      fields.any((field) => field.type == 'Bytes' || field.type == 'JSON');
  final convert =
      schema.models.values.any(
        (model) => needsConvert(model.name, model.fields),
      ) ||
      schema.types.values.any((type) => needsConvert(type.name, type.fields));
  final b = StringBuffer();
  b.writeln('// Auto-generated by luxo_client. DO NOT EDIT.\n');
  if (convert) {
    b.writeln("import 'dart:convert';");
  }
  if (convert || usages.values.any(_hasOutputUsage)) {
    b.writeln("import 'dart:typed_data';");
  }
  if (schema.enums.isNotEmpty ||
      usages.values.any((usage) => usage != TypeUsage.unused)) {
    b.writeln("import 'package:luxo_client/luxo_client.dart';\n");
  }
  b.writeln("export 'package:luxo_client/src/types.dart' show Page;\n");

  _writeEnums(b, schema);

  for (final t in schema.types.values) {
    _writeDartDeclarations(b, t.name, t.fields, schema, usages);
  }
  for (final m in schema.models.values) {
    _writeDartDeclarations(b, m.name, m.fields, schema, usages);
  }

  return b.toString();
}

void _writeDartDeclarations(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
  final usage = usages[name] ?? TypeUsage.unused;
  if (_hasOutputUsage(usage)) {
    _writeDartDeclaration(b, name, fields, schema, usages, output: true);
    _writeDartDecoders(b, name, fields, schema, usages);
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

void _writeDartDeclaration(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  Map<String, TypeUsage> usages, {
  required bool output,
}) {
  b.writeln(
    'class $name implements ${output ? 'LuxoJSONEncodable' : 'LuxoBinaryEncodable, LuxoJSONParameters'} {',
  );
  for (final f in fields) {
    final base = _dartFieldType(f, schema, usages: usages, input: !output);
    final value = f.nullable ? '$base?' : base;
    final type = output ? 'Selected<$value>' : value;
    b.writeln('  final $type ${f.name};');
  }
  b.writeln();
  b.writeln('  const $name({');
  for (final f in fields) {
    if (output) {
      final base = _dartFieldType(f, schema, usages: usages);
      final value = f.nullable ? '$base?' : base;
      b.writeln('    this.${f.name} = const Selected<$value>.unselected(),');
    } else {
      b.writeln(
        f.nullable ? '    this.${f.name},' : '    required this.${f.name},',
      );
    }
  }
  b.writeln('  });\n');
  _writeDartJSONFactory(b, name, fields, schema, usages, output: output);
  _writeDartJSONProjection(b, fields, schema, output: output);
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
  Map<String, TypeUsage> usages, {
  required bool output,
}) {
  if (output) {
    _writeDartSelectedJSONFactory(b, name, fields, schema, usages);
    return;
  }
  b.writeln('  factory $name.fromJson(Map<String, dynamic> json) => $name(');
  for (final f in fields) {
    b.writeln('    ${f.name}: ${_jsonCast(f, schema, usages, input: true)},');
  }
  b.writeln('  );');
}

// '$' helper names cannot collide with DSL fields. List validation shares the
// original field pass, while the public unary constructor keeps its signature.
void _writeDartSelectedJSONFactory(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
  b.writeln(
    '  factory $name.fromJson(Map<String, dynamic> json) => $name._\$json(json, false);',
  );
  b.writeln(
    '  static $name \$fromJsonItem(Map<String, dynamic> json) => $name._\$json(json, true);',
  );
  b.writeln(
    '  factory $name._\$json(Map<String, dynamic> json, bool \$requireField) {',
  );
  b.writeln('    var \$hasField = false;');
  for (final (index, field) in fields.indexed) {
    final base = _dartFieldType(field, schema, usages: usages);
    final value = field.nullable ? '$base?' : base;
    b.writeln('    var \$field$index = const Selected<$value>.unselected();');
    b.writeln("    if (json.containsKey('${field.name}')) {");
    b.writeln('      \$hasField = true;');
    b.writeln(
      '      \$field$index = Selected<$value>.value(${_jsonCast(field, schema, usages, input: false)});',
    );
    b.writeln('    }');
  }
  b.writeln(
    "    if (\$requireField && !\$hasField) throw const FormatException('$name list item requires at least one Schema field');",
  );
  b.writeln('    return $name(');
  for (final (index, field) in fields.indexed) {
    b.writeln('      ${field.name}: \$field$index,');
  }
  b.writeln('    );\n  }');
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
  b.writeln('  Map<String, Object?> $method() => {');
  for (final f in fields) {
    final fieldValue = output ? '${f.name}.value' : f.name;
    final condition = output ? 'if (${f.name}.isSelected) ' : '';
    b.writeln(
      "    $condition'${f.name}': ${_jsonWrite(f, schema, fieldValue, parameters: parameters)},",
    );
  }
  b.writeln('  };');
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
  final arenaTerms = arenaFields.map(
    (field) => field.nullable
        ? "(${field.name} == null ? 0 : luxoUtf8Length(${_enumWireValue(field.type, field.typeName, '${field.name}!')}))"
        : 'luxoUtf8Length(${_enumWireValue(field.type, field.typeName, field.name)})',
  );
  b.writeln('\n  @override');
  b.writeln('  void writeLuxo(LuxoEncoder encoder) {');
  b.writeln(
    '    encoder.writeVarint(${arenaTerms.isEmpty ? '0' : arenaTerms.join(' + ')});',
  );
  for (final field in fields) {
    _writeDartBinaryField(b, field, schema);
  }
  b.writeln('    encoder.writeEnd();');
  b.writeln('  }');
}

void _writeDartBinaryField(StringBuffer b, LuxoField field, LuxoSchema schema) {
  b.writeln('    encoder.writeVarint(${field.id});');
  if (!field.nullable) {
    _writeDartBinaryValue(b, field, field.name, schema, '    ');
    return;
  }
  b.writeln('    if (${field.name} == null) {');
  b.writeln('      encoder.writeNull();');
  b.writeln('    } else {');
  b.writeln('      encoder.writePresent();');
  _writeDartBinaryValue(b, field, '${field.name}!', schema, '      ');
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
    b.writeln('${indent}encoder.writeVarint($value.length);');
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
  if (_isNested(field, schema)) {
    b.writeln('${indent}$value.writeLuxo(encoder);');
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
        '${indent}encoder.writeBytes(Uint8List.fromList(utf8.encode(encodeLuxoJSON($value))));',
      );
  }
}

void _writeDartDecoders(
  StringBuffer b,
  String name,
  List<LuxoField> fields,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
  b.writeln('$name decode$name(LuxoDecoder dec) {');
  for (final f in fields) {
    final base = _dartFieldType(f, schema, usages: usages);
    final value = f.nullable ? '$base?' : base;
    b.writeln(
      '  Selected<$value> _${f.name} = const Selected<$value>.unselected();',
    );
  }
  b.writeln('  dec.skipArenaHeader();');
  b.writeln('  while (dec.nextField()) {');
  b.writeln('    switch (dec.fieldID) {');
  for (final f in fields) {
    b.writeln(
      '      case ${f.id}: _${f.name} = Selected.value(${_binaryRead(f)}); break;',
    );
  }
  b.writeln(
    "      default: throw FormatException('unknown $name field ID \${dec.fieldID}');",
  );
  b.writeln('    }');
  b.writeln('  }');
  b.writeln('  dec.checkComplete();');
  b.writeln('  return $name(');
  for (final f in fields) {
    b.writeln('    ${f.name}: _${f.name},');
  }
  b.writeln('  );');
  b.writeln('}\n');

  _writeColumnarDecoder(b, name, fields, schema, usages, false);
  _writeColumnarDecoder(b, name, fields, schema, usages, true);
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
    encoded = field.isList
        ? '$value.map((item) => item.toJson()).toList()'
        : '$value.toJson()';
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
        ? '${field.typeName}.\$rawList($value)'
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
  Map<String, TypeUsage> usages,
  bool paginated,
) {
  final returnType = paginated ? 'Page<$name>' : 'List<$name>';
  final function = paginated ? 'decodePaginated$name' : 'decodeColumnar$name';
  b.writeln('$returnType $function(Uint8List data) {');
  b.writeln('  final dec = ColumnarDecoder(data);');
  for (final f in fields) {
    b.writeln('  ${_columnStorageType(f, schema, usages)}? _${f.name};');
  }
  b.writeln('  while (dec.nextColumn()) {');
  b.writeln('    switch (dec.fieldID) {');
  for (final f in fields) {
    b.writeln(
      '      case ${f.id}: _${f.name} = ${_columnRead(f, schema)}; break;',
    );
  }
  b.writeln(
    "      default: throw FormatException('unknown $name column ID \${dec.fieldID}');",
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
  b.writeln('  final items = List<$name>.generate(dec.count, (i) => $name(');
  for (final f in fields) {
    b.writeln('    ${f.name}: ${_columnValue(f, schema, usages)},');
  }
  b.writeln('  ));');
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
      schema.types.containsKey(name);
}

String _columnStorageType(
  LuxoField f,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
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

String _columnValue(
  LuxoField f,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
  final column = '_${f.name}';
  final typeName = f.typeName ?? f.type;
  final base = _dartFieldType(f, schema, usages: usages);
  final valueType = f.nullable ? '$base?' : base;
  final absent = 'const Selected<$valueType>.unselected()';
  final element = f.nullable ? '$column[i]!' : '$column[i]';
  String value;
  if (_isNested(f, schema)) {
    if (f.isList) {
      final decoded = 'decodeColumnar$typeName($element)';
      value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
    } else {
      final decoded = 'decode$typeName(LuxoDecoder($element))';
      value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
    }
  } else if (f.isList) {
    final decoded = f.type == 'Enum' && f.typeName != null
        ? '${f.typeName}.\$readArray(LuxoDecoder($element), complete: true)'
        : _binaryScalarRead(
            f.type,
            'LuxoDecoder($element)',
            array: true,
            checked: true,
          );
    value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
  } else if (f.type == 'Enum' && f.typeName != null) {
    final decoded = '${f.typeName}.fromValue($element)';
    value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
  } else if (f.type == 'JSON') {
    final decoded = 'decodeLuxoJSON(utf8.decode($element)) as Object';
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
      ? '$read.map((v) => decodeLuxoJSON(utf8.decode(v))).toList()'
      : 'decodeLuxoJSON(utf8.decode($read)) as Object';
}

String _jsonCast(
  LuxoField f,
  LuxoSchema schema,
  Map<String, TypeUsage> usages, {
  required bool input,
}) {
  final n = f.name;
  final q = f.nullable;
  if (f.type == 'Enum' && f.typeName != null) {
    final read = f.isList
        ? '${f.typeName}.\$fromJSONList(json[\'$n\'])'
        : '${f.typeName}.fromValue(luxoStringFromJSON(json[\'$n\']))';
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
      return q
          ? "normalizeLuxoJSON(json['$n'])"
          : "normalizeLuxoJSON(json['$n']) as Object";
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
                  schema.types.containsKey(rawTypeName))
          ? _inputTypeName(rawTypeName, usages)
          : rawTypeName;
      if (f.isList) {
        final read = input ? '$typeName.fromJson' : '$typeName.\$fromJsonItem';
        final cast =
            "(json['$n'] as List).map((e) => $read(e as Map<String, dynamic>)).toList()";
        return q ? "json['$n'] != null ? $cast : null" : cast;
      }
      if (q)
        return "json['$n'] != null ? $typeName.fromJson(json['$n'] as Map<String, dynamic>) : null";
      return "$typeName.fromJson(json['$n'] as Map<String, dynamic>)";
  }
}

String _binaryRead(LuxoField f) {
  final n = f.nullable;
  if (f.type == 'Enum' && f.typeName != null) {
    if (f.isList) {
      final read = '${f.typeName}.\$readArray(dec)';
      return n ? 'dec.readBool() ? $read : null' : read;
    }
    return n
        ? '${f.typeName}.\$readNullable(dec)'
        : '${f.typeName}.fromValue(dec.readString())';
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
      final read = 'decodeLuxoJSON(utf8.decode(dec.readBytes())) as Object';
      return n ? 'dec.readNullable(() => $read)' : read;
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
      final tn = f.typeName ?? f.type;
      final decoded = f.isList
          ? 'decodeColumnar$tn(dec.readBytes())'
          : 'decode$tn(LuxoDecoder(dec.readBytes()))';
      return n ? 'dec.readNullable(() => $decoded)' : decoded;
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
        : (schema.models[api.returnType]?.fields ??
              schema.types[api.returnType]?.fields ??
              const <LuxoField>[]);
    final needsTypes =
        returnFields.isNotEmpty ||
        api.params.any((param) => param.type == 'Model');
    b.write("  '${api.name}': APISchemaEntry(${api.id}");
    if (api.params.isNotEmpty || returnFields.isNotEmpty) {
      b.write(', [\n');
      for (final p in api.params) {
        b.writeln(
          "    ParamSchema(${p.id}, '${p.name}', '${p.type}', ${p.isList}, ${p.nullable}${p.typeName == null ? '' : ", '${p.typeName}'"}),",
        );
      }
      b.write('  ]');
    }
    if (returnFields.isNotEmpty) {
      b.write(", luxoSelectionTypes['${api.returnType}']!");
    } else if (needsTypes) {
      b.write(', const {}');
    }
    if (needsTypes) {
      b.write(', luxoSelectionTypes');
    }
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
  Map<String, TypeUsage> usages,
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
  if (apis.isNotEmpty) b.writeln("import 'dart:typed_data';");
  if (apis.any(
    (api) =>
        api.returnType == 'Bytes' ||
        api.returnType == 'JSON' ||
        api.params.any((param) => param.type == 'JSON'),
  )) {
    b.writeln("import 'dart:convert';");
  }
  b.writeln("import 'package:luxo_client/luxo_client.dart';");
  if (types) b.writeln("import 'types.dart';");
  b.writeln("import 'schema.dart';");
}

String _responseType(LuxoAPI api, LuxoSchema schema) {
  final type = api.returnType ?? 'Int';
  return schema.enums.containsKey(type) ? 'Enum' : type;
}

void _genMethod(
  StringBuffer b,
  LuxoAPI api,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
  final type = _responseType(api, schema);
  final ret = _returnType(api, type);
  if (api.stream) {
    _genStreamMethod(b, api, type, ret, usages);
    return;
  }

  final dec = _decoder(api, type);
  final selection = api.paginated || !_isScalar(type);
  final declarations = _unaryDeclarations(api, usages, selection: selection);
  final args = declarations.isEmpty ? '' : '{${declarations.join(', ')}}';
  b.writeln("  Future<$ret> ${api.name}($args) async {");
  if (selection) {
    b.writeln('    final sel = select;');
  }
  b.writeln(
    "    return transport.call('${api.name}', request: ${_requestCall(api, selection: selection)}, decoder: $dec);",
  );
  b.writeln('  }\n');
}

const _paginationNames = ['page', 'pageSize'];

List<String> _unaryDeclarations(
  LuxoAPI api,
  Map<String, TypeUsage> usages, {
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
  if (api.paginated) {
    for (final name in _paginationNames) {
      if (!api.params.any((param) => param.name == name))
        result.add('LuxoInt64? $name');
    }
  }
  if (selection) result.add('String? select');
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
  if (api.paginated) {
    for (final name in _paginationNames) {
      if (!api.params.any((param) => param.name == name))
        b.writeln("      if ($name != null) '$name': $name,");
    }
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
  Map<String, TypeUsage> usages,
) {
  final methodName =
      'subscribe${api.name[0].toUpperCase()}${api.name.substring(1)}';
  final structuredReturn = !_isScalar(type);
  final declarations = api.params
      .map((param) => _dartParamDeclaration(param, usages))
      .toList();
  if (structuredReturn) declarations.add('String? select');
  declarations.add('required void Function($returnType) onData');
  declarations.add('void Function(LuxoError)? onError');
  declarations.add('void Function()? onDone');
  b.writeln(
    '  Future<void Function()> $methodName({${declarations.join(', ')}}) {',
  );
  b.writeln(
    "    return transport.subscribe('${api.name}', const {}, onData, request: ${_requestCall(api, selection: structuredReturn, stream: true)},",
  );
  b.writeln(
    '      decoder: ${_decoder(api, type)}, onError: onError, onDone: onDone);',
  );
  b.writeln('  }\n');
}

String _dartParamDeclaration(
  LuxoParam param,
  Map<String, TypeUsage> usages, {
  bool forcedOptional = false,
}) {
  final type = _dartParamType(param, usages);
  if (param.nullable && param.hasDefault) {
    return 'LuxoOptional<$type> ${param.name} = const LuxoOptional<$type>.absent()';
  }
  if (forcedOptional || param.hasDefault) {
    return '$type? ${param.name}';
  }
  if (param.nullable) {
    return 'required $type? ${param.name}';
  }
  return 'required $type ${param.name}';
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
      return '${param.typeName}.$method($expression)';
    }
    final checked = nullable ? '$expression?' : expression;
    return '$checked.rawValue';
  }

  if (param.nullable && param.hasDefault) {
    b.writeln(
      "      if (${param.name}.isPresent) '${param.name}': ${value('${param.name}.value', true)},",
    );
    return;
  }
  if (forcedOptional || param.hasDefault) {
    b.writeln(
      "      if (${param.name} != null) '${param.name}': ${value(param.name, false)},",
    );
    return;
  }
  b.writeln("      '${param.name}': ${value(param.name, param.nullable)},");
}

String _returnType(LuxoAPI api, String type) {
  final t = type == 'Enum' && api.returnType != 'Enum'
      ? api.returnType!
      : _luxoTypeToDart(type);
  final result = api.paginated
      ? 'Page<$t>'
      : api.returnList
      ? _dartListType(type, t)
      : t;
  return api.returnNullable ? '$result?' : result;
}

/// Generates a decoder lambda that handles both JSON (Map/int/etc) and binary (Uint8List).
String _decoder(LuxoAPI api, String type) {
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
      return ${_decodeExpression(api, type, 'd')};
    }''';
  }
  return '(d) => ${_decodeExpression(api, type, 'd')}';
}

String _decodeExpression(LuxoAPI api, String t, String value) {
  if (t == 'Enum' && api.returnType != 'Enum') {
    final name = api.returnType!;
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
    return "$value is Uint8List ? decodePaginated$t($value) : Page.fromJson($value as Map<String, dynamic>, $t.\$fromJsonItem)";
  }
  if (api.returnList) {
    return "$value is Uint8List ? decodeColumnar$t($value) : ($value as List).map((e) => $t.\$fromJsonItem(e as Map<String, dynamic>)).toList()";
  }
  return "$value is Uint8List ? decode$t(LuxoDecoder($value)) : $t.fromJson($value as Map<String, dynamic>)";
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
    'JSON' => 'normalizeLuxoJSON($value) as Object',
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
