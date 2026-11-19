part of 'json.dart';

class _JSONReader {
  final String source;
  int offset = 0;

  _JSONReader(this.source);

  Object? read({JSONResponseSchema? response}) {
    final value = response == null ? _value(0) : _responseValue(response, 0);
    _space();
    if (offset != source.length) _invalid();
    return value;
  }

  Map<String, Object?> readEnvelope({
    JSONResponseSchema? response,
    bool errorResponse = false,
  }) {
    _space();
    if (offset == source.length || source.codeUnitAt(offset) != 123) _invalid();
    final value =
        response == null ||
            (response.modelType == null &&
                !response.isList &&
                !response.paginated)
        ? _envelope()
        : _typedEnvelope(response, errorResponse);
    _space();
    if (offset != source.length) _invalid();
    return value;
  }

  Map<String, Object?> readSocketEnvelope(JSONSocketDecoder socket) {
    _space();
    if (offset == source.length || source.codeUnitAt(offset) != 123) _invalid();
    final value = _typedEnvelope(null, false, socket: socket);
    _space();
    if (offset != source.length) _invalid();
    return value;
  }

  // Keep transport-only depth handling out of ordinary object iteration.
  Map<String, Object?> _envelope() {
    offset++;
    final values = <String, Object?>{};
    _space();
    if (_take(125)) return values;
    do {
      _space();
      if (offset == source.length || source.codeUnitAt(offset) != 34)
        _invalid();
      final key = _string();
      _space();
      if (!_take(58)) _invalid();
      values[key] = _value(key == 'data' ? 0 : 1);
      _space();
      if (_take(125)) return values;
    } while (_take(44));
    return _invalid();
  }

  Map<String, Object?> _typedEnvelope(
    JSONResponseSchema? response,
    bool errorResponse, {
    JSONSocketDecoder? socket,
  }) {
    offset++;
    final values = <String, Object?>{};
    var dataStart = -1, dataEnd = -1;
    var priorStart = -1, priorEnd = -1;
    List<int>? extraData;
    _space();
    if (_take(125)) return values;
    do {
      _space();
      if (offset == source.length || source.codeUnitAt(offset) != 34)
        _invalid();
      final key = _string();
      _space();
      if (!_take(58)) _invalid();
      if (key == 'data') {
        values[key] = null;
        _space();
        final start = offset;
        if (socket == null) {
          _sourceEnd();
        } else {
          _skipSyntaxValue(socket);
        }
        if (dataStart >= 0) {
          if (priorStart < 0) {
            priorStart = dataStart;
            priorEnd = dataEnd;
          } else {
            (extraData ??= []).add(dataStart);
            extraData.add(dataEnd);
          }
        }
        dataStart = start;
        dataEnd = offset;
      } else {
        values[key] = _schemaValue(1);
      }
      _space();
      if (_take(125)) {
        final end = offset;
        final generic = errorResponse || values.containsKey('error');
        if (socket != null) {
          _space();
          if (offset != source.length) _invalid();
          _socketData(
            values,
            dataStart,
            dataEnd,
            priorStart,
            priorEnd,
            extraData,
            generic,
            socket,
          );
          offset = end;
          return values;
        }
        if (priorStart >= 0)
          _dataValue(priorStart, priorEnd, response!, generic, false);
        if (extraData != null) {
          for (var i = 0; i < extraData.length; i += 2) {
            _dataValue(
              extraData[i],
              extraData[i + 1],
              response!,
              generic,
              false,
            );
          }
        }
        if (dataStart >= 0)
          values['data'] = _dataValue(
            dataStart,
            dataEnd,
            response!,
            generic,
            true,
          );
        offset = end;
        return values;
      }
    } while (_take(44));
    return _invalid();
  }

  void _socketData(
    Map<String, Object?> values,
    int start,
    int end,
    int priorStart,
    int priorEnd,
    List<int>? extra,
    bool generic,
    JSONSocketDecoder socket,
  ) {
    final response = socket.resolve(values);
    if (response == null) return;
    var depthFailure =
        priorStart >= 0 &&
        _socketDataValue(
          values,
          priorStart,
          priorEnd,
          response,
          generic,
          false,
        );
    if (extra != null) {
      for (var i = 0; i < extra.length; i += 2) {
        if (_socketDataValue(
          values,
          extra[i],
          extra[i + 1],
          response,
          generic,
          false,
        ))
          depthFailure = true;
      }
    }
    if (start >= 0 &&
        _socketDataValue(values, start, end, response, generic, !depthFailure))
      depthFailure = true;
    if (depthFailure) throw JSONModelDepthError(values);
  }

