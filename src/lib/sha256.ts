type DigestProvider = Pick<SubtleCrypto, "digest">;

function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(
    ""
  );
}

/**
 * Hash a browser Blob without requiring a secure context.
 *
 * HTTPS and localhost use the browser's native Web Crypto implementation. On
 * plain HTTP origins, where browsers do not expose SubtleCrypto, load the
 * JavaScript implementation only when it is needed.
 */
export async function sha256Blob(
  blob: Blob,
  subtle: DigestProvider | null = globalThis.crypto?.subtle ?? null
): Promise<string> {
  const buffer = await blob.arrayBuffer();

  if (subtle) {
    const digest = await subtle.digest("SHA-256", buffer);
    return bytesToHex(new Uint8Array(digest));
  }

  const { sha256 } = await import("@noble/hashes/sha2.js");
  return bytesToHex(sha256(new Uint8Array(buffer)));
}
