import 'package:test/test.dart';
import 'package:luxo_client/src/error.dart';

void main() {
  group('LuxoError', () {
    test('constructor sets all fields', () {
      final err = LuxoError('NotFound', 404, 'user not found', 'trace-abc-123');
      expect(err.error, equals('NotFound'));
      expect(err.code, equals(404));
      expect(err.message, equals('user not found'));
      expect(err.traceId, equals('trace-abc-123'));
    });

    test('traceId is optional (null)', () {
      final err = LuxoError('ServerError', 500, 'internal error');
      expect(err.traceId, isNull);
    });

    test('carries structured data and development cause', () {
      final err = LuxoError(
          'BadRequest',
          400,
          'bad',
          'trace',
          {
            'param': 'email',
          },
          'validation failed');
      expect(err.data, equals({'param': 'email'}));
      expect(err.cause, equals('validation failed'));
    });

    test('toString format', () {
      final err = LuxoError('Unauthorized', 401, 'invalid token');
      expect(
        err.toString(),
        equals('LuxoError(Unauthorized, 401, invalid token)'),
      );
    });

    test('toString with empty message', () {
      final err = LuxoError('Unknown', 0, '');
      expect(err.toString(), equals('LuxoError(Unknown, 0, )'));
    });

    test('implements Exception', () {
      final err = LuxoError('Test', 0, 'msg');
      expect(err, isA<Exception>());
    });

    test('can be thrown and caught', () {
      expect(
        () => throw LuxoError('BadRequest', 400, 'missing field'),
        throwsA(isA<LuxoError>().having((e) => e.code, 'code', 400)),
      );
    });
  });
}
