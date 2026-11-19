part of 'codegen.dart';

final class _SealedField extends LuxoField {
  final String source;
  _SealedField(LuxoParam field)
    : source = _sealedParamSource(field),
      super(
        id: field.id,
        name: field.name,
        type: field.type,
        typeName: field.typeName,
        nullable: field.nullable,
        isList: field.isList,
      );
}

LuxoField _sealedField(LuxoParam field) => _SealedField(field);

String _dartMember(LuxoField field) =>
    field is _SealedField ? field.source : dartSourceIdentifier(field.name);

const _sealedReserved = {
  'fromJson',
  'toJson',
  'writeLuxo',
  'toLuxoParameters',
  'encoder',
};

String _sealedParamSource(LuxoParam field) {
  final source = dartSourceIdentifier(field.name);
  return source.startsWith('_') ||
          _enumReserved.contains(source) ||
          _sealedReserved.contains(source)
      ? '\$field${field.id}'
      : source;
}

String _sealedFactoryName(String raw, String root) {
  final source = _enumMember(raw, root);
  return _sealedReserved.contains(source) || source == root
      ? '$source\$'
      : source;
}

String _sealedParamDeclaration(LuxoParam field, _TypeUsages usages) {
  final declaration = _dartParamDeclaration(field, usages);
  final end = declaration.indexOf(' = ');
  final length = end < 0 ? declaration.length : end;
  final old = dartSourceIdentifier(field.name);
  return declaration.replaceRange(
    length - old.length,
    length,
    _sealedParamSource(field),
  );
}

void _validateSealedDeclarations(LuxoSchema schema) {
  for (final entry in schema.sealeds.entries) {
    final owner = entry.value;
    _validateDeclarationName(entry.key, owner.name);
    if (schema.models.containsKey(owner.name) ||
        schema.types.containsKey(owner.name) ||
        schema.enums.containsKey(owner.name)) {
      throw FormatException('duplicate type ${owner.name}');
    }
    final cases = <String>{};
    for (final variant in owner.variants) {
      _validateMemberName(variant.name, cases);
      _validateFieldReferences(
        schema,
        '${owner.name}.${variant.name}',
        variant.fields.map(_sealedField).toList(),
      );
    }
  }
}

String _sealedCaseName(String root, LuxoSealedVariant variant) =>
    '$root\$${dartSourceIdentifier(variant.name)}\$${variant.id}';

void _writeDartSealeds(StringBuffer b, LuxoSchema schema, _TypeUsages usages) {
  for (final owner in schema.sealeds.values) {
    final usage = usages[owner.name] ?? TypeUsage.unused;
    if (_hasOutputUsage(usage)) {
      final name = dartTypeIdentifier(owner.name);
      _writeSealedRoot(b, owner, name, schema, usages, input: false);
      for (final variant in owner.variants) {
        _writeSealedOutputCase(b, name, variant, schema, usages);
      }
      _writeSealedDecoder(b, owner, name, schema, usages);
    }
    if (_hasInputUsage(usage)) {
      final name = _inputTypeName(owner.name, usages);
      _writeSealedRoot(b, owner, name, schema, usages, input: true);
      for (final variant in owner.variants) {
        _writeSealedInputCase(b, name, variant, schema, usages);
      }
    }
  }
}

