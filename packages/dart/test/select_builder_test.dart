import 'package:build/build.dart';
import 'package:luxo_client/src/builder/select_builder.dart';
import 'package:test/test.dart';

void main() {
  test('legacy API-wide hints builder rejects use', () {
    expect(
      () => selectHintsBuilder(BuilderOptions({})),
      throwsA(
        isA<UnsupportedError>().having(
          (error) => error.message,
          'migration',
          contains('luxo_client:build'),
        ),
      ),
    );
  });
}
