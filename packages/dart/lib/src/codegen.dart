import 'dart:convert';
import 'dart:io';
import 'types.dart';

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
      json = jsonDecode(body) as Map<String, dynamic>;
    } catch (e) {
      throw Exception('introspection returned invalid JSON: $e');
    }

    final schema = LuxoSchema.fromJson(json);

    final dir = Directory(outDir);
    if (!dir.existsSync()) dir.createSync(recursive: true);

    File('$outDir/types.dart').writeAsStringSync(_genTypes(schema));
    File('$outDir/schema.dart').writeAsStringSync(_genSchemaMap(schema));
    // Placeholder for _select_hints.g.dart — build_runner overwrites with real hints
    final hintsFile = File('$outDir/_select_hints.g.dart');
    if (!hintsFile.existsSync()) {
      hintsFile.writeAsStringSync(
        '// GENERATED placeholder. Run `dart run build_runner build` for real hints.\n'
        'const selectHints = <String, String>{};\n',
      );
    }
    File('$outDir/client.dart').writeAsStringSync(_genClient(schema));
    print('[luxo] Generated types -> $outDir/');
  } finally {
    client.close();
  }
}

String _luxoTypeToDart(String type) {
  switch (type) {
    case 'Int':
    case 'Duration':
      return 'int';
    case 'Float':
      return 'double';
    case 'String':
    case 'DateTime':
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
      'JSON',
    }.contains(type);

String _dartParamType(
  LuxoParam param,
  Map<String, TypeUsage> usages,
) {
  final schemaType =
      param.typeName ?? (usages.containsKey(param.type) ? param.type : null);
  final type = schemaType == null
      ? _luxoTypeToDart(param.type)
      : _inputTypeName(schemaType, usages);
  return param.isList ? 'List<$type>' : type;
}

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
  final structured = schema.models.containsKey(typeName) ||
      schema.types.containsKey(typeName);
  final base = structured && input
      ? _inputTypeName(typeName, usages)
      : f.type == 'Model' || structured
          ? typeName
          : _luxoTypeToDart(f.type);
  if (f.isList) return 'List<$base>';
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
  final b = StringBuffer();
  b.writeln('// Auto-generated by luxo_client. DO NOT EDIT.\n');
  b.writeln("import 'dart:convert';");
  b.writeln("import 'dart:typed_data';");
  b.writeln("import 'package:luxo_client/luxo_client.dart';\n");
  b.writeln("export 'package:luxo_client/src/types.dart' show Page;\n");

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
  b.writeln('class $name {');
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
      b.writeln(
        '    this.${f.name} = const Selected<$value>.unselected(),',
      );
    } else {
      b.writeln(
        f.nullable ? '    this.${f.name},' : '    required this.${f.name},',
      );
    }
  }
  b.writeln('  });\n');
  b.writeln('  factory $name.fromJson(Map<String, dynamic> json) => $name(');
  for (final f in fields) {
    if (output) {
      final base = _dartFieldType(f, schema, usages: usages);
      final value = f.nullable ? '$base?' : base;
      b.writeln(
        "    ${f.name}: json.containsKey('${f.name}') ? Selected<$value>.value(${_jsonCast(f, schema, usages, input: false)}) : const Selected<$value>.unselected(),",
      );
    } else {
      b.writeln(
        '    ${f.name}: ${_jsonCast(f, schema, usages, input: true)},',
      );
    }
  }
  b.writeln('  );');
  b.writeln('  Map<String, dynamic> toJson() => {');
  for (final f in fields) {
    final fieldValue = output ? '${f.name}.value' : f.name;
    final condition = output ? 'if (${f.name}.isSelected) ' : '';
    b.writeln(
      "    $condition'${f.name}': ${_jsonWrite(f, schema, fieldValue)},",
    );
  }
  b.writeln('  };');
  b.writeln('}\n');
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
  b.writeln('  if (dec.error != null) throw FormatException(dec.error!);');
  b.writeln('  return $name(');
  for (final f in fields) {
    b.writeln('    ${f.name}: _${f.name},');
  }
  b.writeln('  );');
  b.writeln('}\n');

  _writeColumnarDecoder(b, name, fields, schema, usages, false);
  _writeColumnarDecoder(b, name, fields, schema, usages, true);
}