  bool _socketDataValue(
    Map<String, Object?> values,
    int start,
    int end,
    JSONResponseSchema response,
    bool generic,
    bool materialize,
  ) {
    try {
      final value = _dataValue(start, end, response, generic, materialize);
      if (materialize) values['data'] = value;
      return false;
    } on _JSONModelDepthFailure {
      return true;
    }
  }

  // Iterative syntax validation also covers payloads whose operation has retired.
  // The transport-owned frame storage is reused without retaining business values.
  void _skipSyntaxValue(JSONSocketDecoder socket) {
    socket._depth = 0;
    while (true) {
      _space();
      if (offset == source.length) _invalid();
      final code = source.codeUnitAt(offset);
      if (code == 91 || code == 123) {
        offset++;
        _space();
        final close = code == 91 ? 93 : 125;
        if (!_take(close)) {
          socket._push(close);
          if (close == 125) _syntaxKey();
          continue;
        }
      } else {
        switch (code) {
          case 34:
            _skipSyntaxString();
          case 116:
            _keyword('true', true);
          case 102:
            _keyword('false', false);
          case 110:
            _keyword('null', null);
          default:
            _skipSyntaxNumber();
        }
      }
      while (true) {
        if (socket._depth == 0) return;
        _space();
        final close = socket._close;
        if (_take(close)) {
          socket._depth--;
          continue;
        }
        if (!_take(44)) _invalid();
        if (close == 125) _syntaxKey();
        break;
      }
    }
  }

  void _syntaxKey() {
    _space();
    if (!_take(34)) _invalid();
    offset--;
    _skipSyntaxString();
    _space();
    if (!_take(58)) _invalid();
  }

  void _skipSyntaxString() {
    offset++;
    while (offset < source.length) {
      final code = source.codeUnitAt(offset++);
      if (code == 34) return;
      if (code < 32) _invalid();
      if (code != 92) continue;
      if (offset == source.length) _invalid();
      final escape = source.codeUnitAt(offset++);
      if (escape == 117) {
        final unit = _syntaxHex();
        if (unit >= 0xd800 && unit <= 0xdbff) {
          if (!_take(92) || !_take(117)) _invalid();
          final low = _syntaxHex();
          if (low < 0xdc00 || low > 0xdfff) _invalid();
        } else if (unit >= 0xdc00 && unit <= 0xdfff) {
          _invalid();
        }
      } else if (escape != 34 &&
          escape != 92 &&
          escape != 47 &&
          escape != 98 &&
          escape != 102 &&
          escape != 110 &&
          escape != 114 &&
          escape != 116) {
        _invalid();
      }
    }
    _invalid();
  }

  int _syntaxHex() {
    var value = 0;
    for (var i = 0; i < 4; i++) {
      if (offset == source.length) _invalid();
      final code = source.codeUnitAt(offset++);
      final digit = code >= 48 && code <= 57
          ? code - 48
          : code >= 65 && code <= 70
          ? code - 55
          : code >= 97 && code <= 102
          ? code - 87
          : -1;
      if (digit < 0) _invalid();
      value = value * 16 + digit;
    }
    return value;
  }

  void _skipSyntaxNumber() {
    _take(45);
    if (!_take(48)) _digits();
    if (_take(46)) _digits();
    if (_take(101) || _take(69)) {
      if (!_take(43)) _take(45);
      _digits();
    }
  }

  Object? _dataValue(
    int start,
    int end,
    JSONResponseSchema response,
    bool generic,
    bool materialize,
  ) {
    offset = start;
    final value = generic
        ? _schemaValue(0, materialize: materialize)
        : _responseValue(response, 0, materialize: materialize);
    if (offset != end) _invalid();
    return value;
  }

  // Locate a source slice before final envelope metadata chooses its schema.
  // Every slice is subsequently validated, including overwritten data keys.
  void _sourceEnd() {
    if (offset == source.length) _invalid();
    final first = source.codeUnitAt(offset);
    if (first == 34) {
      _sourceStringEnd();
      return;
    }
    if (first != 91 && first != 123) {
      while (offset < source.length) {
        final code = source.codeUnitAt(offset);
        if (code == 44 ||
            code == 93 ||
            code == 125 ||
            code == 32 ||
            code == 9 ||
            code == 10 ||
            code == 13)
          break;
        offset++;
      }
      return;
    }
    var depth = 0;
    do {
      if (offset == source.length) _invalid();
      final code = source.codeUnitAt(offset);
      if (code == 34) {
        _sourceStringEnd();
        continue;
      }
      offset++;
      if (code == 91 || code == 123) depth++;
      if (code == 93 || code == 125) depth--;
    } while (depth != 0);
  }

  void _sourceStringEnd() {
    offset++;
    while (offset < source.length) {
      final code = source.codeUnitAt(offset++);
      if (code == 34) return;
      if (code == 92) offset++;
    }
    _invalid();
  }

