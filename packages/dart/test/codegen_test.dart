import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

import 'support/stream_decode_server.dart';
import 'select_fixture.dart';

import 'package:luxo_client/src/builder/select_rewrite.dart';

void main() {
  test('internal service routes retain metadata without public identifier validation', () async {
    const name = 'svc:batchLoad:QueryProbe';
    final api = <String, Object?>{
      'id': 1,
      'name': name,
      'module': 'svc',
      'returnType': 'Int',
      'internal': true,
    };
    await _checkGeneratedConsumer(
      {
        'apis': {name: api},
      },
      r'''
import 'package:test/test.dart';
import 'client.dart';
void main() { test('internal API does not prevent public client generation', () { expect(LuxoClient, isNotNull); }); }
''',
    );
    api['internal'] = false;
    await _rejectSchema({
      'apis': {name: api},
    }, 'invalid declaration identifier');
  });
  test(
    'empty enums generate unknown-only values with exact raw text',
    () async {
      await _checkGeneratedConsumer(
        {
          'enums': {
            'Empty': {'name': 'Empty', 'values': <String>[]},
          },
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Empty',
            },
          },
        },
        r'''
import 'dart:typed_data';
import 'package:luxo_client/luxo_client.dart' as sdk;
import 'package:test/test.dart';
import 'types.dart';
void main() {
  test('unknown-only enum retains exact text', () {
    expect(EmptyKnown.values, isEmpty);
    for (final raw in ['', 'FUTURE', '中文', 'é', 'e\u0301']) {
      final value = Empty.fromValue(raw);
      expect(value, isA<EmptyUnknown>());
      expect(value.rawValue, raw);
      expect(value, Empty.fromValue(raw));
      final encoder = sdk.LuxoEncoder()..writeString(raw);
      expect(Empty.$readArray(sdk.LuxoDecoder(Uint8List.fromList([1, ...encoder.bytes()]))).single, value);
      expect(Empty.$fromJSONList([raw]).single, value);
    }
    expect(Empty.fromValue('é'), isNot(Empty.fromValue('e\u0301')));
  });
}
''',
      );
    },
  );
  test('generated nullable JSON canonical null preserves selection and body presence', () async {
    await _checkGeneratedConsumer(
      {
        'types': {
          'Payload': {
            'name': 'Payload',
            'usage': 'inputOutput',
            'fields': [
              {'id': 1, 'name': 'optional', 'type': 'JSON', 'nullable': true},
              {'id': 2, 'name': 'literal', 'type': 'JSON'},
            ],
          },
        },
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'JSON',
            'returnNullable': true,
            'params': [
              {'id': 1, 'name': 'value', 'type': 'JSON', 'nullable': true},
            ],
          },
          'requiredJSON': {
            'id': 2,
            'name': 'requiredJSON',
            'module': 'test',
            'returnType': 'JSON',
          },
          'payload': {
            'id': 3,
            'name': 'payload',
            'module': 'test',
            'returnType': 'Payload',
          },
        },
      },
      r'''
import 'dart:typed_data';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart' as sdk;
import 'package:test/test.dart';
import 'client.dart';
import 'types.dart';
void main() {
  test('canonical nullable JSON and non-nullable literal null remain distinct', () async {
    const literal = [4, 110, 117, 108, 108];
    final row = Uint8List.fromList([0, 1, 0, 2, ...literal, 0]);
    final selected = decodePayload(sdk.LuxoDecoder(row));
    expect(selected.optional.isSelected, isTrue);
    expect(selected.optional.value, isNull);
    expect(selected.literal.value, isNull);
    expect(decodePayload(sdk.LuxoDecoder(Uint8List.fromList([0, 0]))).optional.isSelected, isFalse);
    expect(decodeColumnarPayload(Uint8List.fromList([1, 0, 1, 0, 2, ...literal, 0])).single.optional.value, isNull);
    expect(Payload.fromJson({}).optional.isSelected, isFalse);
    expect(Payload.fromJson({'optional': null}).optional.isSelected, isTrue);
    final input = sdk.LuxoEncoder();
    PayloadInput(optional: null, literal: null).writeLuxo(input);
    expect(input.bytes(), row);
    for (final mode in sdk.TransportMode.values) {
      var missing = false;
      var requiredResponse = false;
      final client = LuxoClient(sdk.HttpTransport('http://example.test/luxo',
        options: sdk.TransportOptions(mode: mode),
        client: MockClient((request) async {
          if (!requiredResponse && mode == sdk.TransportMode.binary) {
            expect(request.bodyBytes, [1, 0, 1, 0, 0]);
          }
          return mode == sdk.TransportMode.json
              ? http.Response(missing ? '{}' : '{"data":null}', 200)
              : http.Response.bytes(missing ? [] : requiredResponse ? literal : [0], 200);
        }),
      ));
      addTearDown(client.close);
      expect(await client.read(value: null), isNull);
      missing = true;
      await expectLater(client.read(value: null), throwsA(isA<sdk.LuxoError>()));
      missing = false;
      requiredResponse = true;
      expect(await client.requiredJSON(), isNull);
    }
  });
}
''',
    );
  });

  test('sealed public generation cold scalar and rejection controls', () async {
    final schema = <String, Object?>{
      'models': {
        'InputRecord': {
          'name': 'InputRecord',
          'fields': [
            {
              'id': 1,
              'name': 'choice',
              'type': 'Sealed',
              'typeName': 'Outcome',
            },
          ],
        },
      },
      'sealeds': {
        'Outcome': {
          'name': 'Outcome',
          'variants': [
            {'id': 1, 'name': 'Ready', 'fields': <Object?>[]},
          ],
        },
      },
      'apis': {
        'write': {
          'id': 1,
          'name': 'write',
          'module': 'test',
          'returnType': 'Int',
          'params': [
            {
              'id': 1,
              'name': 'record',
              'type': 'Model',
              'typeName': 'InputRecord',
            },
          ],
        },
        'ticks': {
          'id': 2,
          'name': 'ticks',
          'module': 'test',
          'returnType': 'Int',
          'stream': true,
        },
      },
    };
    await _checkGeneratedConsumer(schema, r"""
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart';
void main(){test('input-only nested sealed and scalar callback compile',(){
 final input=InputRecord(choice:Outcome.Ready());
 expect(input.toJson(),{'choice':{'Ready':{}}});
 expect(LuxoClient.schema['write']!.types,contains('Outcome'));
});}
""");
    await _rejectSchema(
      {
        'models': {
          'Broken': {
            'name': 'Broken',
            'fields': [
              {'id': 1, 'name': 'field', 'type': 'Missing'},
            ],
          },
        },
        'apis': <String, Object?>{},
      },
      'undeclared type Missing',
      existing: true,
    );
    await _rejectSchema(
      {
        'models': <String, Object?>{},
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Missing',
          },
        },
      },
      'undeclared return type Missing',
      existing: true,
    );
  }, timeout: const Timeout(Duration(seconds: 120)));
  test(
    'sealed schema rejects duplicate owners and missing payload declarations',
    () async {
      final owner = {
        'name': 'Outcome',
        'variants': [
          {'id': 1, 'name': 'Pending', 'fields': <Object?>[]},
        ],
      };
      await _rejectSchema(
        {
          'models': {
            'Outcome': {'name': 'Outcome', 'fields': <Object?>[]},
          },
          'sealeds': {'Outcome': owner},
          'apis': <String, Object?>{},
        },
        'duplicate type Outcome',
        existing: true,
      );
      for (final parameter in [false, true]) {
        await _rejectSchema(
          {
            'sealeds': {'Outcome': owner},
            if (!parameter)
              'types': {
                'Container': {
                  'name': 'Container',
                  'fields': [
                    {
                      'id': 1,
                      'name': 'value',
                      'type': 'Sealed',
                      'typeName': 'Missing',
                    },
                  ],
                },
              },
            'apis': {
              'echo': {
                'id': 1,
                'name': 'echo',
                'module': 'test',
                'returnType': 'Outcome',
                if (parameter)
                  'params': [
                    {
                      'id': 1,
                      'name': 'value',
                      'type': 'Sealed',
                      'typeName': 'Missing',
                    },
                  ],
              },
            },
          },
          'requires a declared sealed type',
          existing: true,
        );
      }
    },
  );
  test('sealed generation shapes retain stream callbacks, DTO lists and input-only defaults', () async {
    final schema = jsonDecode(
      File('../vite-plugin/src/fixtures/sealed-generation-shapes.json')
          .readAsStringSync(),
    ) as Map<String, Object?>;
    await _checkGeneratedConsumer(schema, r"""
import 'dart:typed_data';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart';
class _Callbacks extends HttpTransport {
 _Callbacks():super('http://unused.fixture.invalid');
 var cancelled=false;
 @override Future<void Function()> subscribe<T>(String api,Map<String,dynamic> params,void Function(T) onData,{required T Function(Object?) decoder,GeneratedRequest? request,void Function(LuxoError)? onError,void Function()? onDone}) async {
  expect(api,'watch');expect(request!.toJSONParameters().containsKey('value'),isFalse);
  for(final raw in [null,{'Ready':{}},Uint8List.fromList([0]),Uint8List.fromList([1,1,2,0,0])]) onData(decoder(raw));
  onDone?.call();return (){cancelled=true;};
 }
}
void main() {
 test('native stream decoder and nested sealed-list codecs',() async {
  final source=Batch.fromJson({'choices':[{'Ready':{}}]});
  expect(source.choices.value!.single,isA<Outcome$Ready$1>());
  expect(Batch.fromJson({'choices':null}).choices.value,isNull);
  final input=BatchInput(choices:[OutcomeInput.Ready()]);
  expect(input.toJson(),{'choices':[{'Ready':{}}]});
  final bytes=LuxoEncoder();input.writeLuxo(bytes);expect(bytes.bytes().isNotEmpty,isTrue);
  expect(InputOnly.Write().toJson(),{'Write':{}});
  expect(InputOnly.Write(value:'kept').toJson(),{'Write':{'value':'kept'}});
  final transport=_Callbacks(), client=LuxoClient(_Callbacks());
  client.close();
  final streamClient=LuxoClient(transport);addTearDown(streamClient.close);
  final seen=<Outcome?>[];var done=0;
  final cancel=await streamClient.subscribeWatch(onData:seen.add,onDone:()=>done++);
  expect(seen,[null,isA<Outcome$Ready$1>(),null,isA<Outcome$Ready$1>()]);
  expect(done,1);cancel();expect(transport.cancelled,isTrue);
 });
}
""");
    final invalid = jsonDecode(jsonEncode(schema)) as Map<String, Object?>;
    invalid['types'] = {
      'Outcome': {'name': 'Outcome', 'fields': <Object?>[]},
    };
    await _rejectSchema(invalid, 'duplicate type Outcome', existing: true);
    final duplicate = jsonDecode(jsonEncode(schema)) as Map<String, Object?>;
    duplicate['sealeds'] = {
      'Outcome': {
        'name': 'Outcome',
        'variants': [
          {'id': 1, 'name': 'Ready', 'fields': <Object?>[]},
          {'id': 2, 'name': 'Ready', 'fields': <Object?>[]},
        ],
      },
    };
    await _rejectSchema(duplicate, 'duplicate name Ready', existing: true);
  }, timeout: const Timeout(Duration(seconds: 120)));
  test(
    'sealed Model payload client consumes actual ordinary rows and lists',
    () async {
      final fixture = jsonDecode(
        File('../vite-plugin/src/fixtures/sealed-model.json')
            .readAsStringSync(),
      ) as Map<String, dynamic>;
      await _checkGeneratedConsumer(
        fixture['schema'] as Map<String, Object?>,
        File('test/fixtures/sealed_model_consumer.dart.txt').readAsStringSync(),
        automaticSelection: true,
        expectedSelection: "'Owner{people{name},person{id}}'",
      );
    },
    timeout: const Timeout(Duration(seconds: 120)),
  );
  test(
    'closed generic client retains automatic nested field selections',
    () async {
      final schema = jsonDecode(
        File(
          Platform.environment['LUXO_GENERIC_SCHEMA'] ??
              '../vite-plugin/src/fixtures/closed-generics.json',
        ).readAsStringSync(),
      ) as Map<String, Object?>;
      await _checkGeneratedConsumer(
        schema,
        File('test/fixtures/generic_consumer.dart.txt').readAsStringSync(),
        automaticSelection: true,
        expectedSelection: 'items{value{value}}',
      );
    },
    timeout: const Timeout(Duration(seconds: 120)),
  );
  test(
    'sealed generated client preserves typed requests through both wire modes',
    () async {
      final fixture = jsonDecode(
        File('../../pkg/lux/codec/testdata/sealed-v1.json').readAsStringSync(),
      ) as Map<String, dynamic>;
      final schemaFile = Platform.environment['LUXO_SEALED_SCHEMA'];
      await _checkGeneratedConsumer(
        schemaFile == null
            ? fixture['schema'] as Map<String, Object?>
            : jsonDecode(File(schemaFile).readAsStringSync())
                  as Map<String, Object?>,
        File('test/fixtures/sealed_consumer.dart.txt').readAsStringSync(),
        automaticSelection: true,
        expectedSelection: "'Success{value}'",
      );
    },
    timeout: const Timeout(Duration(seconds: 120)),
  );
  test(
    'sealed cases use static payload codecs and canonical tag frames',
    () async {
      final fixture = jsonDecode(
        File('../../pkg/lux/codec/testdata/sealed-v1.json').readAsStringSync(),
      ) as Map<String, dynamic>;
      await _checkGeneratedConsumer(
        fixture['schema'] as Map<String, Object?>,
        r'''
import 'dart:typed_data';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'schema.dart';
import 'package:luxo_client/src/json.dart';
class _Output implements LuxoJSONOutput {
 final value=StringBuffer();
 @override void writeRaw(String source)=>value.write(source);
 @override void writeJSON(Object? object)=>value.write(encodeLuxoJSON(object));
}
void main() {
 test('closed sum input and selected output', () {
  final seven=LuxoInt64.fromInt(7);
  final input=OutcomeInput.Success(value:seven);
  final encoder=LuxoEncoder();input.writeLuxo(encoder);
  expect(encoder.bytes(),[1,4,0,1,14,0]);
  expect(input.toJson(),{'Success':{'value':BigInt.from(7)}});
  final request=JSONRequestEncoder(luxoSchema).encode('echo',{'value':input});
  expect(request,'{"\$api":"echo","value":{"Success":{"value":7}}}');
  final response=luxoSchema['echo']!.response!;
  expect(decodeLuxoJSON('{"Success":{"value":7}}',response:response),{'Success':{'value':7}});
  for(final raw in ['{}','{"Future":{}}','{"Success":{},"Success":{}}','{"Success":{},"Pending":{}}']) {
   expect(()=>decodeLuxoJSON(raw,response:response),throwsFormatException);
  }
  final output=decodeOutcome(LuxoDecoder(encoder.bytes()));
  expect(output,isA<Outcome$Success$1>());
  expect((output as Outcome$Success$1).value.value,seven);
  final tagOnly=decodeOutcome(LuxoDecoder(Uint8List.fromList([1,2,0,0]))) as Outcome$Success$1;
  expect(tagOnly.value.isSelected,isFalse);
  expect(()=>tagOnly.value.value,throwsStateError);
  expect(OutcomeInput.Failure().toJson(),{'Failure':{}});
  expect(OutcomeInput.Failure(reason:null).toJson(),OutcomeInput.Failure().toJson());
  final omittedDefault=LuxoEncoder(), sentinelDefault=LuxoEncoder();
  OutcomeInput.Failure().writeLuxo(omittedDefault);OutcomeInput.Failure(reason:null).writeLuxo(sentinelDefault);
  expect(sentinelDefault.bytes(),omittedDefault.bytes());
  expect(()=>OutcomeInput.fromJson({'Failure':{'reason':null}}),throwsA(anyOf(isA<FormatException>(),isA<TypeError>())));
  final pending=LuxoEncoder();OutcomeInput.Pending().writeLuxo(pending);
  expect(pending.bytes(),[3,2,0,0]);
  final explicit=OutcomeInput.Nested(child:const LuxoOptional<OutcomeInput>.present(null),
    history:const LuxoOptional<List<OutcomeInput>>.present([]));
  expect(explicit.toJson(),{'Nested':{'child':null,'history':[]}});
  expect(Outcome.fromJson({'Pending':{}}),isA<Outcome$Pending$3>());
  expect(()=>Outcome.fromJson({'Pending':{'extra':1}}),throwsFormatException);
  expect(()=>OutcomeInput.fromJson({'Success':{}}),throwsFormatException);
  expect(()=>OutcomeInput.fromJson({'Pending':{'extra':1}}),throwsFormatException);
  expect(OutcomeInput.fromJson({'Nested':{}}).toJson(), {'Nested':{}});
  expect(OutcomeInput.fromJson({'Nested':{'child':null,'history':[]}}).toJson(),
    {'Nested':{'child':null,'history':[]}});
  expect(()=>decodeOutcome(LuxoDecoder(Uint8List.fromList([0,2,0,0]))),throwsFormatException);
  expect(()=>decodeOutcome(LuxoDecoder(Uint8List.fromList([99,2,0,0]))),throwsFormatException);
  expect(()=>decodeOutcome(LuxoDecoder(Uint8List.fromList([3,2,0,0,0]))),throwsFormatException);
  expect(decodeSealedListEmptyOutcome(Uint8List.fromList([0])),isEmpty);
  expect(()=>decodeEmptyOutcome(LuxoDecoder(Uint8List.fromList([1,2,0,0]))),throwsFormatException);
  expect(JSONRequestEncoder(luxoSchema).encode('echoEmpty',{'value':null}),
    '{"\$api":"echoEmpty","value":null}');
  expect(JSONRequestEncoder(luxoSchema).encode('echoEmptyList',{'values':<EmptyOutcomeInput>[]}),
    '{"\$api":"echoEmptyList","values":[]}');
  OutcomeInput nested=OutcomeInput.Pending();
  for(var i=0;i<31;i++) { nested=OutcomeInput.Nested(child:LuxoOptional<OutcomeInput>.present(nested)); }
  nested.writeLuxo(LuxoEncoder());
  nested.$writeJSON(_Output());
  nested=OutcomeInput.Nested(child:LuxoOptional<OutcomeInput>.present(nested));
  expect(()=>nested.writeLuxo(LuxoEncoder()),throwsFormatException);
  expect(()=>nested.$writeJSON(_Output()),throwsFormatException);

  for(final invalid in <Map<String,dynamic>>[{}, {'Future':{}}, {'Success':{},'Pending':{}}, {'Success':null}]) {
   expect(()=>Outcome.fromJson(invalid),throwsFormatException);
  }
 });
}
''',
        crossPlatform: true,
        compiler: 'dart2js',
      );
    },
    timeout: const Timeout(Duration(seconds: 90)),
  );
  test(
    'sealed generated client narrows and selects the actual branch',
    () async {
      final fixture = jsonDecode(
        File('../../pkg/lux/codec/testdata/sealed-v1.json').readAsStringSync(),
      ) as Map<String, dynamic>;
      await _checkGeneratedConsumer(
        fixture['schema'] as Map<String, Object?>,
        r"""
import 'dart:convert';
import 'package:luxo_client/luxo_client.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart';
void main() {
 test('typed API and automatic branch selection', () async {
  var sent=0;
  final transport=HttpTransport('http://example.test/luxo',client:MockClient((request) async {
   sent++;
   final body=jsonDecode(request.body) as Map<String,dynamic>;
   if(body[r'$api']=='echoEmpty') {
    expect(body.containsKey(r'$select'),isFalse);
    return http.Response('{"data":null}',200);
   }
   expect(body[r'$select'],'Success{value}');
   expect(body['value'],{'Success':{'value':7}});
   return http.Response('{"data":{"Success":{"value":7}}}',200);
  }));
  final client=LuxoClient(transport);
  addTearDown(client.close);
  final result=await client.echo(value:OutcomeInput.Success(value:LuxoInt64.fromInt(7)));
  if(result case Outcome$Success$1(value:Selected(value:final value))) {
   expect(value, LuxoInt64.fromInt(7));
  } else { fail('expected Success'); }
  expect(await client.echoEmpty(value:null),isNull);
  expect(sent,2);
 });
}
""",
        automaticSelection: true,
        expectedSelection: "'Success{value}'",
      );
    },
  );

  test(
    'sealed generated members use stable collision-safe source names',
    () async {
      await _checkGeneratedConsumer(
        {
          'sealeds': {
            'Collision': {
              'name': 'Collision',
              'usage': 'inputOutput',
              'variants': [
                {
                  'id': 1,
                  'name': 'fromJson',
                  'fields': [
                    {
                      'id': 1,
                      'name': 'toJson',
                      'type': 'String',
                      'nullable': true,
                    },
                  ],
                },
              ],
            },
          },
          'apis': {
            'echo': {
              'id': 1,
              'name': 'echo',
              'module': 'test',
              'params': [
                {
                  'id': 1,
                  'name': 'value',
                  'type': 'Sealed',
                  'typeName': 'Collision',
                },
              ],
              'returnType': 'Collision',
            },
          },
        },
        r"""
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
void main() {
 test('static source alias preserves wire field', () async {
  final input=CollisionInput.fromJson$($field1:'x');
  expect(input.toJson(), {'fromJson':{'toJson':'x'}});
  final encoder=LuxoEncoder();input.writeLuxo(encoder);
  final output=decodeCollision(LuxoDecoder(encoder.bytes())) as Collision$fromJson$1;
  expect(output.$field1.value,'x');
  final nullable=CollisionInput.fromJson$($field1:null);
  expect(nullable.toJson(),{'fromJson':{'toJson':null}});
  final nullEncoder=LuxoEncoder();nullable.writeLuxo(nullEncoder);
  expect((decodeCollision(LuxoDecoder(nullEncoder.bytes())) as Collision$fromJson$1).$field1.value,isNull);
  final transport=HttpTransport('http://example.test/luxo',client:MockClient((request) async {
   expect(request.body,contains('"\$select":"fromJson{toJson}"'));
   return http.Response('{"data":{"fromJson":{"toJson":"x"}}}',200);
  }));
  final client=LuxoClient(transport);addTearDown(client.close);
  final result=await client.echo(value:input);
  if(result is Collision$fromJson$1) { expect(result.$field1.value,'x'); }
  else { fail('expected fromJson case'); }
 });
}
""",
        automaticSelection: true,
        expectedSelection: "'fromJson{toJson}'",
      );
    },
  );

  test(
    'Unicode type and same-name field use distinct source namespaces',
    () async {
      await _checkGeneratedConsumer(
        {
          'models': {
            'Café': {
              'name': 'Café',
              'usage': 'output',
              'fields': [
                {'id': 1, 'name': 'Café', 'type': 'String'},
              ],
            },
          },
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Café',
            },
          },
        },
        r'''
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
void main() {
 test('same-name member remains independently accessible', () {
  final row=$Luxo$43_61_66_e9.fromJson({'Café':'coffee'});
  expect(row.$luxo$43_61_66_e9.value,'coffee');
  expect(row.toJson(),{'Café':'coffee'});
 });
}
''',
      );
    },
  );
  test('rejects non-DSL aliases before writing generated names', () async {
    for (final raw in [r'$luxo$63_61_66_e9', r'$Luxo$43_61_66_e9']) {
      await _rejectSchema(
        {
          'models': {
            'Record': {
              'name': 'Record',
              'fields': [
                {'id': 1, 'name': 'café', 'type': 'String'},
                {'id': 2, 'name': raw, 'type': 'String'},
              ],
            },
          },
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Record',
            },
          },
        },
        'identifier',
        existing: true,
      );
    }
  });
  test(
    'Unicode source aliases preserve API field enum and binary wire names',
    () async {
      final schema = jsonDecode(
        r'''{"models":{"Café":{"name":"Café","usage":"inputOutput","fields":[{"id":1,"name":"café","type":"String"},{"id":2,"name":"状态","type":"Enum","typeName":"État"}]}},"enums":{"État":{"name":"État","values":["PRÊT","中文"]}},"apis":{"café":{"id":7,"name":"café","module":"test","returnType":"Café","params":[{"id":1,"name":"entrée","type":"Model","typeName":"Café"}]}}}''',
      ) as Map<String, Object?>;
      schema['types'] = {
        'Box': {
          'name': 'Box',
          'usage': 'inputOutput',
          'fields': [
            {
              'id': 1,
              'name': '飲物',
              'type': 'Model',
              'typeName': 'Café',
              'isList': true,
            },
          ],
        },
      };
      (schema['apis'] as Map<String, dynamic>)['box'] = {
        'id': 9,
        'name': 'box',
        'module': 'test',
        'returnType': 'Box',
        'params': [
          {'id': 1, 'name': 'payload', 'type': 'Model', 'typeName': 'Box'},
        ],
      };
      (schema['types'] as Map<String, dynamic>)['LuxoQuery_listé'] = {
        'name': 'LuxoQuery_listé',
        'usage': 'output',
        'fields': [
          {'id': 1, 'name': 'label', 'type': 'String'},
        ],
      };
      (schema['apis'] as Map<String, dynamic>)['readQueryType'] = {
        'id': 10,
        'name': 'readQueryType',
        'module': 'test',
        'returnType': 'LuxoQuery_listé',
      };
      (schema['apis'] as Map<String, dynamic>)['listé'] = {
        'id': 8,
        'name': 'listé',
        'module': 'test',
        'returnType': 'Café',
        'paginated': true,
        'params': [
          {'id': 1, 'name': 'page', 'type': 'Int', 'hasDefault': true},
          {'id': 2, 'name': 'pageSize', 'type': 'Int', 'hasDefault': true},
        ],
        'query': {
          'filters': [
            {
              'field': 'café',
              'type': 'String',
              'operators': ['eq'],
            },
            {
              'field': '状态',
              'type': 'État',
              'operators': ['eq'],
            },
          ],
          'sorters': ['café'],
        },
      };
      await _checkGeneratedConsumer(schema, r'''
import 'dart:convert';
import 'package:luxo_client/luxo_client.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'queries.dart';
import 'client.dart' as sdk;
void main() {
 test('Unicode aliases use original wire strings and IDs', () async {
  expect(LuxoQuery_$luxo$6c_69_73_74_e9.$luxo$63_61_66_e9_eq('coffee').field,'café');
  expect(LuxoQuery_$luxo$6c_69_73_74_e9.$luxo$72b6_6001_eq($Luxo$c9_74_61_74.fromValue('PRÊT')).value,'PRÊT');
  expect(LuxoQuery_$luxo$6c_69_73_74_e9.$luxo$63_61_66_e9_desc().field,'café');
  final input=$Luxo$43_61_66_e9Input($luxo$63_61_66_e9:'coffee', $luxo$72b6_6001:$Luxo$c9_74_61_74.fromValue('PRÊT'));
  expect(input.toJson(), {'café':'coffee','状态':'PRÊT'});
  final box=BoxInput($luxo$98f2_7269:[input]);
  expect(Box.fromJson(box.toJson()).$luxo$98f2_7269.value.first.$luxo$63_61_66_e9.value,'coffee');
  final encoder=LuxoEncoder();input.writeLuxo(encoder);
  final decoded=decode$Luxo$43_61_66_e9(LuxoDecoder(encoder.bytes()));
  expect(decoded.$luxo$63_61_66_e9.value,'coffee');
  expect(decoded.$luxo$72b6_6001.value.rawValue,'PRÊT');
  expect($Luxo$c9_74_61_74Known.$luxo$4e2d_6587.rawValue,'中文');
  var requests=0;
  final transport=HttpTransport('http://localhost/luxo',options:const TransportOptions(mode:TransportMode.json),client:MockClient((request) async {
   requests++;
   final body=jsonDecode(request.body) as Map<String,dynamic>;
   expect(body['entrée'],{'café':'coffee','状态':'PRÊT'});
   expect(body[r'$select'],'café,状态');
   expect(body[r'$api'],'café');
   return http.Response('{"data":{"café":"coffee","状态":"PRÊT"}}',200,headers:{'content-type':'application/json'});
  }));
  try {
   final row=await sdk.LuxoClient(transport).$luxo$63_61_66_e9($luxo$65_6e_74_72_e9_65:input);
   expect(row.$luxo$63_61_66_e9.value,'coffee');expect(row.$luxo$72b6_6001.value.rawValue,'PRÊT');expect(requests,1);
  } finally {transport.close();}
 });
}
''', automaticSelection: true);
    },
  );
  for (final target in ['model', 'type', 'enum', 'api', 'field', 'param']) {
    test('reject empty schema names for $target before writes', () async {
      final field = {'id': 1, 'name': 'value', 'type': 'Int'};
      final schema = <String, Object?>{'models': {}, 'apis': {}};
      if (target == 'model')
        schema['models'] = {
          '': {
            'name': '',
            'fields': [field],
          },
        };
      if (target == 'type')
        schema['types'] = {
          '': {
            'name': '',
            'fields': [field],
          },
        };
      if (target == 'enum')
        schema['enums'] = {
          '': {
            'name': '',
            'values': ['READY'],
          },
        };
      if (target == 'api')
        schema['apis'] = {
          '': {'id': 1, 'name': '', 'module': 'test', 'returnType': 'Int'},
        };
      if (target == 'field') {
        schema['models'] = {
          'Payload': {
            'name': 'Payload',
            'fields': [
              {...field, 'name': ''},
            ],
          },
        };
        schema['apis'] = {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Payload',
          },
        };
      }
      if (target == 'param')
        schema['apis'] = {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Int',
            'params': [
              {...field, 'name': ''},
            ],
          },
        };
      await _rejectSchema(schema, 'empty', existing: true);
    });
  }
  for (final target in ['models', 'types', 'params', 'unused']) {
    for (final type in ['', 'Missing', 'Int64']) {
      for (final list in [false, true]) {
        test(
          'reject undeclared member type $target/$type/list=$list',
          () async {
            await _rejectSchema(
              _enumReferenceSchema(target, null, list, type: type),
              'type',
              existing: true,
            );
          },
        );
      }
    }
  }

  test('input helper aliases preserve independent business types', () async {
    final schema = jsonDecode(
      r'''{"models":{"User":{"name":"User","usage":"inputOutput","fields":[{"id":1,"name":"id","type":"Int"},{"id":2,"name":"parent","type":"Model","typeName":"User","nullable":true}]},"LuxoUser":{"name":"LuxoUser","usage":"inputOutput","fields":[{"id":1,"name":"id","type":"Int"}]}},"types":{"UserInput":{"name":"UserInput","usage":"output","fields":[{"id":1,"name":"label","type":"String"}]},"LuxoUserInput2":{"name":"LuxoUserInput2","usage":"output","fields":[{"id":1,"name":"label","type":"String"}]}},"apis":{"readUser":{"id":1,"name":"readUser","module":"test","returnType":"User"},"writeUser":{"id":2,"name":"writeUser","module":"test","returnType":"Int","params":[{"id":1,"name":"input","type":"Model","typeName":"User"}]}}}''',
    ) as Map<String, Object?>;
    await _checkGeneratedConsumer(schema, r'''
import 'package:luxo_client/luxo_client.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart' as sdk;
void main() {
 test('aliased inputs compile, encode nested values and keep output fields', () async {
  final input=LuxoUserInput3(id:luxoInt64FromJSON(1),parent:LuxoUserInput3(id:luxoInt64FromJSON(2)));
  final encoder=LuxoEncoder();input.writeLuxo(encoder);
  expect(encoder.bytes(), [0,1,2,2,1,0,1,4,2,0,0,0]);
  expect(input.parent!.id,luxoInt64FromJSON(2));
  expect(const UserInput(label:Selected.value('ok')).label.value,'ok');
  final transport=HttpTransport('http://localhost/luxo',options:const TransportOptions(mode:TransportMode.binary),client:MockClient((_)async=>http.Response.bytes([2],200,headers:{'content-type':'application/x-luxo'})));
  addTearDown(transport.close);
  expect(await sdk.LuxoClient(transport).writeUser(input:input),luxoInt64FromJSON(1));
 });
}
''');
  });

  test(
    'generated binary arrays reject oversized top level and nested lists',
    () async {
      final schema = jsonDecode(
        r'''{"models":{},"types":{"Input":{"name":"Input","usage":"input","fields":[{"id":1,"name":"flags","type":"Boolean","isList":true}]}},"apis":{"save":{"id":7,"name":"save","module":"test","returnType":"Int","params":[{"id":1,"name":"flags","type":"Boolean","isList":true},{"id":2,"name":"input","type":"Model","typeName":"Input"}]}}}''',
      ) as Map<String, Object?>;
      await _checkGeneratedConsumer(schema, r'''
import 'package:luxo_client/luxo_client.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart' as sdk;
void main() {
 test('oversized lists fail before the transport sends', () async {
  var sends=0;
  final transport=HttpTransport('http://localhost/luxo',options:const TransportOptions(mode:TransportMode.binary),client:MockClient((request)async{sends++;return http.Response.bytes([0],200);}));
  final client=sdk.LuxoClient(transport);
  try {
   for(final nested in [false,true]) {
    final values=List<bool>.filled(1000001,false);
    final future=client.save(flags:nested?[]:values,input:Input(flags:nested?values:[]));
    await expectLater(future,throwsA(isA<RangeError>()));
   }
   expect(sends,0);
  } finally {transport.close();}
 });
}
''');
    },
  );

  test('builtin numeric names stay separate from scalars', () async {
    final schema = jsonDecode(r'''{"models":{},"enums":{
      "Double":{"name":"Double","values":["OPEN"]},"Int64":{"name":"Int64","values":["OPEN"]}},
      "types":{"Payload":{"name":"Payload","usage":"inputOutput","fields":[
        {"id":1,"name":"score","type":"Float"},{"id":2,"name":"nanos","type":"Duration"},
        {"id":3,"name":"state","type":"Enum","typeName":"Double"},{"id":4,"name":"code","type":"Enum","typeName":"Int64"}]}},
      "apis":{"echo":{"id":1,"name":"echo","module":"test","returnType":"Payload","params":[{"id":1,"name":"input","type":"Model","typeName":"Payload"}]},
        "score":{"id":2,"name":"score","module":"test","returnType":"Float","params":[{"id":1,"name":"value","type":"Float"}]},
        "nanos":{"id":3,"name":"nanos","module":"test","returnType":"Duration","params":[{"id":1,"name":"value","type":"Duration"}]}}}
    ''') as Map<String, Object?>;
    await _checkGeneratedConsumer(schema, r'''
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart' as sdk;
void main() {
  test('numeric and enum values', () {
    expect(sdk.LuxoClient, isA<Type>());
    for (final text in ['-9223372036854775808', '9223372036854775807']) {
      final nanos = luxoInt64FromJSON(LuxoJSONNumber.parse(text));
      final input = PayloadInput(score: 1.25, nanos: nanos,
        state: Double.fromValue('OPEN'), code: Int64.fromValue('FUTURE'));
      final encoder = LuxoEncoder();
      input.writeLuxo(encoder);
      final json = decodeLuxoJSON('{"score":1.25,"nanos":$text,"state":"OPEN","code":"FUTURE"}') as Map<String, Object?>;
      for (final value in [decodePayload(LuxoDecoder(encoder.bytes())), Payload.fromJson(json)]) {
        expect(value.score.value, 1.25);
        expect(value.nanos.value, nanos);
        expect(value.state.value.rawValue, 'OPEN');
        expect(value.code.value.rawValue, 'FUTURE');
      }
    }
  });
}
''');
  });

  test('preserves case-distinct generated enum members', () async {
    await _checkGeneratedConsumer(
      {
        'models': <String, Object?>{},
        'apis': <String, Object?>{},
        'enums': {
          'State': {
            'name': 'State',
            'values': ['Open', 'OPEN'],
          },
        },
      },
      r'''import 'package:test/test.dart';
import 'types.dart';
void main() {
  test('case distinction', () {
    final mixed = State.fromValue('Open');
    final upper = State.fromValue('OPEN');
    expect(mixed, isA<StateKnown>());
    expect(upper, isA<StateKnown>());
    expect(mixed, isNot(upper));
    expect(mixed.rawValue, 'Open');
    expect(upper.rawValue, 'OPEN');
    expect(State.fromValue('open'), isA<StateUnknown>());
  });
}
''',
    );
  });
  test(
    'business Future type preserves asynchronous client signatures',
    () async {
      await _checkGeneratedConsumer(
        {
          'models': {
            'Future': {
              'name': 'Future',
              'fields': [
                {'id': 1, 'name': 'id', 'type': 'Int'},
              ],
            },
          },
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Future',
            },
            'watch': {
              'id': 2,
              'name': 'watch',
              'module': 'test',
              'returnType': 'Future',
              'stream': true,
            },
          },
        },
        r'''
import 'package:test/test.dart';
import 'client.dart';
import 'types.dart' as model;
void main() {
  test('business Future remains a model', () {
    expect(LuxoClient.schema.containsKey('read'), isTrue);
    expect(model.Future.fromJson({'id': 42}).id.value.toString(), '42');
  });
}
''',
      );
    },
  );

  test('introspection rejects redirects without forwarding key', () async {
    final source = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final target = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final root = Directory('.tmp')..createSync(recursive: true);
    final output = root.createTempSync('introspection-redirect-');
    String? receivedKey;
    target.listen((request) async {
      receivedKey = request.headers.value('X-Introspection-Key');
      request.response.write(jsonEncode(_schema));
      await request.response.close();
    });
    source.listen((request) async {
      request.response.statusCode = HttpStatus.found;
      request.response.headers.set(
        HttpHeaders.locationHeader,
        'http://${target.address.host}:${target.port}/capture',
      );
      await request.response.close();
    });
    try {
      Object? failure;
      try {
        await generate(
          endpoint: 'http://${source.address.host}:${source.port}/luxo',
          key: 'test-secret',
          outDir: output.path,
        );
      } catch (error) {
        failure = error;
      }
      expect(receivedKey, isNull);
      expect(failure.toString(), contains('HTTP 302'));
      expect(output.listSync(), isEmpty);
    } finally {
      await source.close(force: true);
      await target.close(force: true);
      output.deleteSync(recursive: true);
    }
  });
  test('introspection preserves repeated query parameters', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final root = Directory('.tmp')..createSync(recursive: true);
    final output = root.createTempSync('introspection-query-');
    final received = Completer<Uri>();
    server.listen((request) async {
      received.complete(request.uri);
      request.response.headers.contentType = ContentType.json;
      request.response.write(jsonEncode(_schema));
      await request.response.close();
    });
    try {
      await generate(
        endpoint:
            'http://${server.address.host}:${server.port}/luxo'
            '?tag=one&tag=two&tenant=a%26b&%24schema=old#section',
        key: 'test',
        outDir: output.path,
      );
      final uri = await received.future;
      expect(uri.queryParametersAll['tag'], ['one', 'two']);
      expect(uri.queryParametersAll['tenant'], ['a&b']);
      expect(uri.queryParametersAll[r'$schema'], ['']);
      expect(uri.fragment, isEmpty);
      expect(File('${output.path}/client.dart').existsSync(), isTrue);
    } finally {
      await server.close(force: true);
      output.deleteSync(recursive: true);
    }
  });
  for (final pair in ['model-type', 'model-enum', 'type-enum']) {
    test('reject cross-namespace type collision $pair', () async {
      final declaration = {
        'name': 'Payload',
        'fields': [
          {'id': 1, 'name': 'id', 'type': 'Int'},
        ],
      };
      await _rejectSchema(
        {
          if (pair.contains('model')) 'models': {'Payload': declaration},
          if (pair.contains('type')) 'types': {'Payload': declaration},
          if (pair.contains('enum'))
            'enums': {
              'Payload': {
                'name': 'Payload',
                'values': ['OPEN'],
              },
            },
        },
        'duplicate type',
        existing: true,
      );
    });
  }
  for (final target in ['models', 'types', 'params']) {
    for (final type in ['Int', 'String']) {
      test(
        'reject duplicate $target names with distinct IDs ($type)',
        () async {
          final fields = [
            {'id': 1, 'name': 'value', 'type': 'Int'},
            {'id': 2, 'name': 'value', 'type': type},
          ];
          await _rejectSchema(
            target == 'params'
                ? {
                    'apis': {
                      'read': {
                        'id': 1,
                        'name': 'read',
                        'module': 'test',
                        'returnType': 'Int',
                        'params': fields,
                      },
                    },
                  }
                : {
                    target: {
                      'Payload': {'name': 'Payload', 'fields': fields},
                    },
                  },
            'duplicate name value',
            existing: true,
          );
        },
      );
    }
  }
  for (final target in ['models', 'types', 'apis']) {
    test('reject mismatched $target declaration names', () async {
      await _rejectSchema(
        {
          target: {
            'Payload': target == 'apis'
                ? {
                    'id': 1,
                    'name': 'Other',
                    'module': 'test',
                    'returnType': 'Int',
                  }
                : {
                    'name': 'Other',
                    'fields': [
                      {'id': 1, 'name': 'id', 'type': 'Int'},
                    ],
                  },
          },
        },
        'does not match',
        existing: true,
      );
    });
  }
  test('declared model and recursive type references remain valid', () async {
    await _checkGeneratedConsumer(
      {
        'models': {
          'Container': {
            'name': 'Container',
            'fields': [
              {'id': 1, 'name': 'node', 'type': 'Model', 'typeName': 'Node'},
            ],
          },
        },
        'types': {
          'Node': {
            'name': 'Node',
            'fields': [
              {
                'id': 1,
                'name': 'child',
                'type': 'Model',
                'typeName': 'Node',
                'nullable': true,
              },
            ],
          },
        },
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Container',
          },
        },
      },
      r'''
import 'package:test/test.dart';
import 'types.dart';
void main() {
  test('recursive output decodes', () {
    final value = Container.fromJson({'node': {'child': {'child': null}}});
    expect(value.node.value.child.value!.child.value, isNull);
  });
}
''',
    );
  });

  for (final compiler in ['kernel', 'dart2js', 'dart2wasm']) {
    test(
      'generated model readers and writers enforce 64 object levels ($compiler)',
      () async {
        await _checkGeneratedConsumer(
          {
            'models': {
              'Container': {
                'name': 'Container',
                'fields': [
                  {
                    'id': 1,
                    'name': 'node',
                    'type': 'Model',
                    'typeName': 'Node',
                  },
                ],
              },
            },
            'types': {
              'Node': {
                'name': 'Node',
                'fields': [
                  for (final (id, name, list) in [
                    (1, 'child', false),
                    (2, 'children', true),
                  ])
                    {
                      'id': id,
                      'name': name,
                      'type': 'Model',
                      'typeName': 'Node',
                      'nullable': true,
                      'isList': list,
                    },
                  {
                    'id': 3,
                    'name': 'metadata',
                    'type': 'JSON',
                    'nullable': true,
                  },
                ],
              },
            },
            'apis': {
              'read': {
                'id': 1,
                'name': 'read',
                'module': 'test',
                'returnType': 'Container',
                'params': [
                  {
                    'id': 1,
                    'name': 'value',
                    'type': 'Model',
                    'typeName': 'Node',
                  },
                  {'id': 2, 'name': 'control', 'type': 'Int'},
                ],
              },
            },
          },
          File('test/fixtures/model_depth_consumer.dart.txt')
              .readAsStringSync(),
          crossPlatform: true,
          compiler: compiler,
        );
      },
    );
  }

  for (final compiler in ['kernel', 'dart2js', 'dart2wasm']) {
    test('JSON writer SDK types preserve business names ($compiler)', () async {
      final names = ['LuxoJSONWritable', 'LuxoJSONOutput'];
      await _checkGeneratedConsumer(
        {
          'models': {
            for (final name in names)
              name: {
                'name': name,
                'fields': [
                  {'id': 1, 'name': 'label', 'type': 'String'},
                  {'id': 2, 'name': 'normalizeLuxoJSONList', 'type': 'String'},
                  {
                    'id': 3,
                    'name': 'jsons',
                    'type': 'JSON',
                    'isList': true,
                    'nullable': true,
                  },
                ],
              },
          },
          'apis': {
            for (final (index, name) in names.indexed)
              'echo$index': {
                'id': index + 1,
                'name': 'echo$index',
                'module': 'test',
                'returnType': name,
                'params': [
                  {'id': 1, 'name': 'value', 'type': 'Model', 'typeName': name},
                ],
              },
          },
        },
        r'''
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart' as generated_client;
void main() {
  test('SDK names retain typed models, requests and static JSON writers', () async {
    const first = LuxoJSONWritableInput(label: 'one', normalizeLuxoJSONList: 'shadow', jsons: [true]);
    const second = LuxoJSONOutputInput(label: 'two', normalizeLuxoJSONList: 'shadow', jsons: [true]);
    final one = LuxoEncoder(); first.writeLuxo(one);
    final two = LuxoEncoder(); second.writeLuxo(two);
    final LuxoJSONWritable decodedOne = decodeLuxoJSONWritable(LuxoDecoder(one.bytes()));
    final LuxoJSONOutput decodedTwo = decodeLuxoJSONOutput(LuxoDecoder(two.bytes()));
    expect(decodedOne.label.value, 'one');
    expect(decodedTwo.label.value, 'two');
    expect(decodedOne.toJson()['jsons'], [true]);
    expect(first.toJson()['normalizeLuxoJSONList'], 'shadow');
    expect(encodeLuxoJSON(decodedOne), '{"label":"one","normalizeLuxoJSONList":"shadow","jsons":[true]}');
    expect(encodeLuxoJSON(decodedTwo), '{"label":"two","normalizeLuxoJSONList":"shadow","jsons":[true]}');
    final transport = HttpTransport('http://example.test/luxo', client: MockClient((request) async {
      final body = decodeLuxoJSON(request.body) as Map;
      return http.Response(encodeLuxoJSON({'data': body['value']}), 200);
    }));
    addTearDown(transport.close);
    final client = generated_client.LuxoClient(transport);
    expect((await client.echo0(value: first)).label.value, 'one');
    expect((await client.echo1(value: second)).label.value, 'two');
  });
}
''',
        crossPlatform: true,
        compiler: compiler,
      );
    });
    test(
      'JSON writer SDK names preserve enum-only clients ($compiler)',
      () async {
        await _checkGeneratedConsumer(
          {
            'enums': {
              for (final name in ['LuxoJSONWritable', 'LuxoJSONOutput'])
                name: {
                  'name': name,
                  'values': ['READY'],
                },
            },
            'apis': {
              'first': {
                'id': 1,
                'name': 'first',
                'module': 'test',
                'returnType': 'LuxoJSONWritable',
              },
              'second': {
                'id': 2,
                'name': 'second',
                'module': 'test',
                'returnType': 'LuxoJSONOutput',
              },
            },
          },
          r'''
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
import 'client.dart' as generated_client;
void main() {
  test('enum-only SDK names keep their wire values and public types', () async {
    final transport = HttpTransport('http://example.test/luxo', client: MockClient((_) async => http.Response('{"data":"READY"}', 200)));
    addTearDown(transport.close);
    final client = generated_client.LuxoClient(transport);
    final LuxoJSONWritable first = await client.first();
    final LuxoJSONOutput second = await client.second();
    expect(first.rawValue, 'READY');
    expect(second.rawValue, 'READY');
  });
}
''',
          crossPlatform: true,
          compiler: compiler,
        );
      },
    );
  }

  test('generated input fields do not shadow the Binary encoder', () async {
    final variants = ['Scalar', 'Optional', 'Sequence', 'SequenceItem'];
    final schema = <String, Object?>{
      'types': {
        for (final name in variants)
          name: {
            'name': name,
            'fields': [
              {
                'id': 1,
                'name': name == 'SequenceItem' ? 'item' : 'encoder',
                'type': 'String',
                'nullable': name == 'Optional',
                'isList': name == 'Sequence' || name == 'SequenceItem',
              },
            ],
          },
      },
      'apis': {
        for (final (index, name) in variants.indexed)
          'echo$name': {
            'id': index + 1,
            'name': 'echo$name',
            'module': 'test',
            'returnType': name,
            'params': [
              {'id': 1, 'name': 'value', 'type': 'Model', 'typeName': name},
            ],
          },
      },
    };
    await _checkGeneratedConsumer(schema, r'''
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'types.dart';
void main() {
  test('encoder field round trips', () {
    final scalar = LuxoEncoder();
    const ScalarInput(encoder: 'hello').writeLuxo(scalar);
    expect(decodeScalar(LuxoDecoder(scalar.bytes())).encoder.value, 'hello');
    for (final value in <String?>[null, 'world']) {
      final optional = LuxoEncoder();
      OptionalInput(encoder: value).writeLuxo(optional);
      expect(decodeOptional(LuxoDecoder(optional.bytes())).encoder.value, value);
    }
    final sequence = LuxoEncoder();
    const SequenceInput(encoder: ['a', 'b']).writeLuxo(sequence);
    expect(decodeSequence(LuxoDecoder(sequence.bytes())).encoder.value, ['a', 'b']);
    final items = LuxoEncoder();
    const SequenceItemInput(item: ['c', 'd']).writeLuxo(items);
    expect(decodeSequenceItem(LuxoDecoder(items.bytes())).item.value, ['c', 'd']);
  });
}
''');
  });

  test('reject field collisions with generated Dart members', () async {
    for (final input in [false, true]) {
      final names = [
        'toJson',
        'fromJson',
        'hashCode',
        'runtimeType',
        'toString',
        'noSuchMethod',
        'User',
        if (input) ...['writeLuxo', 'toLuxoParameters'],
        r'_$json',
        r'_$toJson',
        r'$writeJSON',
        r'_$writeJSON',
        if (input) r'_$writeLuxo',
        if (!input) r'$fromJsonItem',
      ];
      for (final name in names) {
        await _rejectSchema(
          {
            'models': {
              'User': {
                'name': 'User',
                'fields': [
                  {'id': 1, 'name': name, 'type': 'String'},
                ],
              },
            },
            'apis': {
              'read': {
                'id': 1,
                'name': 'read',
                'module': 'test',
                'returnType': input ? 'String' : 'User',
                if (input)
                  'params': [
                    {
                      'id': 1,
                      'name': 'value',
                      'type': 'Model',
                      'typeName': 'User',
                    },
                  ],
              },
            },
          },
          name.contains(r'$')
              ? 'invalid member identifier: $name'
              : 'generated Dart member conflicts with field: User.$name',
          existing: true,
        );
      }
    }
  });

  test(
    'shared input declarations validate their actual constructor name',
    () async {
      for (final collection in ['types', 'models']) {
        await _rejectSchema(
          {
            collection: {
              'User': {
                'name': 'User',
                'fields': [
                  {'id': 1, 'name': 'UserInput', 'type': 'String'},
                ],
              },
            },
            'apis': {
              'echo': {
                'id': 1,
                'name': 'echo',
                'module': 'test',
                'returnType': 'User',
                'params': [
                  {
                    'id': 1,
                    'name': 'value',
                    'type': 'Model',
                    'typeName': 'User',
                  },
                ],
              },
            },
          },
          'generated Dart member conflicts with field: UserInput.UserInput',
          existing: true,
        );
      }
    },
  );

  test('output fields may use input-only generated member names', () async {
    await _checkGeneratedConsumer(
      {
        'models': {
          'User': {
            'name': 'User',
            'fields': [
              {'id': 1, 'name': 'writeLuxo', 'type': 'String'},
              {'id': 2, 'name': 'toLuxoParameters', 'type': 'String'},
            ],
          },
        },
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'User',
          },
        },
      },
      r'''
import 'package:test/test.dart';
import 'types.dart';
void main() {
  test('output-only members remain available', () {
    final data = {'writeLuxo': 'first', 'toLuxoParameters': 'second'};
    final value = User.fromJson(data);
    expect(value.writeLuxo.value, 'first');
    expect(value.toLuxoParameters.value, 'second');
    expect(value.toJson(), data);
  });
}
''',
    );
  });

  test(
    'generated nullable benchmark fixtures preserve the wire contract',
    () async {
      final result = await _runDart([
        '--packages=.dart_tool/package_config.json',
        'benchmark/nullable_root_benchmark.dart',
        'verify',
      ]);
      expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
      expect(
        result.stdout,
        contains('verified 11 generated nullable root fixtures'),
      );
    },
    timeout: const Timeout(Duration(seconds: 65)),
  );
  for (final mode in [
    <String>[],
    ['terminal'],
  ]) {
    test('current WebSocket benchmark compiles ${mode.join()}', () async {
      final result = await _runDart([
        'run',
        'benchmark/websocket_subscriptions_benchmark.dart',
        'exe',
        ...mode,
        '--verify',
      ]);
      expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
    });
  }
  for (final missing in ['page', 'pageSize']) {
    test('reject missing stable pagination parameter $missing', () async {
      await _rejectSchema(
        {
          'apis': {
            'browse': {
              'id': 1,
              'name': 'browse',
              'module': 'test',
              'returnType': 'Int',
              'paginated': true,
              'params': [
                for (final name in ['page', 'pageSize'])
                  if (name != missing)
                    {
                      'id': name == 'page' ? 7 : 9,
                      'name': name,
                      'type': 'Int',
                      'hasDefault': true,
                    },
              ],
            },
          },
        },
        'pagination',
        existing: true,
      );
    });
  }
  for (final target in ['models', 'types', 'apis', 'params']) {
    final limit = target == 'apis'
        ? 2147483647
        : target == 'params'
        ? 2147483645
        : 65535;
    for (final id in [0, -1, limit + 1, 9007199254740991]) {
      test('reject stable $target ID $id before writing', () async {
        await _rejectSchema(_numberedSchema(target, id), 'ID', existing: true);
      });
    }
    test('reject stable duplicate $target IDs', () async {
      await _rejectSchema(
        _numberedSchema(target, 1, duplicate: true),
        'duplicate ID',
      );
    });
  }
  test('internal routes use explicit metadata instead of names', () async {
    final root = Directory('.tmp')..createSync(recursive: true);
    final output = root.createTempSync('internal-routes-');
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((request) async {
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'models': <String, Object>{},
          'apis': {
            for (final entry in {
              'ping': false,
              'privateOperation': true,
            }.entries)
              entry.key: {
                'id': entry.value ? 2 : 1,
                'name': entry.key,
                'module': 'test',
                'returnType': 'Int',
                'internal': entry.value,
              },
          },
        }),
      );
      await request.response.close();
    });
    try {
      await generate(
        endpoint: 'http://${server.address.host}:${server.port}',
        key: 'test',
        outDir: output.path,
      );
      for (final file in ['client.dart', 'schema.dart']) {
        final source = File('${output.path}/$file').readAsStringSync();
        expect(source, contains('ping'));
        expect(source, isNot(contains('privateOperation')));
      }
    } finally {
      await server.close(force: true);
      output.deleteSync(recursive: true);
    }
  });
  test('process budget reports an explicit timeout', () async {
    final root = Directory('.tmp')..createSync(recursive: true);
    final output = root.createTempSync('process-budget-');
    final script = File('${output.path}/hold.dart')
      ..writeAsStringSync(
        'Future<void> main() => Future<void>.delayed(const Duration(minutes: 1));\n',
      );
    try {
      await expectLater(
        _runProcess(Platform.resolvedExecutable, [
          script.path,
        ], timeout: Duration.zero),
        throwsA(
          isA<TimeoutException>().having(
            (error) => error.duration,
            'budget',
            Duration.zero,
          ),
        ),
      );
    } finally {
      output.deleteSync(recursive: true);
    }
  });
  test('process budget preserves successful output', () async {
    final result = await _runProcess(Platform.resolvedExecutable, [
      '--version',
    ]);
    expect(result.exitCode, 0);
    expect('${result.stdout}${result.stderr}', contains('Dart SDK version'));
  });
  for (final target in ['models', 'types', 'params']) {
    for (final name in [null, '', 'Missing']) {
      for (final list in [false, true]) {
        test(
          'reject missing model declaration $target/$name/list=$list',
          () async {
            await _rejectSchema(
              _enumReferenceSchema(target, name, list, type: 'Model'),
              'requires a declared model or type',
              existing: true,
            );
          },
        );
      }
    }
  }
  for (final target in ['models', 'types', 'params']) {
    for (final name in [null, '', 'Missing']) {
      for (final list in [false, true]) {
        test(
          'reject missing enum declaration $target/$name/list=$list',
          () async {
            await _rejectSchema(
              _enumReferenceSchema(target, name, list),
              'requires a declared enum type',
            );
          },
        );
      }
    }
  }
  for (final list in [false, true]) {
    test('reject Void API return list=$list', () async {
      await _rejectSchema(
        {
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Void',
              'returnList': list,
            },
          },
        },
        'return type',
        existing: true,
      );
    });
    test('reject omitted API return list=$list', () async {
      await _rejectSchema(
        {
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnList': list,
            },
          },
        },
        'return type',
        existing: true,
      );
    });
    test('reject undeclared API return list=$list', () async {
      await _rejectSchema({
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Missing',
            'returnList': list,
          },
        },
      }, 'has an undeclared return type');
    });
    test('reject bare Enum API return list=$list', () async {
      await _rejectSchema({
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Enum',
            'returnList': list,
          },
        },
      }, 'requires a named enum return type');
    });
  }
  test('reject enum declaration with a different lookup name', () async {
    await _rejectSchema({
      'enums': {
        'State': {
          'name': 'Other',
          'values': ['OPEN'],
        },
      },
    }, 'does not match key');
  });
  test(
    'reject invalid enum Schema without replacing an existing client',
    () async {
      await _rejectSchema(
        _enumReferenceSchema('params', null, false),
        'requires a declared enum type',
        existing: true,
      );
    },
  );
  for (final values in <List<String>>[
    ['OPEN', 'OPEN'],
  ]) {
    test('reject invalid enum members $values before writing types', () async {
      await _rejectSchema({
        'enums': {
          'State': {'name': 'State', 'values': values},
        },
      }, 'requires distinct members');
    });
  }
  for (final suffix in ['Known', 'Unknown']) {
    test('reject generated enum $suffix name collisions', () async {
      await _rejectSchema({
        'enums': {
          'State': {
            'name': 'State',
            'values': ['OPEN'],
          },
          'State$suffix': {
            'name': 'State$suffix',
            'values': ['READY'],
          },
        },
      }, 'conflicts with Schema');
    });
  }
  test(
    'generated client preserves binary list, Bytes, JSON, and field IDs',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final output = Directory('.tmp/codegen_test');
      if (output.existsSync()) output.deleteSync(recursive: true);

      final sockets = <WebSocket>[];
      final schemaServed = Completer<void>();
      server.listen((request) async {
        if (WebSocketTransformer.isUpgradeRequest(request)) {
          await acceptDecodeSocket(request, sockets);
          return;
        }
        expect(request.uri.queryParameters.containsKey(r'$schema'), isTrue);
        expect(request.uri.queryParameters.containsKey('key'), isFalse);
        expect(request.headers.value('X-Introspection-Key'), 'test');
        request.response.headers.contentType = ContentType.json;
        request.response.write(jsonEncode(_schema));
        await request.response.close();
        schemaServed.complete();
      });

      try {
        await generate(
          endpoint: 'http://${server.address.host}:${server.port}/luxo',
          key: 'test',
          outDir: output.path,
        );
        await schemaServed.future;
        if (Platform.environment['LUXO_JSON_PROJECTION_BASELINE'] == '1') {
          for (final name in ['types.dart', 'client.dart']) {
            File('.tmp/dart-json-projection-before/$name')
                .copySync('${output.path}/$name');
          }
        }

        final types = File('${output.path}/types.dart').readAsStringSync();
        final schema = File('${output.path}/schema.dart').readAsStringSync();
        final client = File('${output.path}/client.dart').readAsStringSync();
        if (Platform.environment['LUXO_CAPTURE_JSON_PROJECTION'] == '1') {
          _captureProjectionBaseline(output);
        }

        expect(client, contains('echoInt({required LuxoInt64 value})'));
        expect(client, contains('echoDuration({required LuxoInt64 value})'));
        expect(client, contains('echoEnum({required TaskStatus value})'));
        expect(types, contains('sealed class TaskStatus'));
        expect(
          types,
          contains('static List<String> \$rawList(List<TaskStatus> values)'),
        );
        expect(client, contains('TaskStatus.\$nullableRawList(nullableList)'));
        expect(types, contains('enum TaskStatusKnown implements TaskStatus'));
        expect(
          types,
          contains('final class TaskStatusUnknown extends TaskStatus'),
        );

        expect(types, contains("import 'dart:typed_data';"));
        expect(types, contains('final Selected<Uint8List> data;'));
        expect(types, contains('final Selected<Object?> metadata;'));
        expect(types, contains('final Selected<List<Object?>> jsons;'));
        expect(client, contains('Future<List<Object?>> arrayJSON()'));
        expect(types, contains('final Selected<List<Uint8List>> chunks;'));
        expect(types, contains('dec.readBool() ? dec.readIntArray() : null'));
        expect(types, contains('List<Uint8List?>? _nullableIntegers'));
        expect(types, contains('_nullableIntegers = dec.readColumnBytesPtr()'));
        expect(
          types,
          contains(
            '_nullableIntegers[i] == null ? null : LuxoDecoder(_nullableIntegers[i]!).readIntArrayChecked()',
          ),
        );
        expect(types, contains('final Selected<Child> child;'));
        expect(types, contains('class ChildInput'));
        expect(types, contains('final ChildInput child;'));
        expect(types, contains('Child.fromJson'));
        expect(types, isNot(contains('Uint8List(0)')));
        expect(types, contains('dec.readBytes()'));
        expect(types, contains('decodeLuxoJSONBytes(dec.readBytes())'));
        expect(types, isNot(contains('Uint8List.fromList(utf8.encode(')));
        expect(
          types,
          contains('encoder.writeBytes(utf8.encode(encodeLuxoJSON('),
        );
        expect(
          schema,
          contains("ParamSchema(1, 'chunks', 'Bytes', true, false)"),
        );
        expect(
          schema,
          contains(
            "'child': SelectionFieldSchema(4, 'Child', 'Model', false, false)",
          ),
        );
        expect(
          schema,
          contains(
            "ParamSchema(3, 'input', 'Model', false, false, 'CreateInput')",
          ),
        );
        expect(
          schema,
          contains(
            "'name': SelectionFieldSchema(1, null, 'String', false, false)",
          ),
        );
        expect(
          schema,
          contains("luxoSelectionTypes['Payload']!, luxoSelectionTypes"),
        );
        expect(client, contains('required List<Uint8List> chunks'));
        expect(client, contains('required Object metadata'));
        expect(client, contains('required CreateInput input'));
        expect(
          types,
          contains('class CreateInput implements LuxoBinaryEncodable'),
        );
        expect(types, contains('void writeLuxo(LuxoEncoder encoder)'));
        expect(client, contains('required String? note'));
        expect(
          client,
          contains(
            'LuxoOptional<String> caption = const LuxoOptional<String>.absent()',
          ),
        );
        expect(
          client,
          contains("if (caption.isPresent) 'caption': caption.value"),
        );
        expect(client, isNot(contains('..nextField()')));
        expect(
          types,
          contains('List<Payload> decodeColumnarPayload(Uint8List data)'),
        );
        expect(
          types,
          contains('Page<Payload> decodePaginatedPayload(Uint8List data)'),
        );
        expect(client, contains('decodeColumnarPayload(d)'));
        expect(client, contains('decodePaginatedPayload(d)'));
        expect(client, contains('List<LuxoFilter>? filters'));
        expect(client, contains("if (filters != null) r'\$filters': filters"));
        expect(
          client,
          contains(
            'Future<Page<Payload>> searchPayloads({LuxoInt64? page, LuxoInt64? pageSize, String? select, PreparedSelection? \$selectionPlan, List<LuxoFilter>? filters, List<LuxoSorter>? sorters})',
          ),
        );
        expect(
          client,
          contains(
            'Future<Payload> createSnapshot({String? select, PreparedSelection? \$selectionPlan})',
          ),
        );
        expect(
          client,
          isNot(contains('createSnapshot(Map<String, dynamic> input)')),
        );
        expect(client, contains('Future<Payload> upload({'));
        expect(client, contains('String? select'));
        expect(
          client,
          contains(
            'Future<void Function()> subscribeWatchPayload({required LuxoInt64 projectId, String? select, PreparedSelection? \$selectionPlan, required void Function(Payload) onData, void Function(LuxoError)? onError, void Function()? onDone})',
          ),
        );
        expect(client, contains("transport.subscribe('watchPayload'"));
        expect(
          client,
          contains("const {}, onData, request: _watchPayloadRequest("),
        );
        expect(
          client,
          contains('class _uploadRequest extends GeneratedRequest'),
        );
        expect(client, contains('encoder.writeDelimited(value.writeLuxo)'));
        expect(client, isNot(contains('(d) => onData(')));
        expect(client, contains('onError: onError, onDone: onDone'));
        expect(
          client,
          contains(
            'decoder: (d) => d is Uint8List ? decodePayload(LuxoDecoder(d)) : Payload.fromJson(d as Map<String, dynamic>)',
          ),
        );

        File('${output.path}/int64_consumer_test.dart').writeAsStringSync(
          File('test/fixtures/int64_consumer.dart.txt').readAsStringSync(),
        );
        File('${output.path}/response_consumer.dart').writeAsStringSync(
          File('test/fixtures/response_consumer.dart.txt').readAsStringSync(),
        );
        File('${output.path}/nullable_root_consumer.dart').writeAsStringSync(
          File('test/fixtures/nullable_root_consumer.dart.txt')
              .readAsStringSync(),
        );
        File('${output.path}/json_kind_consumer.dart').writeAsStringSync(
          File('test/fixtures/json_kind_consumer.dart.txt').readAsStringSync(),
        );
        File('${output.path}/json_projection_consumer.dart').writeAsStringSync(
          File('test/fixtures/json_projection_consumer.dart.txt')
              .readAsStringSync(),
        );
        File('${output.path}/stream_isolation_consumer.dart').writeAsStringSync(
          File('test/fixtures/stream_isolation_consumer.dart.txt')
              .readAsStringSync()
              .replaceAll(
                '__STREAM_ENDPOINT__',
                'ws://${server.address.host}:${server.port}',
              ),
        );
        final analysis = await _runDart([
          'analyze',
          '--fatal-infos',
          output.path,
        ]);
        expect(
          analysis.exitCode,
          0,
          reason: '${analysis.stdout}\n${analysis.stderr}',
        );
        final consumer = await _runDart([
          'test',
          '-p',
          'vm,node',
          '-c',
          'kernel',
          '-c',
          'dart2js',
          '-c',
          'dart2wasm',
          '-j',
          '1',
          '${output.path}/int64_consumer_test.dart',
          '--reporter',
          'expanded',
        ]);
        expect(
          consumer.exitCode,
          0,
          reason: '${consumer.stdout}\n${consumer.stderr}',
        );
        final boundaryEvidence = consumer.stdout
            .toString()
            .split('\n')
            .where(
              (line) =>
                  line.contains(
                    'generated Binary JSON readers reject document BOMs',
                  ) ||
                  line.contains(
                    'generated JSON fields preserve literal null',
                  ) ||
                  line.contains('generated JSON fields reject non-finite') ||
                  line.contains('generated JSON roots preserve literal null') ||
                  line.contains(
                    'generated WS responses preserve JSON128 and isolate model65',
                  ) ||
                  line.contains(
                    'generated HTTP responses keep JSON128 independent from model64',
                  ) ||
                  line.contains(
                    'generated HTTP JSON roots distinguish literal null',
                  ),
            )
            .toList();
        for (final compiler in ['kernel', 'dart2js', 'dart2wasm']) {
          for (final boundary in [
            'document BOMs',
            'literal null',
            'non-finite',
            'JSON128 independent from model64',
            'WS responses preserve JSON128 and isolate model65',
          ]) {
            expect(
              boundaryEvidence.any(
                (line) =>
                    line.toLowerCase().contains(compiler) &&
                    line.contains(boundary),
              ),
              isTrue,
              reason:
                  'Missing generated $boundary execution for $compiler: ${consumer.stdout}',
            );
          }
        }
        print(boundaryEvidence.join('\n'));
        if (Platform.environment['LUXO_BENCH_JSON_PROJECTION'] == '1') {
          await _runProjectionBenchmarks(output);
        }
        if (Platform.environment['LUXO_BENCH_ENUMS'] == '1') {
          await _runEnumBenchmarks(output);
        }
      } finally {
        for (final socket in sockets) await socket.close();
        await server.close(force: true);
        if (output.existsSync()) output.deleteSync(recursive: true);
      }
    },
    timeout: Timeout(
      Duration(
        minutes:
            Platform.environment['LUXO_BENCH_JSON_PROJECTION'] == '1' ||
                Platform.environment['LUXO_BENCH_ENUMS'] == '1'
            ? 10
            : 2,
      ),
    ),
  );
}

