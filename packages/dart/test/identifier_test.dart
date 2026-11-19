import 'package:luxo_client/src/identifier.dart';
import 'package:test/test.dart';

void main() {
  test(
    'DSL Unicode and generated source names stay distinct and reversible',
    () {
      const names = [
        'ascii',
        '_id2',
        'café',
        'Café',
        '中文2',
        '𐐀_name',
        'CaféInput',
      ];
      final targets = {for (final name in names) dartSourceIdentifier(name)};
      expect(targets.length, names.length);
      for (final name in names) {
        expect(isDSLIdentifier(name), isTrue);
        final target = dartSourceIdentifier(name);
        expect(RegExp(r'^[A-Za-z_$][A-Za-z0-9_$]*$').hasMatch(target), isTrue);
        expect(wireIdentifier(target), name);
        final nominal = dartTypeIdentifier(name);
        expect(wireIdentifier(nominal), name);
        if (name != 'ascii' && name != '_id2') expect(nominal, isNot(target));
      }
      expect(dartSourceIdentifier('ascii'), 'ascii');
      for (final invalid in [
        '',
        '1name',
        'a-b',
        'e\u0301',
        '²',
        '😀',
        '字\u200d',
        '\ud800',
      ]) {
        expect(isDSLIdentifier(invalid), isFalse);
      }
      for (final literal in [
        r'$luxo$no',
        r'$luxo$110000',
        r'$luxo$d800',
        r'$luxo$61',
      ]) {
        expect(wireIdentifier(literal), literal);
      }
    },
  );
}
