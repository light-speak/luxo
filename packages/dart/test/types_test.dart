import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';
import 'package:luxo_client/src/types.dart';

void main() {
  test('sealed metadata preserves variant and payload ID namespaces', () {
    final fixture = jsonDecode(
      File('../../pkg/lux/codec/testdata/sealed-v1.json').readAsStringSync(),
    ) as Map<String, dynamic>;
    final schema = LuxoSchema.fromJson(
      fixture['schema'] as Map<String, dynamic>,
    );
    final owner = schema.sealeds['Outcome']!;
    expect(owner.name, 'Outcome');
    expect(owner.variants.map((variant) => variant.id), [1, 2, 3, 4]);
    expect(owner.variants[1].fields.single.hasDefault, isTrue);
    expect(owner.variants[3].fields.last.isList, isTrue);
    schema.validateIDs();
  });
  test('Schema enum values validate eagerly and own their list', () {
    for (final value in [null, false, true, 0, 7, [], {}]) {
      expect(
        () => LuxoEnum.fromJson({
          'name': 'State',
          'values': [value],
        }),
        throwsA(isA<TypeError>()),
      );
    }
    final source = ['READY', '0'];
    final parsed = LuxoEnum.fromJson({'name': 'State', 'values': source});
    source[0] = 'CHANGED';
    expect(parsed.values, ['READY', '0']);
    expect(
      LuxoEnum.fromJson({'name': 'State', 'values': <String>[]}).values,
      isEmpty,
    );
  });

  test('Schema member strings reject malformed JSON values', () {
    final missing = Object();
    for (final target in ['model', 'type', 'param']) {
      for (final member in ['name', 'type', 'typeName']) {
        final values = member == 'typeName'
            ? [false, true, 0, 7, [], {}]
            : [missing, null, false, true, 0, 7, [], {}];
        for (final value in values) {
          final field = <String, Object?>{
            'id': 1,
            'name': 'value',
            'type': 'String',
            member: value,
          };
          if (identical(value, missing)) field.remove(member);
          final schema = <String, Object?>{
            'models': <String, Object?>{},
            'types': <String, Object?>{},
            'apis': <String, Object?>{},
          };
          if (target == 'param') {
            schema['apis'] = {
              'read': {
                'id': 1,
                'name': 'read',
                'module': 'test',
                'params': [field],
              },
            };
          } else {
            schema[target == 'model' ? 'models' : 'types'] = {
              'Payload': {
                'name': 'Payload',
                'fields': [field],
              },
            };
          }
          expect(
            () => LuxoSchema.fromJson(schema),
            throwsA(isA<TypeError>()),
            reason: '$target.$member=$value',
          );
          field.remove(member);
          if (member != 'typeName')
            field[member] = member == 'name' ? 'value' : 'String';
          expect(() => LuxoSchema.fromJson(schema), returnsNormally);
        }
      }
    }
  });

  test('Schema flags reject non-boolean JSON metadata', () {
    final groups = {
      'api': [
        'internal',
        'returnList',
        'returnNullable',
        'paginated',
        'stream',
      ],
      'field': ['nullable', 'isList', 'relation'],
      'param': ['nullable', 'isList', 'hasDefault'],
    };
    for (final group in groups.entries) {
      for (final flag in group.value) {
        for (final value in [true, false, 'true', 'false', 0, 1, [], {}]) {
          final metadata = {
            'id': 1,
            'name': 'value',
            'type': 'String',
            'module': 'test',
            flag: value,
          };
          Object parse() => switch (group.key) {
            'api' => LuxoAPI.fromJson(metadata),
            'field' => LuxoField.fromJson(metadata),
            _ => LuxoParam.fromJson(metadata),
          };
          expect(
            parse,
            value is bool ? returnsNormally : throwsA(isA<TypeError>()),
            reason: '${group.key}.$flag=$value',
          );
        }
      }
    }
  });

  test(
    'Schema numbering preserves sparse maxima and independent namespaces',
    () {
      final schema = LuxoSchema(
        models: {
          'Item': LuxoModel(
            name: 'Item',
            fields: [LuxoField(id: 65535, name: 'id', type: 'Int')],
          ),
        },
        types: {
          'Input': LuxoTypeDecl(
            name: 'Input',
            fields: [LuxoField(id: 65535, name: 'id', type: 'Int')],
          ),
        },
        apis: {
          for (final id in [1, 2147483647])
            'read$id': LuxoAPI(
              id: id,
              name: 'read$id',
              module: 'test',
              params: [LuxoParam(id: 2147483645, name: 'value', type: 'Int')],
            ),
        },
      );
      schema.validateIDs();
      expect(schema.models['Item']!.fields.single.id, 65535);
      expect(schema.apis['read2147483647']!.id, 2147483647);
    },
  );
  test('Pagination rejects invalid types and duplicate parameter names', () {
    for (final invalid in [
      [
        {'id': 7, 'name': 'page', 'type': 'String'},
      ],
      [
        {'id': 7, 'name': 'page', 'type': 'Int', 'nullable': true},
      ],
      [
        {'id': 7, 'name': 'page', 'type': 'Int', 'isList': true},
      ],
      [
        {'id': 7, 'name': 'page', 'type': 'Int'},
        {'id': 8, 'name': 'page', 'type': 'Int'},
      ],
    ]) {
      final schema = LuxoSchema.fromJson({
        'models': <String, Object?>{},
        'apis': {
          'browse': {
            'id': 1,
            'name': 'browse',
            'module': 'test',
            'paginated': true,
            'params': [
              ...invalid,
              {'id': 9, 'name': 'pageSize', 'type': 'Int'},
            ],
          },
        },
      });
      expect(schema.validateIDs, throwsFormatException);
    }
  });
  group('Page', () {
    test('fromJson parses correctly', () {
      final json = {
        'items': [
          {'id': 1, 'name': 'Alice'},
          {'id': 2, 'name': 'Bob'},
        ],
        'total': 10,
        'page': 1,
        'pageSize': 2,
      };
      final page = Page.fromJson(json, (e) => e);
      expect(page.items.length, equals(2));
      expect(page.total, equals(10));
      expect(page.page, equals(1));
      expect(page.pageSize, equals(2));
      expect(page.items[0]['name'], equals('Alice'));
    });

    test('fromJson with empty items', () {
      final json = {'items': [], 'total': 0, 'page': 1, 'pageSize': 10};
      final page = Page.fromJson(json, (e) => e);
      expect(page.items, isEmpty);
      expect(page.total, equals(0));
    });
  });

  group('LuxoField', () {
    test('fromJson with all fields', () {
      final json = {
        'id': 1,
        'name': 'username',
        'type': 'String',
        'typeName': 'User',
        'nullable': true,
        'isList': true,
        'relation': true,
      };
      final field = LuxoField.fromJson(json);
      expect(field.id, equals(1));
      expect(field.name, equals('username'));
      expect(field.type, equals('String'));
      expect(field.typeName, equals('User'));
      expect(field.nullable, isTrue);
      expect(field.isList, isTrue);
      expect(field.relation, isTrue);
    });

    test('fromJson with defaults', () {
      final json = {'id': 2, 'name': 'email', 'type': 'String'};
      final field = LuxoField.fromJson(json);
      expect(field.nullable, isFalse);
      expect(field.isList, isFalse);
      expect(field.relation, isFalse);
      expect(field.typeName, isNull);
    });
  });

  group('LuxoModel', () {
    test('preserves computed source metadata without changing fields', () {
      final model = LuxoModel.fromJson({
        'name': 'User',
        'fields': [
          {'id': 1, 'name': 'postCount', 'type': 'Int'},
        ],
        'computedSources': {
          'postCount': {'model': 'Post', 'module': 'blog'},
          'localCount': {'model': 'Local'},
        },
      });
      expect(model.computedSources['postCount']!.model, 'Post');
      expect(model.computedSources['postCount']!.module, 'blog');
      expect(model.computedSources['localCount']!.module, '');
      expect(model.fields.single.type, 'Int');
      expect(
        LuxoModel.fromJson({'name': 'Plain', 'fields': []}).computedSources,
        isEmpty,
      );
    });
    test('fromJson', () {
      final json = {
        'name': 'User',
        'usage': 'output',
        'fields': [
          {'id': 1, 'name': 'id', 'type': 'Int'},
          {'id': 2, 'name': 'name', 'type': 'String'},
        ],
      };
      final model = LuxoModel.fromJson(json);
      expect(model.name, equals('User'));
      expect(model.usage, TypeUsage.output);
      expect(model.fields.length, equals(2));
      expect(model.fields[0].name, equals('id'));
    });
  });

  group('Selected', () {
    test('distinguishes unselected, selected null, and selected value', () {
      const absent = Selected<String?>.unselected();
      const selectedNull = Selected<String?>.value(null);
      const selectedValue = Selected<String?>.value('Luxo');

      expect(absent.isSelected, isFalse);
      expect(() => absent.value, throwsStateError);
      expect(selectedNull.isSelected, isTrue);
      expect(selectedNull.value, isNull);
      expect(selectedValue.value, 'Luxo');
    });
  });

  group('LuxoAPI', () {
    test('preserves explicit root nullability independently of list shape', () {
      for (final list in [false, true]) {
        for (final nullable in [false, true]) {
          final api = LuxoAPI.fromJson({
            'id': 1,
            'name': 'optional',
            'module': 'test',
            'returnType': 'Int',
            'returnList': list,
            'returnNullable': nullable,
          });
          expect(api.returnNullable, nullable);
          expect(api.returnList, list);
        }
      }
    });
    test('fromJson with all fields', () {
      final json = {
        'id': 1,
        'name': 'getUser',
        'module': 'user',
        'returnType': 'User',
        'returnList': true,
        'paginated': true,
        'defaultPageSize': 50,
        'params': [
          {'id': 1, 'name': 'userId', 'type': 'Int'},
        ],
      };
      final api = LuxoAPI.fromJson(json);
      expect(api.id, equals(1));
      expect(api.name, equals('getUser'));
      expect(api.module, equals('user'));
      expect(api.returnType, equals('User'));
      expect(api.returnList, isTrue);
      expect(api.paginated, isTrue);
      expect(api.defaultPageSize, equals(50));
      expect(api.params.length, equals(1));
      expect(api.params[0].name, equals('userId'));
    });

    test('fromJson with defaults', () {
      final json = {'id': 2, 'name': 'listPosts', 'module': 'post'};
      final api = LuxoAPI.fromJson(json);
      expect(api.returnType, isNull);
      expect(api.returnList, isFalse);
      expect(api.returnNullable, isFalse);
      expect(api.paginated, isFalse);
      expect(api.defaultPageSize, isNull);
      expect(api.params, isEmpty);
    });
  });

  group('LuxoEnum', () {
    test('fromJson', () {
      final json = {
        'name': 'Status',
        'values': ['Active', 'Inactive', 'Pending'],
      };
      final e = LuxoEnum.fromJson(json);
      expect(e.name, equals('Status'));
      expect(e.values, equals(['Active', 'Inactive', 'Pending']));
    });
  });

  group('LuxoSchema', () {
    test('fromJson with all sections', () {
      final json = {
        'models': {
          'User': {
            'name': 'User',
            'fields': [
              {'id': 1, 'name': 'id', 'type': 'Int'},
            ],
          },
        },
        'apis': {
          'getUser': {'id': 1, 'name': 'getUser', 'module': 'user'},
        },
        'enums': {
          'Status': {
            'name': 'Status',
            'values': ['Active'],
          },
        },
        'types': {
          'Address': {
            'name': 'Address',
            'fields': [
              {'id': 1, 'name': 'street', 'type': 'String'},
            ],
          },
        },
      };
      final schema = LuxoSchema.fromJson(json);
      expect(schema.models.containsKey('User'), isTrue);
      expect(schema.apis.containsKey('getUser'), isTrue);
      expect(schema.enums.containsKey('Status'), isTrue);
      expect(schema.types.containsKey('Address'), isTrue);
    });

    test('fromJson with empty/null sections', () {
      final schema = LuxoSchema.fromJson({});
      expect(schema.models, isEmpty);
      expect(schema.apis, isEmpty);
      expect(schema.enums, isEmpty);
      expect(schema.types, isEmpty);
    });
  });

  group('LuxoParam', () {
    test('fromJson', () {
      final json = {'id': 3, 'name': 'limit', 'type': 'Int'};
      final param = LuxoParam.fromJson(json);
      expect(param.id, equals(3));
      expect(param.name, equals('limit'));
      expect(param.type, equals('Int'));
    });
  });

  group('LuxoTypeDecl', () {
    test('fromJson', () {
      final json = {
        'name': 'Address',
        'fields': [
          {'id': 1, 'name': 'city', 'type': 'String'},
          {'id': 2, 'name': 'zip', 'type': 'String'},
        ],
      };
      final t = LuxoTypeDecl.fromJson(json);
      expect(t.name, equals('Address'));
      expect(t.fields.length, equals(2));
    });
  });
}