Map<String, Object?> _numberedSchema(
  String target,
  int id, {
  bool duplicate = false,
}) {
  final fields = [
    {'id': id, 'name': 'value', 'type': 'Int'},
    if (duplicate) {'id': id, 'name': 'other', 'type': 'Int'},
  ];
  if (target == 'apis' || target == 'params') {
    return {
      'apis': {
        'read': {
          'id': target == 'apis' ? id : 1,
          'name': 'read',
          'module': 'test',
          'returnType': 'Int',
          if (target == 'params') 'params': fields,
        },
        if (target == 'apis' && duplicate)
          'other': {
            'id': id,
            'name': 'other',
            'module': 'test',
            'returnType': 'Int',
          },
      },
    };
  }
  return {
    target: {
      'Payload': {'name': 'Payload', 'fields': fields},
    },
  };
}

Map<String, Object?> _enumReferenceSchema(
  String target,
  String? name,
  bool list, {
  String type = 'Enum',
}) {
  final field = {
    'id': 1,
    'name': 'state',
    'type': type,
    if (name != null) 'typeName': name,
    'isList': list,
    'nullable': list,
  };
  return target == 'params'
      ? {
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Int',
              'params': [field],
            },
          },
        }
      : {
          target == 'unused' ? 'types' : target: {
            'Payload': {
              'name': 'Payload',
              if (target == 'unused') 'usage': 'unused',
              'fields': [field],
            },
          },
        };
}

