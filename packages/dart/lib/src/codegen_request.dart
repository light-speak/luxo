part of 'codegen.dart';

String _requestCall(
  LuxoAPI api, {
  required bool selection,
  bool stream = false,
}) {
  final args = api.params.map((p) => '${p.name}: ${p.name}').toList();
  if (selection) args.add('sel: ${stream ? 'select' : 'sel'}');
  if (api.paginated) {
    for (final name in _paginationNames) {
      if (!api.params.any((p) => p.name == name)) args.add('$name: $name');
    }
    args.addAll(['filters: filters', 'sorters: sorters']);
  }
  return '_${api.name}Request(${args.join(', ')})';
}

void _genRequest(
  StringBuffer b,
  LuxoAPI api,
  LuxoSchema schema,
  Map<String, TypeUsage> usages,
) {
  final selection = api.paginated || !_isScalar(_responseType(api, schema));
  final (fields, declarations) = _requestFields(api, usages, selection);
  final stored = {
    for (final (index, name) in fields.keys.indexed) name: '_p$index',
  };
  b.writeln('\nclass _${api.name}Request extends GeneratedRequest {');
  for (final entry in fields.entries) {
    b.writeln('  final ${entry.value} ${stored[entry.key]};');
  }
  final args = declarations.isEmpty ? '' : '{${declarations.join(', ')}}';
  final init = fields.isEmpty
      ? ''
      : ' : ${fields.keys.map((name) => '${stored[name]} = $name').join(', ')}';
  b.writeln('  _${api.name}Request($args)$init;');
  b.writeln('  @override\n  int get apiID => ${api.id};');
  _writeRequestAccessors(b, api, selection, stored);
  b.writeln('  @override\n  Map<String, dynamic> toJSONParameters() {');
  for (final name in fields.keys) {
    b.writeln('    final $name = ${stored[name]};');
  }
  b.writeln('    return {');
  _writeUnaryParams(b, api, selection: selection);
  b.writeln('    };\n  }');
  b.writeln('  @override\n  void writeParameters(LuxoEncoder encoder) {');
  for (final p in api.params) {
    _writeRequestParam(b, p, api, schema, stored[p.name]!);
  }
  b.writeln('  }\n}');
}

(Map<String, String>, List<String>) _requestFields(
  LuxoAPI api,
  Map<String, TypeUsage> usages,
  bool selection,
) {
  final fields = <String, String>{};
  final declarations = <String>[];
  for (final p in api.params) {
    final base = _dartParamType(p, usages);
    final optional =
        p.hasDefault || (api.paginated && _paginationNames.contains(p.name));
    fields[p.name] = p.nullable && p.hasDefault
        ? 'LuxoOptional<$base>'
        : '$base${p.nullable || optional ? '?' : ''}';
    declarations.add(
      _dartParamDeclaration(p, usages, forcedOptional: optional),
    );
  }
  if (selection) {
    fields['sel'] = 'String?';
    declarations.add('String? sel');
  }
  if (api.paginated) {
    for (final name in _paginationNames) {
      if (!fields.containsKey(name)) {
        fields[name] = 'LuxoInt64?';
        declarations.add('LuxoInt64? $name');
      }
    }
    fields.addAll({
      'filters': 'List<LuxoFilter>?',
      'sorters': 'List<LuxoSorter>?',
    });
    declarations.addAll([
      'List<LuxoFilter>? filters',
      'List<LuxoSorter>? sorters',
    ]);
  }
  return (fields, declarations);
}

void _writeRequestAccessors(
  StringBuffer b,
  LuxoAPI api,
  bool selection,
  Map<String, String> stored,
) {
  if (selection)
    b.writeln('  @override\n  String? get selection => ${stored['sel']};');
  if (api.paginated) {
    b.writeln(
      '  @override\n  List<LuxoFilter>? get filters => ${stored['filters']};',
    );
    b.writeln(
      '  @override\n  List<LuxoSorter>? get sorters => ${stored['sorters']};',
    );
  }
}

void _writeRequestParam(
  StringBuffer b,
  LuxoParam p,
  LuxoAPI api,
  LuxoSchema schema,
  String stored,
) {
  final optional =
      p.hasDefault || (api.paginated && _paginationNames.contains(p.name));
  final triState = p.nullable && p.hasDefault;
  if (optional)
    b.writeln(
      '    if (${triState ? '$stored.isPresent' : '$stored != null'}) {',
    );
  b.writeln('    { final value = $stored${triState ? '.value' : ''};');
  b.writeln('      encoder.writeVarint(${p.id});');
  if (p.nullable)
    b.writeln(
      '      if (value == null) { encoder.writeNull(); } else {\n        encoder.writePresent();',
    );
  const value = 'value';
  final field = LuxoField(
    id: p.id,
    name: p.name,
    type: p.type,
    typeName: p.typeName,
  );
  if (p.isList) {
    b.writeln(
      '      encoder.writeVarint($value.length);\n      for (final item in $value) {',
    );
    _writeRequestScalar(b, field, 'item', schema);
    b.writeln('      }');
  } else {
    _writeRequestScalar(b, field, value, schema);
  }
  if (p.nullable) b.writeln('      }');
  b.writeln('    }');
  if (optional) b.writeln('    }');
}

void _writeRequestScalar(
  StringBuffer b,
  LuxoField field,
  String value,
  LuxoSchema schema,
) {
  if (_isNested(field, schema)) {
    b.writeln('      encoder.writeDelimited($value.writeLuxo);');
  } else {
    _writeDartBinaryScalar(b, field, value, schema, '      ');
  }
}