void _writeSealedRoot(
  StringBuffer b,
  LuxoSealed owner,
  String name,
  LuxoSchema schema,
  _TypeUsages usages, {
  required bool input,
}) {
  if (!input)
    b.writeln(
      "@LuxoSealedProjection('${owner.name}', ${jsonEncode(owner.variants.map((variant) => variant.name).toList())})",
    );
  b.writeln(
    'sealed class $name implements '
    '${input ? 'LuxoBinaryEncodable, LuxoJSONParameters, ' : ''}'
    r'$jsonCodec.LuxoSealedJSONWritable {',
  );
  if (owner.variants.isNotEmpty) b.writeln('  const $name._();');
  b.writeln("  @override\n  String get \$sealedType => '${owner.name}';");
  b.writeln(r'  @override');
  b.writeln(
    r'  void $writeSealedJSON($jsonCodec.LuxoJSONOutput output, int depth) => _$writeJSON(output, depth);',
  );
  if (input) {
    for (final variant in owner.variants) {
      final declarations = variant.fields
          .map(
            (field) =>
                _sealedParamDeclaration(field, usages).split(' = ').first,
          )
          .join(', ');
      final args = declarations.isEmpty ? '' : '{$declarations}';
      b.writeln(
        '  factory $name.${_sealedFactoryName(variant.name, name)}($args) = '
        '${_sealedCaseName(name, variant)};',
      );
    }
    b.writeln(
      '  @override\n  void writeLuxo(LuxoEncoder encoder) => _\$writeLuxo(encoder, 0);',
    );
    b.writeln(r'  void _$writeLuxo(LuxoEncoder encoder, int $depth);');
    b.writeln(
      '  @override\n  Map<String, Object?> toLuxoParameters() => _\$toParameters(0);',
    );
    b.writeln(r'  Map<String, Object?> _$toParameters(int $depth);');
  }
  b.writeln('  @override\n  Map<String, Object?> toJson() => _\$toJson(0);');
  b.writeln(r'  Map<String, Object?> _$toJson(int $depth);');
  b.writeln(r'  @override');
  b.writeln(
    r'  void $writeJSON($jsonCodec.LuxoJSONOutput $out) => _$writeJSON($out, 0);',
  );
  b.writeln(r'  void _$writeJSON($jsonCodec.LuxoJSONOutput $out, int $depth);');
  final suffix = input ? '' : 'bool \$requireField, ';
  final args = input ? '0' : 'false, 0';
  b.writeln(
    '  factory $name.fromJson(Map<String, dynamic> json) => $name._\$json(json, $args);',
  );
  b.writeln(
    '  static $name _\$json(Map<String, dynamic> json, ${suffix}int \$depth) {',
  );
  _writeDartModelDepthGuard(b, '    ');
  b.writeln(
    "    if (json.length != 1) throw const FormatException('sealed ${owner.name} requires exactly one case');",
  );
  b.writeln('    final entry = json.entries.single;');
  b.writeln(
    "    if (entry.value is! Map) throw const FormatException('sealed payload must be an object');",
  );
  if (owner.variants.isNotEmpty)
    b.writeln(
      '    final payload = entry.value is Map<String, dynamic> ? entry.value as Map<String, dynamic> : (entry.value as Map).cast<String, dynamic>();',
    );
  b.writeln('    switch (entry.key) {');
  for (final variant in owner.variants) {
    final readArgs = input ? '\$depth + 1' : 'false, \$depth + 1';
    b.writeln(
      "      case '${variant.name}': return ${_sealedCaseName(name, variant)}._\$json(payload, $readArgs);",
    );
  }
  b.writeln(
    "      default: throw FormatException('unknown sealed ${owner.name} case \${entry.key}');",
  );
  b.writeln('    }\n  }\n}\n');
}

void _writeSealedOutputCase(
  StringBuffer b,
  String root,
  LuxoSealedVariant variant,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  final name = _sealedCaseName(root, variant);
  final fields = variant.fields.map(_sealedField).toList();
  b.writeln(
    "@LuxoSealedCaseProjection('${wireIdentifier(root)}', '${variant.name}', ${variant.id})",
  );
  b.writeln('final class $name extends $root {');
  for (final field in fields) {
    final type = _dartFieldValueType(field, schema, usages: usages);
    if (_dartMember(field) != dartSourceIdentifier(field.name)) {
      b.writeln("  @LuxoSelectionFieldProjection('${field.name}')");
    }
    b.writeln('  final Selected<$type> ${_dartMember(field)};');
  }
  b.writeln('  const $name(${fields.isEmpty ? '' : '{'}');
  for (final field in fields) {
    final type = _dartFieldValueType(field, schema, usages: usages);
    b.writeln(
      '    this.${_dartMember(field)} = const Selected<$type>.unselected(),',
    );
  }
  b.writeln('  ${fields.isEmpty ? '' : '}'}): super._();');
  _writeDartSelectedJSONFactory(
    b,
    name,
    fields,
    schema,
    usages,
    sealedPayload: true,
  );
  _writeSealedPayloadProjection(b, fields, schema, output: true);
  _writeSealedEnvelopeMethods(b, variant.name);
  b.writeln('}\n');
}