  Object? _responseValue(
    JSONResponseSchema response,
    int objectDepth, {
    bool materialize = true,
  }) {
    _space();
    if (response.paginated &&
        offset < source.length &&
        source.codeUnitAt(offset) == 123) {
      return _typedObject(
        0,
        response: response,
        objectDepth: objectDepth,
        page: true,
        materialize: materialize,
      );
    }
    if (response.isList || response.paginated) {
      return _typedArray(response, objectDepth, materialize);
    }
    return _responseElement(response, objectDepth, materialize);
  }

  Object? _typedArray(
    JSONResponseSchema response,
    int objectDepth,
    bool materialize,
  ) {
    _space();
    if (offset < source.length && source.codeUnitAt(offset) == 91) {
      return _typedValues(
        0,
        response: response,
        objectDepth: objectDepth,
        materialize: materialize,
      );
    }
    return _schemaValue(0, materialize: materialize);
  }

  Object? _responseElement(
    JSONResponseSchema response,
    int objectDepth,
    bool materialize,
  ) {
    return _modelValue(
      response.modelType,
      response.types,
      objectDepth,
      materialize,
    );
  }

  Object? _modelValue(
    String? name,
    Map<String, Map<String, SelectionFieldSchema>> types,
    int objectDepth,
    bool materialize, {
    bool strict = false,
  }) {
    if (name == null) return _schemaValue(0, materialize: materialize);
    if (name.startsWith('@sealed:')) {
      return _sealedValue(name, types, objectDepth, materialize);
    }
    final fields = types[name];
    if (fields == null)
      throw FormatException('missing JSON model schema: $name');
    _space();
    if (offset < source.length && source.codeUnitAt(offset) == 123) {
      if (objectDepth >= 64) throw _JSONModelDepthFailure(source, offset);
      return _typedObject(
        0,
        fields: fields,
        types: types,
        objectDepth: objectDepth + 1,
        materialize: materialize,
        strict: strict,
      );
    }
    return _schemaValue(0, materialize: materialize);
  }

  Object? _sealedValue(
    String name,
    Map<String, Map<String, SelectionFieldSchema>> types,
    int objectDepth,
    bool materialize,
  ) {
    final cases = types[name];
    if (cases == null) throw FormatException('missing sealed schema: $name');
    _space();
    if (source.startsWith('null', offset)) return _keyword('null', null);
    if (objectDepth >= 64) throw _JSONModelDepthFailure(source, offset);
    if (!_take(123)) _invalid();
    _space();
    if (offset == source.length || source.codeUnitAt(offset) != 34) _invalid();
    final key = _string();
    final variant = cases[key];
    if (variant == null) throw FormatException('unknown sealed case: $key');
    _space();
    if (!_take(58)) _invalid();
    _space();
    if (offset == source.length || source.codeUnitAt(offset) != 123) _invalid();
    final payload = _modelValue(
      variant.typeName,
      types,
      objectDepth + 1,
      materialize,
      strict: true,
    );
    _space();
    if (!_take(125)) _invalid();
    return materialize ? <String, Object?>{key: payload} : null;
  }

  Object? _fieldValue(
    SelectionFieldSchema field,
    Map<String, Map<String, SelectionFieldSchema>> types,
    int objectDepth,
    bool materialize,
  ) {
    final name = field.typeName ?? field.type;
    if (field.type == 'Model' && !types.containsKey(name)) {
      throw FormatException('missing JSON model schema: $name');
    }
    final model = field.type == 'Sealed'
        ? '@sealed:$name'
        : types.containsKey(name)
        ? name
        : null;
    _space();
    if (field.isList &&
        offset < source.length &&
        source.codeUnitAt(offset) == 91) {
      return _typedValues(
        0,
        modelType: model,
        types: types,
        objectDepth: objectDepth,
        typedElements: true,
        materialize: materialize,
      );
    }
    return _modelValue(model, types, objectDepth, materialize);
  }

  Never _invalid() => throw FormatException('Invalid JSON', source, offset);

  void _space() {
    while (offset < source.length) {
      final code = source.codeUnitAt(offset);
      if (code != 32 && code != 9 && code != 10 && code != 13) return;
      offset++;
    }
  }

  bool _take(int code) {
    if (offset >= source.length || source.codeUnitAt(offset) != code)
      return false;
    offset++;
    return true;
  }

  Object? _value(int depth) {
    _space();
    if (depth > 128 || offset == source.length) _invalid();
    return switch (source.codeUnitAt(offset)) {
      34 => _string(),
      91 => _array(depth + 1),
      123 => _object(depth + 1),
      116 => _keyword('true', true),
      102 => _keyword('false', false),
      110 => _keyword('null', null),
      _ => _number(),
    };
  }

