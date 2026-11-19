// DSL identifiers use Unicode letters, underscores, and noninitial ASCII digits.
late final _unicodeLetter = RegExp(r'^\p{L}$', unicode: true);

int identifierWidth(String text, int offset, {required bool first}) {
  final code = text.codeUnitAt(offset);
  if (code < 128) {
    return code == 95 ||
            (code >= 65 && code <= 90) ||
            (code >= 97 && code <= 122) ||
            (!first && code >= 48 && code <= 57)
        ? 1
        : 0;
  }
  var width = 1;
  if (code >= 0xd800 && code <= 0xdbff && offset + 1 < text.length) {
    final low = text.codeUnitAt(offset + 1);
    if (low >= 0xdc00 && low <= 0xdfff) width = 2;
  }
  return _unicodeLetter.hasMatch(text.substring(offset, offset + width))
      ? width
      : 0;
}

bool isDSLIdentifier(String text) {
  if (text.isEmpty) return false;
  var offset = 0;
  while (offset < text.length) {
    final width = identifierWidth(text, offset, first: offset == 0);
    if (width == 0) return false;
    offset += width;
  }
  return true;
}

// '$' is unavailable in DSL names, so this reversible alias cannot collide.
String dartSourceIdentifier(String name) => _sourceIdentifier(name, r'$luxo$');

String dartTypeIdentifier(String name) => _sourceIdentifier(name, r'$Luxo$');

String _sourceIdentifier(String name, String prefix) {
  var ascii = true;
  for (var i = 0; i < name.length; i++) {
    if (name.codeUnitAt(i) >= 128) {
      ascii = false;
      break;
    }
  }
  if (ascii) return name;
  return prefix + name.runes.map((rune) => rune.toRadixString(16)).join('_');
}

String wireIdentifier(String name) {
  final type = name.startsWith(r'$Luxo$');
  const prefix = r'$luxo$';
  if (!type && !name.startsWith(prefix)) return name;
  final codes = <int>[];
  for (final part in name.substring(prefix.length).split('_')) {
    final code = int.tryParse(part, radix: 16);
    if (code == null ||
        code < 0 ||
        code > 0x10ffff ||
        (code >= 0xd800 && code <= 0xdfff))
      return name;
    codes.add(code);
  }
  final wire = String.fromCharCodes(codes);
  final source = type ? dartTypeIdentifier(wire) : dartSourceIdentifier(wire);
  return source == name ? wire : name;
}