void _writeSealedPayloadProjection(
  StringBuffer b,
  List<LuxoField> fields,
  LuxoSchema schema, {
  required bool output,
}) {
  final payload = StringBuffer();
  _writeDartJSONProjection(payload, fields, schema, output: output);
  _writeDartNativeJSONWriter(payload, fields, schema, output: output);
  b.write(
    payload
        .toString()
        .replaceAll('  @override\n', '')
        .replaceAll(
          r'toJson() => _$toJson(0)',
          r'$payloadJson() => _$payloadJSON(0)',
        )
        .replaceAll(
          r'Map<String, Object?> _$toJson(int',
          r'Map<String, Object?> _$payloadJSON(int',
        )
        .replaceAll(
          r'$writeJSON($jsonCodec.LuxoJSONOutput $out) => _$writeJSON($out, 0)',
          r'$writePayloadJSON($jsonCodec.LuxoJSONOutput $out) => _$writePayloadJSON($out, 0)',
        )
        .replaceAll(
          r'void _$writeJSON($jsonCodec.LuxoJSONOutput $out, int',
          r'void _$writePayloadJSON($jsonCodec.LuxoJSONOutput $out, int',
        ),
  );
}

void _writeSealedEnvelopeMethods(StringBuffer b, String variant) {
  b.writeln('  @override\n  Map<String, Object?> _\$toJson(int \$depth) {');
  _writeDartModelDepthGuard(b, '    ');
  b.writeln("    return {'$variant': _\$payloadJSON(\$depth + 1)};\n  }");
  b.writeln(r'  @override');
  b.writeln(
    r'  void _$writeJSON($jsonCodec.LuxoJSONOutput $out, int $depth) {',
  );
  _writeDartModelDepthGuard(b, '    ');
  _writeDartJSONRaw(b, '{${jsonEncode(variant)}:', '    ');
  b.writeln(r'    _$writePayloadJSON($out, $depth + 1);');
  _writeDartJSONRaw(b, '}', '    ');
  b.writeln('  }');
}

void _writeSealedInputCase(
  StringBuffer b,
  String root,
  LuxoSealedVariant variant,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  final name = _sealedCaseName(root, variant);
  b.writeln('final class $name extends $root {');
  for (final param in variant.fields) {
    final base = _dartParamType(param, usages);
    final type = param.nullable && param.hasDefault
        ? 'LuxoOptional<$base>'
        : '$base${param.nullable || param.hasDefault ? '?' : ''}';
    b.writeln('  final $type ${_sealedParamSource(param)};');
  }
  final declarations = variant.fields
      .map((field) {
        final declaration = _sealedParamDeclaration(field, usages);
        final source = _sealedParamSource(field);
        final type = _dartParamType(field, usages);
        final typed = field.nullable && field.hasDefault
            ? 'LuxoOptional<$type>'
            : '$type${field.nullable || field.hasDefault ? '?' : ''}';
        return declaration.replaceFirst('$typed $source', 'this.$source');
      })
      .join(', ');
  b.writeln(
    '  const $name(${declarations.isEmpty ? '' : '{$declarations}'}): super._();',
  );
  _writeSealedInputJSONFactory(b, name, variant, schema, usages);
  _writeSealedInputProjection(b, variant, schema);
  _writeSealedEnvelopeMethods(b, variant.name);
  _writeSealedInputParameters(b, variant, schema);
  _writeSealedInputBinary(b, variant, schema);
  b.writeln('}\n');
}

String _sealedInputValue(LuxoParam param) =>
    _sealedParamSource(param) +
    (param.nullable && param.hasDefault ? '.value' : '');

String? _sealedInputCondition(LuxoParam param) => !param.hasDefault
    ? null
    : '${_sealedParamSource(param)}${param.nullable ? '.isPresent' : ' != null'}';

void _writeSealedInputJSONFactory(
  StringBuffer b,
  String name,
  LuxoSealedVariant variant,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  b.writeln('  static $name _\$json(Map<String, dynamic> json, int \$depth) {');
  _writeDartModelDepthGuard(b, '    ');
  _writeSealedJSONKeys(b, variant.fields.map(_sealedField).toList());
  for (final param in variant.fields) {
    if (!param.hasDefault)
      b.writeln(
        "    if (!json.containsKey('${param.name}')) throw const FormatException('missing required ${variant.name}.${param.name}');",
      );
  }
  for (final (index, param) in variant.fields.indexed) {
    final base = _dartParamType(param, usages);
    final type = param.nullable && param.hasDefault
        ? 'LuxoOptional<$base>'
        : '$base${param.nullable || param.hasDefault ? '?' : ''}';
    b.writeln('    final $type \$field$index;');
    if (param.hasDefault)
      b.writeln("    if (json.containsKey('${param.name}')) {");
    final read = _writeDartJSONRead(
      b,
      _sealedField(param),
      schema,
      usages,
      index,
      input: true,
    );
    final value = param.nullable && param.hasDefault
        ? 'LuxoOptional<$base>.present($read)'
        : read;
    b.writeln('      \$field$index = $value;');
    if (param.hasDefault) {
      final absent = param.nullable
          ? 'const LuxoOptional<$base>.absent()'
          : 'null';
      b.writeln('    } else { \$field$index = $absent; }');
    }
  }
  b.writeln('    return $name(');
  for (final (index, param) in variant.fields.indexed) {
    b.writeln('      ${_sealedParamSource(param)}: \$field$index,');
  }
  b.writeln('    );\n  }');
}

