import 'dart:typed_data';

import 'codec.dart';
import 'error.dart';
import 'schema.dart';
import 'schema_ids.dart' show isSelectionSnapshot;

/// An immutable call-site plan. Mutable external Schema objects are never cached.
final class PreparedSelection {
  final String text;
  final List<_SelectedField> _selected;
  final _masks = Expando<Uint8List>();

  PreparedSelection(this.text) : _selected = _SelectionParser(text).parse() {
    if (text.trim().isEmpty) {
      throw LuxoError(
        'ConfigError',
        0,
        r'$select is required for structured responses',
      );
    }
  }

  void _write(LuxoEncoder encoder, APISchemaEntry schema) {
    final reusable = isSelectionSnapshot(schema);
    var mask = reusable ? _masks[schema] : null;
    if (mask == null) {
      mask = _encodeSelectionNode(_selected, schema.fields, schema.types);
      if (reusable) _masks[schema] = mask;
    }
    encoder.writeVarint(mask.length);
    encoder.writeRawBytes(mask);
  }
}

class _SelectedField {
  final String name;
  final List<_SelectedField>? children;
  const _SelectedField(this.name, [this.children]);
}

class _SelectionParser {
  final String input;
  int offset = 0;
  _SelectionParser(this.input);

  List<_SelectedField> parse() {
    final fields = _parseList(false, 0);
    _skipSpaces();
    if (offset != input.length) _fail("unexpected '${input[offset]}'");
    return fields;
  }

  List<_SelectedField> _parseList(bool nested, int depth) {
    if (depth >= 32) _fail('selection depth exceeds 32');
    final fields = <_SelectedField>[];
    final names = <String>{};
    while (true) {
      _skipSpaces();
      if (offset >= input.length || (nested && input[offset] == '}')) {
        if (fields.isNotEmpty) _fail('expected field after comma');
        break;
      }
      final name = _readIdentifier();
      if (name.isEmpty) _fail('expected field name');
      if (!names.add(name)) _fail("duplicate field '$name'");
      _skipSpaces();
      List<_SelectedField>? children;
      if (offset < input.length && input[offset] == '{') {
        offset++;
        children = _parseList(true, depth + 1);
        if (children.isEmpty) _fail("empty selection for '$name'");
        _skipSpaces();
        if (offset >= input.length || input[offset] != '}') {
          _fail("missing '}' for '$name'");
        }
        offset++;
      }
      fields.add(_SelectedField(name, children));
      _skipSpaces();
      if (offset >= input.length || input[offset] != ',') break;
      offset++;
    }
    return fields;
  }

  String _readIdentifier() {
    final start = offset;
    if (offset >= input.length || !_identifierStart(input.codeUnitAt(offset))) {
      return '';
    }
    offset++;
    while (offset < input.length && _identifierPart(input.codeUnitAt(offset))) {
      offset++;
    }
    return input.substring(start, offset);
  }

  void _skipSpaces() {
    while (offset < input.length && input.codeUnitAt(offset) <= 32) offset++;
  }

  Never _fail(String message) =>
      throw LuxoError('ConfigError', 0, '$message at position $offset');
}

bool _identifierStart(int code) =>
    code == 95 || (code >= 65 && code <= 90) || (code >= 97 && code <= 122);

bool _identifierPart(int code) =>
    _identifierStart(code) || (code >= 48 && code <= 57);

Uint8List _encodeSelectionNode(
  List<_SelectedField> selected,
  Map<String, SelectionFieldSchema> fields,
  Map<String, Map<String, SelectionFieldSchema>> types,
) {
  var mask = Uint8List(0);
  final children = <({int fieldID, Uint8List data})>[];
  var childCapacity = 0;
  for (final field in selected) {
    final meta = fields[field.name];
    if (meta == null) {
      throw LuxoError(
        'ConfigError',
        0,
        'unknown selected field: ${field.name}',
      );
    }
    mask = fieldMaskSet(mask, meta.fieldID);
    if (field.children == null) continue;
    final nestedFields = meta.typeName == null ? null : types[meta.typeName];
    if (nestedFields == null) {
      throw LuxoError(
        'ConfigError',
        0,
        'field ${field.name} does not support nested selection',
      );
    }
    final data = _encodeSelectionNode(field.children!, nestedFields, types);
    children.add((fieldID: meta.fieldID, data: data));
    // Each varint writer reserves eight bytes before writing its actual length.
    childCapacity += data.length + 16;
  }
  children.sort((a, b) => a.fieldID.compareTo(b.fieldID));
  final node = LuxoEncoder(mask.length + childCapacity + 8);
  node.writeVarint(mask.length);
  node.writeRawBytes(mask);
  for (final child in children) {
    node.writeVarint(child.fieldID);
    node.writeVarint(child.data.length);
    node.writeRawBytes(child.data);
  }
  return node.bytes();
}

void writeSelectionMask(
  LuxoEncoder enc,
  APISchemaEntry meta,
  Object? select, {
  PreparedSelection? plan,
}) {
  if (meta.fields.isEmpty) {
    enc.writeVarint(0);
    return;
  }
  if (select is! String || select.trim().isEmpty) {
    throw LuxoError(
      'ConfigError',
      0,
      r'$select is required for structured responses',
    );
  }
  if (plan != null && plan.text == select) {
    plan._write(enc, meta);
    return;
  }
  final mask = _encodeSelectionNode(
    _SelectionParser(select).parse(),
    meta.fields,
    meta.types,
  );
  enc.writeVarint(mask.length);
  enc.writeRawBytes(mask);
}
