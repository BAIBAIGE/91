function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(
    ""
  );
}

/**
 * Hash a browser Blob incrementally with bounded memory. Web Crypto only
 * exposes a one-shot digest and would require materializing a multi-gigabyte
 * backup in one ArrayBuffer, so backup hashing uses the streaming hasher on
 * both HTTP and HTTPS origins.
 */
export async function sha256Blob(
  blob: Blob,
  signal?: AbortSignal
): Promise<string> {
  const { sha256 } = await import("@noble/hashes/sha2.js");
  const hasher = sha256.create();
  const chunkSize = 8 * 1024 * 1024;
  for (let offset = 0; offset < blob.size; offset += chunkSize) {
    if (signal?.aborted) {
      throw signal.reason instanceof Error
        ? signal.reason
        : new DOMException("Hashing aborted", "AbortError");
    }
    const chunk = await blob
      .slice(offset, Math.min(blob.size, offset + chunkSize))
      .arrayBuffer();
    hasher.update(new Uint8Array(chunk));
  }
  if (signal?.aborted) {
    throw signal.reason instanceof Error
      ? signal.reason
      : new DOMException("Hashing aborted", "AbortError");
  }
  return bytesToHex(hasher.digest());
}