void _writeSealedJSONKeys(StringBuffer b, List<LuxoField> fields) {
  b.writeln('    for (final key in json.keys) {');
  b.writeln('      switch (key) {');
  for (final field in fields) {
    b.writeln("        case '${field.name}': break;");
  }
  b.writeln(
    r"        default: throw FormatException('unknown sealed payload field $key');",
  );
  b.writeln('      }\n    }');
}

void _writeSealedInputProjection(
  StringBuffer b,
  LuxoSealedVariant variant,
  LuxoSchema schema,
) {
  b.writeln(r'  Map<String, Object?> _$payloadJSON(int $depth) {');
  _writeDartModelDepthGuard(b, '    ');
  b.writeln(r'    final $json = <String, Object?>{};');
  for (final (index, param) in variant.fields.indexed) {
    final condition = _sealedInputCondition(param);
    if (condition != null) b.writeln('    if ($condition) {');
    final field = _sealedField(param);
    final source =
        _sealedInputValue(param) +
        (param.hasDefault && !param.nullable ? '!' : '');
    final read = _writeDartProjectionValue(
      b,
      field,
      schema,
      source,
      index,
      '      ',
    );
    b.writeln("      \$json['${param.name}'] = $read;");
    if (condition != null) b.writeln('    }');
  }
  b.writeln(r'    return $json;');
  b.writeln('  }');
  b.writeln(
    r'  void _$writePayloadJSON($jsonCodec.LuxoJSONOutput $out, int $depth) {',
  );
  _writeDartModelDepthGuard(b, '    ');
  _writeDartJSONRaw(b, '{', '    ');
  if (variant.fields.length > 1) b.writeln(r'    var $first = true;');
  for (final (index, param) in variant.fields.indexed) {
    final condition = _sealedInputCondition(param);
    if (condition != null) b.writeln('    if ($condition) {');
    if (index != 0) b.writeln(r"      if (!$first) $out.writeRaw(',');");
    if (index + 1 < variant.fields.length) b.writeln(r'      $first = false;');
    _writeDartJSONRaw(b, '${jsonEncode(param.name)}:', '      ');
    final value =
        _sealedInputValue(param) +
        (param.hasDefault && !param.nullable ? '!' : '');
    _writeDartNativeJSONValue(b, _sealedField(param), schema, value, '      ');
    if (condition != null) b.writeln('    }');
  }
  _writeDartJSONRaw(b, '}', '    ');
  b.writeln('  }');
}

void _writeSealedInputBinary(
  StringBuffer b,
  LuxoSealedVariant variant,
  LuxoSchema schema,
) {
  b.writeln(r'  @override');
  b.writeln(r'  void _$writeLuxo(LuxoEncoder encoder, int $depth) {');
  _writeDartModelDepthGuard(b, '    ');
  b.writeln('    encoder.writeVarint(${variant.id});');
  b.writeln(r'    encoder.writeDelimited((encoder) {');
  b.writeln(
    r'      if ($depth + 1 >= 64) throw const FormatException("Model nesting exceeds 64 levels");',
  );
  // Payload text values are ordinary row arena entries; omitted values add none.
  final arena = <String>[];
  for (final param in variant.fields) {
    if (param.isList || (param.type != 'String' && param.type != 'Enum'))
      continue;
    final value = _sealedInputValue(param);
    final scalar = _enumWireValue(
      param.type,
      param.typeName,
      param.nullable || param.hasDefault ? '$value!' : value,
    );
    final condition = _sealedInputCondition(param);
    final present = [
      if (condition != null) condition,
      if (param.nullable) '$value != null',
    ].join(' && ');
    arena.add(
      present.isEmpty
          ? 'luxoUtf8Length($scalar)'
          : '($present ? luxoUtf8Length($scalar) : 0)',
    );
  }
  b.writeln(
    '      encoder.writeVarint(${arena.isEmpty ? '0' : arena.join(' + ')});',
  );
  for (final param in variant.fields) {
    final condition = _sealedInputCondition(param);
    if (condition != null) b.writeln('      if ($condition) {');
    b.writeln('      encoder.writeVarint(${param.id});');
    final field = _sealedField(param);
    var value = _sealedInputValue(param);
    if (param.nullable) {
      b.writeln('      if ($value == null) { encoder.writeNull(); } else {');
      b.writeln('        encoder.writePresent();');
      value += '!';
    } else if (param.hasDefault) {
      value += '!';
    }
    final payload = StringBuffer();
    _writeDartBinaryValue(payload, field, value, schema, '      ');
    b.write(payload.toString().replaceAll(r'$depth + 1', r'$depth + 2'));
    if (param.nullable) b.writeln('      }');
    if (condition != null) b.writeln('      }');
  }
  b.writeln('      encoder.writeEnd();\n    });\n  }');
}

