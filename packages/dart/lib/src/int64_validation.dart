/// Both Dart VM and JavaScript can represent every integer in this range.
const int maxSafeInteger = 9007199254740991;

int requireSafeInteger(int value) {
  if (value < -maxSafeInteger || value > maxSafeInteger) {
    throw RangeError.range(value, -maxSafeInteger, maxSafeInteger, 'value');
  }
  return value;
}

/// Validates before backend parsing, which may otherwise wrap at 64 bits.
void validateInt64Decimal(String value) {
  if (value.isEmpty) throw FormatException('Expected an int64 decimal', value);
  final start = value.codeUnitAt(0) == 45 ? 1 : 0;
  final digits = value.length - start;
  if (digits > 19) {
    throw FormatException('Integer outside signed 64-bit range', value);
  }
  if (digits == 0 || (digits > 1 && value.codeUnitAt(start) == 48)) {
    throw FormatException('Expected an int64 decimal', value);
  }
  for (var i = start; i < value.length; i++) {
    final digit = value.codeUnitAt(i);
    if (digit < 48 || digit > 57) {
      throw FormatException('Expected an int64 decimal', value);
    }
  }
  final limit = start == 1 ? '9223372036854775808' : '9223372036854775807';
  if (digits == limit.length && value.substring(start).compareTo(limit) > 0) {
    throw FormatException('Integer outside signed 64-bit range', value);
  }
}
