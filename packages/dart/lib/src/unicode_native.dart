/// Skip plain code units cheaply; the platform iterator handles pair semantics.
bool hasCompleteUnicode(String value) {
  for (var index = 0; index < value.length; index++) {
    if ((value.codeUnitAt(index) & 0xf800) == 0xd800) {
      return _completeRunesFrom(value, index);
    }
  }
  return true;
}

bool _completeRunesFrom(String value, int index) {
  // This is the first surrogate, so index cannot split a preceding pair.
  final iterator = RuneIterator.at(value, index);
  while (iterator.moveNext()) {
    final rune = iterator.current;
    if (rune >= 0xd800 && rune <= 0xdfff) return false;
  }
  return true;
}
