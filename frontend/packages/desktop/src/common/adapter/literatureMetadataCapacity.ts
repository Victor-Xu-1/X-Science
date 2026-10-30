/** Receiver bounds also cover durable commands from a newer capable writer. */
export const LITERATURE_METADATA_SNAPSHOT_BYTES = 80 * 1024;
export const LITERATURE_METADATA_PATCH_BYTES = 96 * 1024;

/** Go's canonical metadata serializer additionally escapes HTML and separators. */
export function literatureMetadataEncodedBytes(json: string): number {
  const encoded = json.replace(
    /[<>&\u2028\u2029]/g,
    (char) => '\\u' + char.charCodeAt(0).toString(16).padStart(4, '0')
  );
  return new TextEncoder().encode(encoded).length;
}