String _jsonWrite(LuxoField field, LuxoSchema schema, String fieldValue) {
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
        '      case ${f.id}: _${f.name} = ${_columnRead(f, schema)}; break;');
  }
  b.writeln(
    "      default: throw FormatException('unknown $name column ID \${dec.fieldID}');",
  );
  b.writeln('    }');
  b.writeln('  }');
  b.writeln('  if (dec.error != null) throw FormatException(dec.error!);');
  b.writeln('  final items = List<$name>.generate(dec.count, (i) => $name(');
  for (final f in fields) {
    b.writeln('    ${f.name}: ${_columnValue(f, schema, usages)},');
  }
  b.writeln('  ));');
  if (paginated) {
    b.writeln(
        '  return Page(items: items, total: dec.readSvarint(), page: dec.readSvarint(), pageSize: dec.readSvarint());');
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
    return f.nullable && !f.isList ? 'List<Uint8List?>' : 'List<Uint8List>';
  }
  final base = _dartFieldType(f, schema, usages: usages);
  return f.nullable ? 'List<$base?>' : 'List<$base>';
}

String _columnRead(LuxoField f, LuxoSchema schema) {
  if (_isNested(f, schema) ||
      f.isList ||
      f.type == 'Bytes' ||
      f.type == 'JSON') {
    return f.nullable && !f.isList
        ? 'dec.readColumnBytesPtr()'
        : 'dec.readColumnBytes()';
  }
  return switch (f.type) {
    'Int' ||
    'Duration' =>
      f.nullable ? 'dec.readColumnIntPtr()' : 'dec.readColumnInt()',
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
  String value;
  if (_isNested(f, schema)) {
    if (f.isList) {
      value = 'decodeColumnar$typeName($column[i])';
    } else {
      final element = f.nullable ? '$column[i]!' : '$column[i]';
      final decoded = 'decode$typeName(LuxoDecoder($element))';
      value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
    }
  } else if (f.isList) {
    value = _binaryArrayRead(f.type, 'LuxoDecoder($column[i])');
  } else if (f.type == 'JSON') {
    final element = f.nullable ? '$column[i]!' : '$column[i]';
    final decoded = 'jsonDecode(utf8.decode($element)) as Object';
    value = f.nullable ? '$column[i] == null ? null : $decoded' : decoded;
  } else {
    value = '$column[i]';
  }
  return '$column == null ? $absent : Selected<$valueType>.value($value)';
}

String _binaryArrayRead(String type, String dec) => switch (type) {
      'Int' || 'Duration' => '$dec.readIntArray()',
      'Float' => '$dec.readFloatArray()',
      'Boolean' => '$dec.readBoolArray()',
      'DateTime' => '$dec.readDateTimeArray()',
      'UUID' => '$dec.readUuidArray()',
      'Bytes' => '$dec.readBytesArray()',
      'JSON' =>
        '$dec.readBytesArray().map((v) => jsonDecode(utf8.decode(v)) as Object).toList()',
      _ => '$dec.readStringArray()',
    };

String _jsonCast(
  LuxoField f,
  LuxoSchema schema,
  Map<String, TypeUsage> usages, {
  required bool input,
}) {
  final n = f.name;
  final q = f.nullable;
  // Scalar array field ([T]) — JSON value is a List.
  if (f.isList && _isScalar(f.type)) {
    final elem = _luxoTypeToDart(f.type);
    final cast = switch (f.type) {
      'Float' =>
        "(json['$n'] as List).map((e) => (e as num).toDouble()).toList()",
      'Bytes' =>
        "(json['$n'] as List).map((e) => base64Decode(e as String)).toList()",
      'JSON' => "(json['$n'] as List).cast<Object>()",
      _ => "(json['$n'] as List).cast<$elem>()",
    };
    return q ? "json['$n'] != null ? $cast : null" : cast;
  }
  switch (f.type) {
    case 'Int':
    case 'Duration':
      return q ? "json['$n'] as int?" : "json['$n'] as int";
    case 'Float':
      return q
          ? "(json['$n'] as num?)?.toDouble()"
          : "(json['$n'] as num).toDouble()";
    case 'Boolean':
      return q ? "json['$n'] as bool?" : "json['$n'] as bool";
    case 'Bytes':
      return q
          ? "json['$n'] == null ? null : base64Decode(json['$n'] as String)"
          : "base64Decode(json['$n'] as String)";
    case 'JSON':
      return q ? "json['$n'] as Object?" : "json['$n'] as Object";
    case 'String':
    case 'DateTime':
    case 'UUID':
    case 'Decimal':
    case 'Enum':
      return q ? "json['$n'] as String?" : "json['$n'] as String";
    default:
      // Relation / nested-model field.
      final rawTypeName = f.typeName ?? f.type;
      final typeName = input &&
              (schema.models.containsKey(rawTypeName) ||
                  schema.types.containsKey(rawTypeName))
          ? _inputTypeName(rawTypeName, usages)
          : rawTypeName;
      if (f.isList) {
        final cast =
            "(json['$n'] as List).map((e) => $typeName.fromJson(e as Map<String, dynamic>)).toList()";
        return q ? "json['$n'] != null ? $cast : null" : cast;
      }
      if (q)
        return "json['$n'] != null ? $typeName.fromJson(json['$n'] as Map<String, dynamic>) : null";
      return "$typeName.fromJson(json['$n'] as Map<String, dynamic>)";
  }
}

String _binaryRead(LuxoField f) {
  final n = f.nullable;
  // Scalar array field ([T]) — inline [count][items...] in the row stream.
  if (f.isList && _isScalar(f.type)) {
    switch (f.type) {
      case 'Int':
      case 'Duration':
        return 'dec.readIntArray()';
      // DateTime arrays are svarint(unix seconds) on the wire but surface as
      // List<String> (ISO 8601) to match JSON mode.
      case 'DateTime':
        return 'dec.readDateTimeArray()';
      case 'Float':
        return 'dec.readFloatArray()';
      case 'Boolean':
        return 'dec.readBoolArray()';
      case 'UUID':
        return 'dec.readUuidArray()';
      case 'Bytes':
        return 'dec.readBytesArray()';
      case 'JSON':
        return 'dec.readBytesArray().map((v) => jsonDecode(utf8.decode(v)) as Object).toList()';
      default:
        return 'dec.readStringArray()';
    }
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
      final read = 'jsonDecode(utf8.decode(dec.readBytes())) as Object';
      return n ? 'dec.readNullable(() => $read)' : read;
    // DateTime is svarint(unix seconds) on the binary wire; decode to an
    // ISO 8601 string so the field type matches JSON mode (String).
    case 'DateTime':
      return n ? 'dec.readDateTimePtr()' : 'dec.readDateTime()';
    case 'String':
    case 'Decimal':
    case 'Enum':
      return n ? 'dec.readStringPtr()' : 'dec.readString()';
    default:
      // Nested model — decode recursively
      final tn = f.typeName ?? f.type;
      if (f.isList) return 'dec.readArray(() => decode$tn(dec))';
      return n ? 'dec.readNullable(() => decode$tn(dec))' : 'decode$tn(dec)';
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
      'final luxoSelectionTypes = <String, Map<String, SelectionFieldSchema>>{');
  for (final type in selectionTypes.values) {
    b.write("  '${type.name}': {");
    for (var i = 0; i < type.fields.length; i++) {
      final field = type.fields[i];
      if (i > 0) b.write(', ');
      final typeName = field.typeName ?? field.type;
      final nested = _isNested(field, schema) ? ", '$typeName'" : '';
      b.write("'${field.name}': SelectionFieldSchema(${field.id}$nested)");
    }
    b.writeln('},');
  }
  b.writeln('};\n');
  b.writeln(
      '/// API schema map — used by binary transport for encoding requests.');
  b.writeln('final luxoSchema = <String, APISchemaEntry>{');
  for (final api in schema.apis.values) {
    if (api.name.startsWith('svc:')) continue;
    final returnFields = api.returnType == null
        ? const <LuxoField>[]
        : (schema.models[api.returnType]?.fields ??
            schema.types[api.returnType]?.fields ??
            const <LuxoField>[]);
    b.write("  '${api.name}': APISchemaEntry(${api.id}");
    if (api.params.isNotEmpty || returnFields.isNotEmpty) {
      b.write(', [\n');
      for (final p in api.params) {
        b.writeln(
          "    ParamSchema(${p.id}, '${p.name}', '${p.type}', ${p.isList}, ${p.nullable}),",
        );
      }
      b.write('  ]');
    }
    if (returnFields.isNotEmpty) {
      b.write(", luxoSelectionTypes['${api.returnType}']!, luxoSelectionTypes");
    }
    b.writeln('),');
  }
  b.writeln('};');
  return b.toString();
}

String _genClient(LuxoSchema schema) {
  final usages = _inferTypeUsages(schema);
  final b = StringBuffer();
  b.writeln('// Auto-generated by luxo_client. DO NOT EDIT.\n');
  b.writeln("import 'dart:typed_data';");
  b.writeln("import 'package:luxo_client/luxo_client.dart';");
  b.writeln("import 'types.dart';");
  b.writeln("import 'schema.dart';");
  b.writeln("import '_select_hints.g.dart' as hints show selectHints;\n");
  b.writeln('class LuxoClient {');
  b.writeln('  final Transport transport;');
  b.writeln('  static final schema = luxoSchema;\n');
  b.writeln('  LuxoClient(Transport transport) : transport = transport {');
  b.writeln('    transport.setSchema(luxoSchema);');
  b.writeln('  }\n');
  b.writeln('  /// Create client from URL — auto-detects HTTP vs WebSocket.');
  b.writeln(
      "  factory LuxoClient.create(String endpoint, {TransportOptions? options}) {");
  b.writeln(
      "    final transport = endpoint.startsWith('ws') ? WsTransport(endpoint, options: options) : HttpTransport(endpoint, options: options);");
  b.writeln('    return LuxoClient(transport);');
  b.writeln('  }\n');
  b.writeln('  void setMode(TransportMode mode) => transport.setMode(mode);');
  b.writeln('  void setToken(String token) => transport.setToken(token);');
  b.writeln('  void close() => transport.close();\n');
  b.writeln('  String? _hint(String api) => hints.selectHints[api];\n');

  for (final api in schema.apis.values) {
    if (api.name.startsWith('svc:')) continue;
    _genMethod(b, api, usages);
  }

  b.writeln('}');
  return b.toString();
}

void _genMethod(
  StringBuffer b,
  LuxoAPI api,
  Map<String, TypeUsage> usages,
) {
  final ret = _returnType(api);
  final dec = _decoder(api);
  final structuredReturn =
      api.returnType != null && !_isScalar(api.returnType!);
  if (api.stream) {
    _genStreamMethod(b, api, ret, usages);
    return;
  }

  // If API has explicit params, always use them (no guessing by name prefix)
  if (api.params.isNotEmpty) {
    // page/pageSize are always optional for paginated APIs.
    final paginationNames = {'page', 'pageSize'};
    final isPaginated = api.paginated;
    final paramParts = api.params.map((p) {
      final forcedOptional = isPaginated && paginationNames.contains(p.name);
      return _dartParamDeclaration(
        p,
        usages,
        forcedOptional: forcedOptional,
      );
    }).toList();

    if (api.paginated) {
      final existingNames = api.params.map((p) => p.name).toSet();
      if (!existingNames.contains('page')) paramParts.add('int? page');
      if (!existingNames.contains('pageSize')) paramParts.add('int? pageSize');
      paramParts.add('String? select');
      paramParts.add('List<LuxoFilter>? filters');
      paramParts.add('List<LuxoSorter>? sorters');
      b.writeln(
          "  Future<$ret> ${api.name}({${paramParts.join(', ')}}) async {");
      b.writeln("    final sel = select ?? _hint('${api.name}');");
      b.writeln("    return transport.call('${api.name}', params: {");
      for (final p in api.params) {
        _writeDartParamEntry(
          b,
          p,
          forcedOptional: paginationNames.contains(p.name),
        );
      }
      if (!existingNames.contains('page')) {
        b.writeln("      if (page != null) 'page': page,");
      }
      if (!existingNames.contains('pageSize')) {
        b.writeln("      if (pageSize != null) 'pageSize': pageSize,");
      }
      b.writeln("      if (sel != null) r'\$select': sel,");
      b.writeln("      if (filters != null) r'\$filters': filters,");
      b.writeln("      if (sorters != null) r'\$sorters': sorters,");
      b.writeln('    }, decoder: $dec);');
      b.writeln('  }\n');
      return;
    }

    if (structuredReturn) {
      paramParts.add('String? select');
      b.writeln(
          "  Future<$ret> ${api.name}({${paramParts.join(', ')}}) async {");
      b.writeln("    final sel = select ?? _hint('${api.name}');");
      b.writeln("    return transport.call('${api.name}', params: {");
      for (final p in api.params) {
        _writeDartParamEntry(b, p);
      }
      b.writeln("      if (sel != null) r'\$select': sel,");
      b.writeln('    }, decoder: $dec);');
      b.writeln('  }\n');
      return;
    }

    // Generic params — all other APIs with explicit params
    b.writeln("  Future<$ret> ${api.name}({${paramParts.join(', ')}}) async {");
    b.writeln("    return transport.call('${api.name}', params: {");
    for (final p in api.params) {
      _writeDartParamEntry(b, p);
    }
    b.writeln('    }, decoder: $dec);');
    b.writeln('  }\n');
    return;
  }

  if (api.paginated) {
    b.writeln(
        "  Future<$ret> ${api.name}({int? page, int? pageSize, String? select, List<LuxoFilter>? filters, List<LuxoSorter>? sorters}) async {");
    b.writeln("    final sel = select ?? _hint('${api.name}');");
    b.writeln("    return transport.call('${api.name}', params: {");
    b.writeln("      if (page != null) 'page': page,");
    b.writeln("      if (pageSize != null) 'pageSize': pageSize,");
    b.writeln("      if (sel != null) r'\$select': sel,");
    b.writeln("      if (filters != null) r'\$filters': filters,");
    b.writeln("      if (sorters != null) r'\$sorters': sorters,");
    b.writeln('    }, decoder: $dec);');
    b.writeln('  }\n');
    return;
  }
  if (structuredReturn) {
    b.writeln("  Future<$ret> ${api.name}({String? select}) async {");
    b.writeln("    final sel = select ?? _hint('${api.name}');");
    b.writeln("    return transport.call('${api.name}', params: {");
    b.writeln("      if (sel != null) r'\$select': sel,");
    b.writeln('    }, decoder: $dec);');
    b.writeln('  }\n');
    return;
  }

  b.writeln("  Future<$ret> ${api.name}() async {");
  b.writeln("    return transport.call('${api.name}', decoder: $dec);");
  b.writeln('  }\n');
}

void _genStreamMethod(
  StringBuffer b,
  LuxoAPI api,
  String returnType,
  Map<String, TypeUsage> usages,
) {
  final methodName =
      'subscribe${api.name[0].toUpperCase()}${api.name.substring(1)}';
  final structuredReturn =
      api.returnType != null && !_isScalar(api.returnType!);
  final declarations =
      api.params.map((param) => _dartParamDeclaration(param, usages)).toList();
  if (structuredReturn) declarations.add('String? select');
  declarations.add('required void Function($returnType) onData');
  b.writeln(
    '  Future<void Function()> $methodName({${declarations.join(', ')}}) {',
  );
  b.writeln("    return transport.subscribe('${api.name}', {");
  for (final param in api.params) {
    _writeDartParamEntry(b, param);
  }
  if (structuredReturn) {
    b.writeln("      if (select != null) r'\$select': select,");
  }
  b.writeln('    }, (d) => onData(${_decodeExpression(api, 'd')}));');
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
  if (param.nullable && param.hasDefault) {
    b.writeln(
      "      if (${param.name}.isPresent) '${param.name}': ${param.name}.value,",
    );
    return;
  }
  if (forcedOptional || param.hasDefault) {
    b.writeln(
      "      if (${param.name} != null) '${param.name}': ${param.name},",
    );
    return;
  }
  b.writeln("      '${param.name}': ${param.name},");
}

String _returnType(LuxoAPI api) {
  if (api.returnType == null) return 'int';
  final t = _luxoTypeToDart(api.returnType!);
  if (api.paginated) return 'Page<$t>';
  if (api.returnList) return 'List<$t>';
  return t;
}

/// Generates a decoder lambda that handles both JSON (Map/int/etc) and binary (Uint8List).
String _decoder(LuxoAPI api) {
  return '(d) => ${_decodeExpression(api, 'd')}';
}

String _decodeExpression(LuxoAPI api, String value) {
  if (api.returnType == null) {
    return '$value is Uint8List ? LuxoDecoder($value).readInt() : $value as int';
  }
  final t = api.returnType!;
  if (_isScalar(t)) {
    final dartType = _luxoTypeToDart(t);
    final binaryScalarRead = switch (t) {
      'Float' => 'LuxoDecoder($value).readFloat()',
      'Boolean' => 'LuxoDecoder($value).readBool()',
      'Int' || 'Duration' => 'LuxoDecoder($value).readInt()',
      'DateTime' => 'LuxoDecoder($value).readDateTime()',
      'UUID' => 'LuxoDecoder($value).readUuid()',
      'Bytes' => 'LuxoDecoder($value).readBytes()',
      'JSON' =>
        'jsonDecode(utf8.decode(LuxoDecoder($value).readBytes())) as Object',
      _ => 'LuxoDecoder($value).readString()',
    };
    final binaryListRead = switch (t) {
      'Float' => 'LuxoDecoder($value).readFloatArray()',
      'Boolean' => 'LuxoDecoder($value).readBoolArray()',
      'Int' || 'Duration' => 'LuxoDecoder($value).readIntArray()',
      'DateTime' => 'LuxoDecoder($value).readDateTimeArray()',
      'UUID' => 'LuxoDecoder($value).readUuidArray()',
      'Bytes' => 'LuxoDecoder($value).readBytesArray()',
      'JSON' =>
        'LuxoDecoder($value).readBytesArray().map((v) => jsonDecode(utf8.decode(v)) as Object).toList()',
      _ => 'LuxoDecoder($value).readStringArray()',
    };
    if (api.returnList) {
      final jsonRead = t == 'Float'
          ? '($value as List).map((e) => (e as num).toDouble()).toList()'
          : t == 'Bytes'
              ? '($value as List).map((e) => base64Decode(e as String)).toList()'
              : '($value as List).cast<$dartType>()';
      return '$value is Uint8List ? $binaryListRead : $jsonRead';
    }
    final jsonRead = t == 'Float'
        ? '($value as num).toDouble()'
        : t == 'Bytes'
            ? 'base64Decode($value as String)'
            : '$value as $dartType';
    return '$value is Uint8List ? $binaryScalarRead : $jsonRead';
  }
  if (api.paginated) {
    return "$value is Uint8List ? decodePaginated$t($value) : Page.fromJson($value as Map<String, dynamic>, (e) => $t.fromJson(e))";
  }
  if (api.returnList) {
    return "$value is Uint8List ? decodeColumnar$t($value) : ($value as List).map((e) => $t.fromJson(e as Map<String, dynamic>)).toList()";
  }
  return "$value is Uint8List ? decode$t(LuxoDecoder($value)) : $t.fromJson($value as Map<String, dynamic>)";
}
