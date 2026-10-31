part of 'json.dart';

class _JSONReader {
  final String source;
  int offset = 0;

  _JSONReader(this.source);

  Object? read() {
    final value = _value(0);
    _space();
    if (offset != source.length) _invalid();
    return value;
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
