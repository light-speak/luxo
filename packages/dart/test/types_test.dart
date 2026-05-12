import 'package:test/test.dart';
import 'package:luxo_client/src/types.dart';

void main() {
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
      final json = {
        'items': [],
        'total': 0,
        'page': 1,
        'pageSize': 10,
      };
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
      final json = {
        'id': 2,
        'name': 'email',
        'type': 'String',
      };
      final field = LuxoField.fromJson(json);
      expect(field.nullable, isFalse);
      expect(field.isList, isFalse);
      expect(field.relation, isFalse);
      expect(field.typeName, isNull);
    });
  });

  group('LuxoModel', () {
    test('fromJson', () {
      final json = {
        'name': 'User',
        'fields': [
          {'id': 1, 'name': 'id', 'type': 'Int'},
          {'id': 2, 'name': 'name', 'type': 'String'},
        ],
      };
      final model = LuxoModel.fromJson(json);
      expect(model.name, equals('User'));
      expect(model.fields.length, equals(2));
      expect(model.fields[0].name, equals('id'));
    });
  });

  group('LuxoAPI', () {
    test('fromJson with all fields', () {
      final json = {
        'id': 1,
        'name': 'getUser',
        'module': 'user',
        'returnType': 'User',
        'returnList': true,
        'paginated': true,
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
      expect(api.params.length, equals(1));
      expect(api.params[0].name, equals('userId'));
    });

    test('fromJson with defaults', () {
      final json = {
        'id': 2,
        'name': 'listPosts',
        'module': 'post',
      };
      final api = LuxoAPI.fromJson(json);
      expect(api.returnType, isNull);
      expect(api.returnList, isFalse);
      expect(api.paginated, isFalse);
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
          'getUser': {
            'id': 1,
            'name': 'getUser',
            'module': 'user',
          },
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
