export function decodeBase64Url(value: string): ArrayBuffer {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes.buffer;
}

export function encodeBase64Url(value: ArrayBufferLike): string {
  const bytes = new Uint8Array(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

export function toCredentialCreationOptions(raw: unknown): PublicKeyCredentialCreationOptions {
  const wrapper = (raw ?? {}) as {
    publicKey?: Record<string, unknown>;
  };
  const source = wrapper.publicKey ?? (raw as Record<string, unknown>) ?? {};
  const user = (source.user ?? {}) as Record<string, unknown>;
  const excludeCredentials = Array.isArray(source.excludeCredentials)
    ? source.excludeCredentials.map((item) => {
        const descriptor = item as Record<string, unknown>;
        return {
          ...descriptor,
          id: decodeBase64Url(String(descriptor.id ?? "")),
        } as PublicKeyCredentialDescriptor;
      })
    : undefined;
  return {
    ...source,
    challenge: decodeBase64Url(String(source.challenge ?? "")),
    user: {
      ...user,
      id: decodeBase64Url(String(user.id ?? "")),
    },
    excludeCredentials,
  } as PublicKeyCredentialCreationOptions;
}

export function serializeCreationCredential(
  credential: PublicKeyCredential,
  response: AuthenticatorAttestationResponse,
) {
  return {
    id: credential.id,
    type: credential.type,
    rawId: encodeBase64Url(credential.rawId),
    response: {
      clientDataJSON: encodeBase64Url(response.clientDataJSON),
      attestationObject: encodeBase64Url(response.attestationObject),
      transports: response.getTransports?.() ?? [],
    },
  };
}
