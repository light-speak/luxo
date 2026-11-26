final _leadingBom = RegExp(r'(^|/)%EF%BB%BF');
final _uriDropsLeadingBom = Uri.parse('%EF%BB%BFx').pathSegments.single == 'x';

/// Preserve native filenames when the SDK decodes individual URI components.
Uri nativeFileUri(Uri uri) {
  final path = uri.path;
  if (!path.contains('%EF%BB%BF') || !_uriDropsLeadingBom) return uri;
  // The affected SDK discards the first U+FEFF in each decoded component.
  final exact = path.replaceAllMapped(_leadingBom, (match) {
    return '${match[0]}%EF%BB%BF';
  });
  return exact == path ? uri : uri.replace(path: exact);
}