  List<Object?> _array(int depth) {
    if (depth > 128) _invalid();
    offset++;
    final values = <Object?>[];
    _space();
    if (_take(93)) return values;
    do {
      values.add(_value(depth));
      _space();
      if (_take(93)) return values;
    } while (_take(44));
    return _invalid();
  }

  Map<String, Object?> _object(int depth) {
    if (depth > 128) _invalid();
    offset++;
    final values = <String, Object?>{};
    _space();
    if (_take(125)) return values;
    do {
      _space();
      if (offset == source.length || source.codeUnitAt(offset) != 34)
        _invalid();
      final key = _string();
      _space();
      if (!_take(58)) _invalid();
      values[key] = _value(depth);
      _space();
      if (_take(125)) return values;
    } while (_take(44));
    return _invalid();
  }

  Object? _schemaValue(int depth, {bool materialize = true}) {
    if (materialize) return _value(depth);
    _space();
    if (depth > 128 || offset == source.length) _invalid();
    return switch (source.codeUnitAt(offset)) {
      34 => _string(),
      91 => _typedValues(depth + 1, materialize: materialize),
      123 => _typedObject(depth + 1, materialize: materialize),
      116 => _keyword('true', true),
      102 => _keyword('false', false),
      110 => _keyword('null', null),
      _ => _number(),
    };
  }

  Object? _keyword(String word, Object? value) {
    if (!source.startsWith(word, offset)) _invalid();
    offset += word.length;
    return value;
  }

  String _string() {
    final start = offset++;
    while (offset < source.length) {
      final code = source.codeUnitAt(offset++);
      if (code == 34)
        return luxoStringFromJSON(jsonDecode(source.substring(start, offset)));
      if (code == 92) offset++;
    }
    return _invalid();
  }

  List<Object?>? _typedValues(
    int depth, {
    JSONResponseSchema? response,
    String? modelType,
    Map<String, Map<String, SelectionFieldSchema>> types = const {},
    int objectDepth = 0,
    bool typedElements = false,
    bool materialize = true,
  }) {
    if (depth > 128) _invalid();
    offset++;
    final values = materialize ? <Object?>[] : null;
    _space();
    if (_take(93)) return values;
    do {
      final value = response != null
          ? _responseElement(response, objectDepth, materialize)
          : typedElements
          ? _modelValue(modelType, types, objectDepth, materialize)
          : _schemaValue(depth, materialize: materialize);
      values?.add(value);
      _space();
      if (_take(93)) return values;
    } while (_take(44));
    return _invalid();
  }

  Map<String, Object?>? _typedObject(
    int depth, {
    JSONResponseSchema? response,
    Map<String, SelectionFieldSchema>? fields,
    Map<String, Map<String, SelectionFieldSchema>> types = const {},
    int objectDepth = 0,
    bool page = false,
    bool materialize = true,
    bool strict = false,
  }) {
    if (depth > 128) _invalid();
    offset++;
    final values = materialize ? <String, Object?>{} : null;
    _space();
    if (_take(125)) return values;
    do {
      _space();
      if (offset == source.length || source.codeUnitAt(offset) != 34)
        _invalid();
      final key = _string();
      _space();
      if (!_take(58)) _invalid();
      final field = fields?[key];
      if (strict && field == null)
        throw FormatException('unknown sealed payload field: $key');
      final value = page && key == 'items'
          ? _typedArray(response!, objectDepth, materialize)
          : field != null
          ? _fieldValue(field, types, objectDepth, materialize)
          : _schemaValue(depth, materialize: materialize);
      if (values != null) values[key] = value;
      _space();
      if (_take(125)) return values;
    } while (_take(44));
    return _invalid();
  }

  bool get _digit =>
      offset < source.length &&
      source.codeUnitAt(offset) >= 48 &&
      source.codeUnitAt(offset) <= 57;

  void _digits() {
    if (!_digit) _invalid();
    do {
      offset++;
    } while (_digit);
  }

  Object _number({bool raw = false}) {
    final start = offset;
    _take(45);
    if (!_take(48)) _digits();
    var floating = false;
    if (_take(46)) {
      floating = true;
      _digits();
    }
    if (_take(101) || _take(69)) {
      floating = true;
      if (!_take(43)) _take(45);
      _digits();
    }
    final literal = source.substring(start, offset);
    if (raw || floating || literal == '-0') return LuxoJSONNumber._(literal);
    // Parsing a short token is exact on every platform; longer tokens must
    // never pass through int.parse or num.parse on JavaScript first.
    if (literal.length < 16) return int.parse(literal);
    final integer = BigInt.parse(literal);
    if (integer >= -_safeBigInt && integer <= _safeBigInt)
      return integer.toInt();
    return integer;
  }
}

final _safeBigInt = BigInt.from(maxSafeInteger);
