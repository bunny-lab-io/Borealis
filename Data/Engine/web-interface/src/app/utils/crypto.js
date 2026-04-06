export async function sha512(text) {
  try {
    if (window.crypto && window.crypto.subtle && window.isSecureContext) {
      const encoder = new TextEncoder();
      const data = encoder.encode(text || "");
      const hashBuffer = await window.crypto.subtle.digest("SHA-512", data);
      const hashArray = Array.from(new Uint8Array(hashBuffer));
      return hashArray.map((item) => item.toString(16).padStart(2, "0")).join("");
    }
  } catch {
    /* fall through to plaintext fallback */
  }
  return null;
}