void _writeSealedDecoder(
  StringBuffer b,
  LuxoSealed owner,
  String name,
  LuxoSchema schema,
  _TypeUsages usages,
) {
  b.writeln('$name decode$name(LuxoDecoder dec) => _\$decode$name(dec, 0);');
  b.writeln('$name _\$decode$name(LuxoDecoder dec, int \$depth) {');
  _writeDartModelDepthGuard(b, '  ');
  b.writeln('  final tag = dec.readUnsigned();');
  b.writeln(
    owner.variants.isEmpty
        ? '  dec.readBytes();'
        : '  final payload = dec.readBytes();',
  );
  b.writeln('  dec.checkComplete();');
  b.writeln('  switch (tag) {');
  for (final variant in owner.variants) {
    final caseName = _sealedCaseName(name, variant);
    b.writeln(
      '    case ${variant.id}: return _\$decode$caseName(LuxoDecoder(payload), \$depth + 1);',
    );
  }
  b.writeln(
    "    default: throw FormatException('unknown sealed ${owner.name} tag \$tag');",
  );
  b.writeln('  }\n}\n');
  for (final variant in owner.variants) {
    final decoded = StringBuffer();
    final caseName = _sealedCaseName(name, variant);
    _writeDartDecoders(
      decoded,
      caseName,
      variant.fields.map(_sealedField).toList(),
      schema,
      usages,
      rowOnly: true,
    );
    b.write(decoded);
  }
  b.writeln(
    'List<$name> decodeSealedList$name(Uint8List bytes) => _\$decodeSealedList$name(bytes, 0);',
  );
  b.writeln(
    'List<$name> _\$decodeSealedList$name(Uint8List bytes, int \$depth) {',
  );
  b.writeln('  final dec = LuxoDecoder(bytes);');
  b.writeln('  final result = _\$readSealedList$name(dec, \$depth);');
  b.writeln('  dec.checkComplete();\n  return result;\n}');
  b.writeln(
    'List<$name> _\$readSealedList$name(LuxoDecoder dec, int \$depth) {',
  );
  b.writeln('  final count = dec.readArrayCount();');
  b.writeln(
    '  return List<$name>.generate(count, (_) => _\$decode$name(LuxoDecoder(dec.readBytes()), \$depth));\n}\n',
  );
}

void _writeSealedInputParameters(
  StringBuffer b,
  LuxoSealedVariant variant,
  LuxoSchema schema,
) {
  b.writeln(r'  @override');
  b.writeln(r'  Map<String, Object?> _$toParameters(int $depth) {');
  _writeDartModelDepthGuard(b, '    ');
  b.writeln(
    r'    if ($depth + 1 >= 64) throw const FormatException("Model nesting exceeds 64 levels");',
  );
  b.writeln("    return {'${variant.name}': <String, Object?>{");
  for (final param in variant.fields) {
    final condition = _sealedInputCondition(param);
    final value =
        _sealedInputValue(param) +
        (param.hasDefault && !param.nullable ? '!' : '');
    final encoded = _jsonWrite(
      _sealedField(param),
      schema,
      value,
      parameters: true,
    );
    b.writeln(
      "      ${condition == null ? '' : 'if ($condition) '}'${param.name}': $encoded,",
    );
  }
  b.writeln('    }};\n  }');
}