Future<void> _checkGeneratedConsumer(
  Map<String, Object?> schema,
  String consumer, {
  bool crossPlatform = false,
  String? compiler,
  bool automaticSelection = false,
  String expectedSelection = "'café,状态'",
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final root = Directory('.tmp')..createSync(recursive: true);
  final output = root.createTempSync('member-schema-');
  final sockets = <WebSocket>[];
  var modelFrames = 0;
  server.listen((request) async {
    if (WebSocketTransformer.isUpgradeRequest(request)) {
      await acceptModelDepthSocket(request, sockets, () => modelFrames++);
      return;
    }
    request.response.headers.contentType = ContentType.json;
    request.response.write(jsonEncode(schema));
    await request.response.close();
  });
  try {
    await generate(
      endpoint: 'http://${server.address.host}:${server.port}/luxo',
      key: 'test',
      outDir: output.path,
    );
    if (automaticSelection) {
      final fixture = await SelectFixture.create({
        'lib/main.dart': consumer,
        for (final name in [
          'types.dart',
          'schema.dart',
          'client.dart',
          'queries.dart',
        ])
          'lib/$name': File('${output.path}/$name').readAsStringSync(),
      });
      try {
        final unit = await fixture.unit('lib/main.dart');
        final sdk = await fixture.unit('lib/client.dart');
        consumer = rewriteSelections(
          unit.unit,
          consumer,
          clientLibrary: sdk.libraryElement.uri,
          library: unit.libraryElement,
        );
        expect(consumer, contains(expectedSelection));
      } finally {
        await fixture.close();
      }
    }
    final entry = File('${output.path}/consumer_test.dart')
      ..writeAsStringSync(
        consumer.replaceAll(
          '__MODEL_DEPTH_WS__',
          'ws://${server.address.host}:${server.port}/model-depth',
        ),
      );
    if (crossPlatform) {
      final analysis = await _runDart([
        'analyze',
        '--fatal-infos',
        output.path,
      ]);
      expect(
        analysis.exitCode,
        0,
        reason: '${analysis.stdout}\n${analysis.stderr}',
      );
    }
    final coverageRoot = Platform.environment['LUXO_SDK_COVERAGE'];
    final coverage = coverageRoot != null && !crossPlatform
        ? Directory(coverageRoot).createTempSync('dart-generated-')
        : null;
    if (coverage != null) {
      for (final name in [
        'types.dart',
        'schema.dart',
        'client.dart',
        'queries.dart',
        'consumer_test.dart',
      ]) {
        File('${output.path}/$name').copySync('${coverage.path}/$name');
      }
    }
    final result = await _runDart([
      'test',
      if (coverage != null) ...[
        '--coverage=${coverage.path}/raw',
        '--branch-coverage',
      ],
      if (crossPlatform) ...[
        '-p',
        compiler == null
            ? 'vm,node'
            : compiler == 'kernel'
            ? 'vm'
            : 'node',
        for (final target
            in compiler == null
                ? ['kernel', 'dart2js', 'dart2wasm']
                : [compiler]) ...['-c', target],
        '-j',
        '1',
      ],
      entry.path,
      '--reporter',
      'expanded',
    ]);
    if (crossPlatform) print(result.stdout);
    expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
    if (consumer.contains('__MODEL_DEPTH_WS__')) {
      expect(modelFrames, compiler == null ? 36 : 12);
    }
  } finally {
    for (final socket in sockets) await socket.close();
    await server.close(force: true);
    output.deleteSync(recursive: true);
  }
}

Future<void> _rejectSchema(
  Map<String, Object?> schema,
  String message, {
  bool existing = false,
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final root = Directory('.tmp')..createSync(recursive: true);
  final output = root.createTempSync('enum-schema-');
  final types = File('${output.path}/types.dart');
  if (existing) types.writeAsStringSync('// Existing generated client.\n');
  server.listen((request) async {
    request.response.headers.contentType = ContentType.json;
    request.response.write(jsonEncode(schema));
    await request.response.close();
  });
  try {
    await expectLater(
      generate(
        endpoint: 'http://${server.address.host}:${server.port}/luxo',
        key: 'test',
        outDir: output.path,
      ),
      throwsA(
        isA<FormatException>().having(
          (error) => error.message,
          'message',
          contains(message),
        ),
      ),
    );
    if (existing) {
      expect(types.readAsStringSync(), '// Existing generated client.\n');
      expect(output.listSync(), hasLength(1));
    } else {
      expect(output.listSync(), isEmpty);
    }
  } finally {
    await server.close(force: true);
    output.deleteSync(recursive: true);
  }
}

(String, String) _prepareProjectionBenchmark(Directory output) {
  for (final name in ['types', 'client']) {
    final source = File('.tmp/dart-json-projection-before/$name.dart')
        .readAsStringSync();
    if (source.contains(r'$fromJsonItem'))
      throw StateError('Expected the pre-projection snapshot');
    File('${output.path}/${name}_before.dart').writeAsStringSync(
      source.replaceAll("'types.dart'", "'types_before.dart'"),
    );
  }
  final script = '${output.path}/projection_benchmark.dart';
  File(script).writeAsStringSync(
    File('benchmark/json_projection_benchmark.dart.txt').readAsStringSync(),
  );
  final test = '${output.path}/projection_benchmark_test.dart';
  File(test).writeAsStringSync(
    "import 'package:test/test.dart';\nimport 'projection_benchmark.dart' as bench;\nvoid main() { test('projection benchmark', bench.main); }\n",
  );
  return (script, test);
}

Future<void> _runProjectionBenchmarks(Directory output) async {
  final (script, test) = _prepareProjectionBenchmark(output);
  await _runGeneratedBenchmarks(output, 'projection', script, test);
}

Future<void> _runEnumBenchmarks(Directory output) async {
  final script = '${output.path}/enum_benchmark.dart';
  File(script).writeAsStringSync(
    File('benchmark/enum_benchmark.dart.txt').readAsStringSync(),
  );
  final test = '${output.path}/enum_benchmark_test.dart';
  File(test).writeAsStringSync(
    "import 'package:test/test.dart';\nimport 'enum_benchmark.dart' as bench;\nvoid main() { test('enum benchmark', bench.main); }\n",
  );
  await _runGeneratedBenchmarks(output, 'enum', script, test);
}

Future<void> _runGeneratedBenchmarks(
  Directory output,
  String name,
  String script,
  String test,
) async {
  final analysis = await _runDart(['analyze', '--fatal-infos', output.path]);
  expect(
    analysis.exitCode,
    0,
    reason: '${analysis.stdout}\n${analysis.stderr}',
  );
  final compile = await _runDart([
    'compile',
    'exe',
    script,
    '-o',
    '${output.path}/$name-bench',
  ]);
  expect(compile.exitCode, 0, reason: '${compile.stdout}\n${compile.stderr}');
  for (final platform in ['aot', 'dart2js', 'dart2wasm']) {
    for (var round = 1; round <= 3; round++) {
      final result = platform == 'aot'
          ? await _runProcess(
              '${output.path}/$name-bench',
              [],
              timeout: const Duration(minutes: 3),
            )
          : await _runProcess(Platform.resolvedExecutable, [
              'test',
              '-p',
              'node',
              '-c',
              platform,
              test,
              '--reporter',
              'expanded',
            ], timeout: const Duration(minutes: 3));
      expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
      print('$name benchmark $platform round $round\n${result.stdout}');
    }
  }
}

void _captureProjectionBaseline(Directory output) {
  final baseline = Directory('.tmp/dart-json-projection-before')
    ..createSync(recursive: true);
  for (final name in ['types.dart', 'client.dart']) {
    final source = File('${output.path}/$name').readAsStringSync();
    final target = File('${baseline.path}/$name');
    if (target.existsSync() && target.readAsStringSync() != source) {
      throw StateError(
        'Projection baseline already exists with different content',
      );
    }
    target.writeAsStringSync(source);
  }
}

Future<ProcessResult> _runDart(List<String> arguments) async {
  return _runProcess(Platform.resolvedExecutable, arguments);
}

Future<ProcessResult> _runProcess(
  String executable,
  List<String> arguments, {
  Duration timeout = const Duration(seconds: 60),
}) async {
  final process = await Process.start(executable, arguments);
  final stdout = process.stdout.transform(utf8.decoder).join();
  final stderr = process.stderr.transform(utf8.decoder).join();
  var expired = false;
  Timer? force;
  final timer = Timer(timeout, () {
    expired = true;
    process.kill(ProcessSignal.sigterm);
    force = Timer(
      const Duration(seconds: 2),
      () => process.kill(ProcessSignal.sigkill),
    );
  });
  try {
    final result = ProcessResult(
      process.pid,
      await process.exitCode,
      await stdout,
      await stderr,
    );
    if (expired) {
      throw TimeoutException(
        'Process $executable exceeded its budget.\n${result.stdout}\n${result.stderr}',
        timeout,
      );
    }
    return result;
  } finally {
    timer.cancel();
    force?.cancel();
  }
}

const _scalarTypes = [
  'Int',
  'Duration',
  'Float',
  'String',
  'Boolean',
  'DateTime',
  'UUID',
  'Bytes',
  'Decimal',
  'Enum',
  'JSON',
];

final _schema = <String, Object>{
  'types': {
    'Projection': {
      'name': 'Projection',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'label', 'type': 'String', 'nullable': true},
        {
          'id': 2,
          'name': 'child',
          'type': 'Model',
          'typeName': 'Projection',
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'children',
          'type': 'Model',
          'typeName': 'Projection',
          'isList': true,
        },
        {
          'id': 4,
          'name': 'models',
          'type': 'Model',
          'typeName': 'Payload',
          'isList': true,
        },
      ],
    },
  },
  'enums': {
    'TaskStatus': {
      'name': 'TaskStatus',
      'values': ['OPEN', 'DONE'],
    },
    'OtherStatus': {
      'name': 'OtherStatus',
      'values': ['class', 'values', 'rawValue', '_hidden', 'OtherStatusKnown'],
    },
  },
  'models': {
    'EnumPayload': {
      'name': 'EnumPayload',
      'usage': 'inputOutput',
      'fields': [
        {'id': 1, 'name': 'status', 'type': 'Enum', 'typeName': 'TaskStatus'},
        {
          'id': 2,
          'name': 'optional',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'statuses',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
        },
        {
          'id': 4,
          'name': 'optionalList',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
          'nullable': true,
        },
      ],
    },
    'OptionalArrays': {
      'name': 'OptionalArrays',
      'usage': 'output',
      'fields': [
        {
          'id': 1,
          'name': 'nullableIntegers',
          'type': 'Int',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 2,
          'name': 'texts',
          'type': 'String',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'flags',
          'type': 'Boolean',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 4,
          'name': 'child',
          'type': 'Model',
          'typeName': 'ScalarArrays',
          'nullable': true,
        },
      ],
    },
    'ScalarArrays': {
      'name': 'ScalarArrays',
      'usage': 'inputOutput',
      'fields': [
        {'id': 1, 'name': 'integers', 'type': 'Int', 'isList': true},
        {'id': 2, 'name': 'durations', 'type': 'Duration', 'isList': true},
        {'id': 3, 'name': 'floats', 'type': 'Float', 'isList': true},
        {'id': 4, 'name': 'strings', 'type': 'String', 'isList': true},
        {'id': 5, 'name': 'booleans', 'type': 'Boolean', 'isList': true},
        {'id': 6, 'name': 'dates', 'type': 'DateTime', 'isList': true},
        {'id': 7, 'name': 'uuids', 'type': 'UUID', 'isList': true},
        {'id': 8, 'name': 'blobs', 'type': 'Bytes', 'isList': true},
        {'id': 9, 'name': 'decimals', 'type': 'Decimal', 'isList': true},
        {
          'id': 10,
          'name': 'enums',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
        },
        {'id': 11, 'name': 'jsons', 'type': 'JSON', 'isList': true},
      ],
    },
    'Numbers': {
      'name': 'Numbers',
      'usage': 'inputOutput',
      'fields': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
        {'id': 2, 'name': 'duration', 'type': 'Duration'},
        {'id': 3, 'name': 'values', 'type': 'Int', 'isList': true},
        {'id': 4, 'name': 'optional', 'type': 'Int', 'nullable': true},
        {'id': 5, 'name': 'ratios', 'type': 'Float', 'isList': true},
        {'id': 6, 'name': 'metadata', 'type': 'JSON'},
        {'id': 7, 'name': 'createdAt', 'type': 'DateTime'},
      ],
    },
    'Child': {
      'name': 'Child',
      'usage': 'unused',
      'fields': [
        {'id': 1, 'name': 'name', 'type': 'String'},
      ],
    },
    'Payload': {
      'name': 'Payload',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'data', 'type': 'Bytes'},
        {'id': 2, 'name': 'metadata', 'type': 'JSON'},
        {'id': 3, 'name': 'chunks', 'type': 'Bytes', 'isList': true},
        {
          'id': 4,
          'name': 'child',
          'type': 'Model',
          'typeName': 'Child',
          'relation': true,
        },
      ],
    },
    'CreateInput': {
      'name': 'CreateInput',
      'usage': 'input',
      'fields': [
        {'id': 1, 'name': 'child', 'type': 'Model', 'typeName': 'Child'},
      ],
    },
  },
  'apis': {
    for (final (index, type) in [
      ..._scalarTypes,
      'Payload',
      'Projection',
    ].indexed)
      for (final array in [false, true])
        for (final stream in [false, true])
          'optional$type${array ? 'Array' : ''}${stream ? 'Stream' : ''}': {
            'id': 1000 + index * 4 + (array ? 2 : 0) + (stream ? 1 : 0),
            'name':
                'optional$type${array ? 'Array' : ''}${stream ? 'Stream' : ''}',
            'module': 'nullable',
            'returnType': type == 'Enum' ? 'TaskStatus' : type,
            'returnList': array,
            'returnNullable': true,
            'stream': stream,
          },
    'projection': {
      'id': 17,
      'name': 'projection',
      'module': 'file',
      'returnType': 'Projection',
    },
    'watchPayloadList': {
      'id': 18,
      'name': 'watchPayloadList',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
      'stream': true,
    },
    'namedEnum': {
      'id': 300,
      'name': 'namedEnum',
      'module': 'scalar',
      'returnType': 'TaskStatus',
    },
    'enumOptions': {
      'id': 304,
      'name': 'enumOptions',
      'module': 'scalar',
      'returnType': 'TaskStatus',
      'params': [
        {
          'id': 1,
          'name': 'nullable',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'nullable': true,
        },
        {
          'id': 2,
          'name': 'nullableList',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'defaulted',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'hasDefault': true,
        },
        {
          'id': 4,
          'name': 'nullableDefault',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'nullable': true,
          'hasDefault': true,
        },
      ],
    },
    'namedEnums': {
      'id': 301,
      'name': 'namedEnums',
      'module': 'scalar',
      'returnType': 'TaskStatus',
      'returnList': true,
    },
    'explicitInteger': {
      'id': 302,
      'name': 'explicitInteger',
      'module': 'scalar',
      'returnType': 'Int',
    },
    'explicitIntegers': {
      'id': 303,
      'name': 'explicitIntegers',
      'module': 'scalar',
      'returnType': 'Int',
      'returnList': true,
    },
    for (final (index, type) in [..._scalarTypes, 'TaskStatus'].indexed)
      for (final array in [false, true])
        'watch$type${array ? 'Array' : ''}': {
          'id': 400 + index * 2 + (array ? 1 : 0),
          'name': 'watch$type${array ? 'Array' : ''}',
          'module': 'scalar',
          'returnType': type == 'Enum' ? 'TaskStatus' : type,
          'returnList': array,
          'stream': true,
        },
    for (var index = 0; index < _scalarTypes.length; index++)
      'echo${_scalarTypes[index]}': {
        'id': 200 + index,
        'name': 'echo${_scalarTypes[index]}',
        'module': 'scalar',
        'returnType': _scalarTypes[index] == 'Enum'
            ? 'TaskStatus'
            : _scalarTypes[index],
        'params': [
          {
            'id': 1,
            'name': 'value',
            'type': _scalarTypes[index],
            'typeName': _scalarTypes[index] == 'Enum'
                ? 'TaskStatus'
                : _scalarTypes[index],
          },
        ],
      },
    for (final (index, type) in _scalarTypes.indexed)
      'scalar$type': {
        'id': 100 + index * 2,
        'name': 'scalar$type',
        'module': 'scalar',
        'returnType': type == 'Enum' ? 'TaskStatus' : type,
      },
    for (final (index, type) in _scalarTypes.indexed)
      'array$type': {
        'id': 101 + index * 2,
        'name': 'array$type',
        'module': 'scalar',
        'returnType': type == 'Enum' ? 'TaskStatus' : type,
        'returnList': true,
      },
    'echoInteger': {
      'id': 13,
      'name': 'echoInteger',
      'module': 'number',
      'returnType': 'Int',
      'params': [
        {'id': 1, 'name': 'value', 'type': 'Int'},
      ],
    },
    'listDurations': {
      'id': 14,
      'name': 'listDurations',
      'module': 'number',
      'returnType': 'Duration',
      'returnList': true,
    },
    'listFloats': {
      'id': 15,
      'name': 'listFloats',
      'module': 'number',
      'returnType': 'Float',
      'returnList': true,
    },
    'inspectNumbers': {
      'id': 16,
      'name': 'inspectNumbers',
      'module': 'number',
      'returnType': 'Numbers',
      'params': [
        {'id': 1, 'name': 'input', 'type': 'Model', 'typeName': 'Numbers'},
      ],
    },
    'upload': {
      'id': 7,
      'name': 'upload',
      'module': 'file',
      'returnType': 'Payload',
      'params': [
        {'id': 1, 'name': 'chunks', 'type': 'Bytes', 'isList': true},
        {'id': 2, 'name': 'metadata', 'type': 'JSON'},
        {'id': 3, 'name': 'input', 'type': 'Model', 'typeName': 'CreateInput'},
        {'id': 4, 'name': 'note', 'type': 'String', 'nullable': true},
        {
          'id': 5,
          'name': 'caption',
          'type': 'String',
          'nullable': true,
          'hasDefault': true,
        },
      ],
    },
    'listPayloads': {
      'id': 8,
      'name': 'listPayloads',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
    },
    'listPayloadPage': {
      'id': 9,
      'name': 'listPayloadPage',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
      'paginated': true,
      'params': [
        {'id': 7, 'name': 'page', 'type': 'Int', 'hasDefault': true},
        {'id': 9, 'name': 'pageSize', 'type': 'Int', 'hasDefault': true},
      ],
    },
    'watchPayload': {
      'id': 10,
      'name': 'watchPayload',
      'module': 'file',
      'returnType': 'Payload',
      'stream': true,
      'params': [
        {'id': 1, 'name': 'projectId', 'type': 'Int'},
      ],
    },
    'searchPayloads': {
      'id': 11,
      'name': 'searchPayloads',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
      'paginated': true,
      'params': [
        {'id': 7, 'name': 'page', 'type': 'Int', 'hasDefault': true},
        {'id': 9, 'name': 'pageSize', 'type': 'Int', 'hasDefault': true},
      ],
    },
    'createSnapshot': {
      'id': 12,
      'name': 'createSnapshot',
      'module': 'file',
      'returnType': 'Payload',
    },
  },
};
