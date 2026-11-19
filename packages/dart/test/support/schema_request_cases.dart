import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/transport.dart';

final class ReplacingSchemaRequest extends GeneratedRequest {
  @override
  final int apiID;
  final void Function() replace;

  ReplacingSchemaRequest(this.apiID, this.replace);

  @override
  void writeParameters(LuxoEncoder encoder) {}

  @override
  Map<String, dynamic> toJSONParameters() {
    replace();
    return {'value': 1.5};
  }
}
