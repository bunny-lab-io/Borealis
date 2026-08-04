const DEFAULT_MAX_LENGTH = 4096;
const IDENTIFIER_MAX_LENGTH = 256;
const PATH_MAX_LENGTH = 4096;
const SECRET_MAX_LENGTH = 16 * 1024 * 1024;

const FIELD_CLASS = {
  PLAIN_SINGLE_LINE: "plain_single_line",
  PLAIN_MULTILINE: "plain_multiline",
  IDENTIFIER: "identifier",
  SLUG: "slug",
  HOST: "host",
  URL: "url",
  PATH: "path",
  REGISTRY: "registry",
  REGEX: "regex",
  SECRET: "secret",
  CODE: "code",
};

const IDENTIFIER_RE = /^[A-Za-z0-9][A-Za-z0-9._:@/-]*$/;
const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;
const IPV4_RE = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}(\/([0-9]|[1-2]\d|3[0-2]))?$/;
const HOST_RE = /^(?=.{1,253}$)([A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.([A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?))*$/;
const CONTROL_RE = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/;
const UNSAFE_MARKUP_RE = /<\s*\/?\s*(script|iframe|object|embed|img|svg|math|link|style|meta|base|form|input|button|textarea|select|option|video|audio|source|body|html)\b|on[a-z]+\s*=|javascript\s*:/i;

export { FIELD_CLASS };

export function sanitizeSingleLineInput(value) {
  return String(value ?? "")
    .replace(/[\u0000-\u001f\u007f]+/g, " ")
    .trim()
    .replace(/\s+/g, " ");
}

export function sanitizeMultilineInput(value) {
  return String(value ?? "")
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n")
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]+/g, " ")
    .trim();
}

export function sanitizeNotificationMessage(value) {
  let text = sanitizeMultilineInput(value)
    .replaceAll("<B>", "<b>")
    .replaceAll("</B>", "</b>")
    .replaceAll("<b>", "\u0000BOLD_OPEN\u0000")
    .replaceAll("</b>", "\u0000BOLD_CLOSE\u0000");
  text = text.replaceAll("<", "").replaceAll(">", "");
  return text
    .replaceAll("\u0000BOLD_OPEN\u0000", "<b>")
    .replaceAll("\u0000BOLD_CLOSE\u0000", "</b>")
    .slice(0, DEFAULT_MAX_LENGTH);
}

export function classifyInputField(field) {
  const key = String(field || "").toLowerCase().trim().replace(/^\[|\]$/g, "");
  if (
    key.includes("password") ||
    key.includes("cipher") ||
    key.includes("secret") ||
    key.includes("token") ||
    key.includes("pem") ||
    key.includes("private_key") ||
    key.includes("backup")
  ) return FIELD_CLASS.SECRET;
  if (
    key.includes("script") ||
    key.includes("code") ||
    key.includes("content") ||
    key.includes("stdout") ||
    key.includes("stderr") ||
    key.includes("command") ||
    key.includes("argument") ||
    key.includes("json") ||
    key.includes("payload") ||
    key === "data" ||
    key === "value_data"
  ) return FIELD_CLASS.CODE;
  if (key.includes("regex")) return FIELD_CLASS.REGEX;
  if (key.includes("registry")) return FIELD_CLASS.REGISTRY;
  if (key === "path" || key.endsWith("_path") || key.includes("directory") || key.includes("folder")) return FIELD_CLASS.PATH;
  if (key.includes("url") || key.includes("uri") || key.includes("endpoint")) return FIELD_CLASS.URL;
  if (key === "hostname" || key.includes("host_name") || key.endsWith("_host") || key.includes("cidr") || key.includes("ip_address")) return FIELD_CLASS.HOST;
  if (key.includes("slug")) return FIELD_CLASS.SLUG;
  if (
    key === "id" ||
    key.endsWith("_id") ||
    key.includes("guid") ||
    key.includes("uuid") ||
    key === "role" ||
    key === "action" ||
    key.endsWith("_action") ||
    key === "platform" ||
    key === "artifact" ||
    key === "branch" ||
    key === "service_mode"
  ) return FIELD_CLASS.IDENTIFIER;
  if (key.includes("description") || key.includes("notes") || key.includes("message") || key.includes("comment")) {
    return FIELD_CLASS.PLAIN_MULTILINE;
  }
  return FIELD_CLASS.PLAIN_SINGLE_LINE;
}

export function validateInputValue(field, value, fieldClass = classifyInputField(field)) {
  const text = String(value ?? "");
  const allowNewlines = [FIELD_CLASS.PLAIN_MULTILINE, FIELD_CLASS.SECRET, FIELD_CLASS.CODE, FIELD_CLASS.REGEX].includes(fieldClass);
  const maxLength = [FIELD_CLASS.SECRET, FIELD_CLASS.CODE].includes(fieldClass)
    ? SECRET_MAX_LENGTH
    : fieldClass === FIELD_CLASS.PATH || fieldClass === FIELD_CLASS.REGISTRY
      ? PATH_MAX_LENGTH
      : fieldClass === FIELD_CLASS.IDENTIFIER
        ? IDENTIFIER_MAX_LENGTH
        : DEFAULT_MAX_LENGTH;

  if (text.length > maxLength) return `${field} exceeds ${maxLength} characters.`;
  if (CONTROL_RE.test(text) || (!allowNewlines && /[\r\n\t]/.test(text))) return `${field} cannot include control characters.`;

  const trimmed = text.trim();
  if (!trimmed) return "";
  if (![FIELD_CLASS.SECRET, FIELD_CLASS.CODE, FIELD_CLASS.PATH, FIELD_CLASS.REGISTRY, FIELD_CLASS.REGEX].includes(fieldClass) && UNSAFE_MARKUP_RE.test(trimmed)) {
    return `${field} cannot include executable markup.`;
  }

  if (fieldClass === FIELD_CLASS.IDENTIFIER && !IDENTIFIER_RE.test(trimmed)) return `${field} must be an identifier, enum, or safe ref.`;
  if (fieldClass === FIELD_CLASS.SLUG && !SLUG_RE.test(trimmed)) return `${field} must be a lowercase slug.`;
  if (fieldClass === FIELD_CLASS.HOST && !isHostInput(trimmed)) return `${field} must be a hostname, IP address, or CIDR.`;
  if (fieldClass === FIELD_CLASS.URL) {
    try {
      const parsed = new URL(trimmed);
      if (!["http:", "https:", "ldap:", "ldaps:"].includes(parsed.protocol)) return `${field} uses an unsupported URL scheme.`;
    } catch {
      return `${field} must be an absolute URL.`;
    }
  }
  if (fieldClass === FIELD_CLASS.REGISTRY && (trimmed.includes("/") || /[<>"|?*]/.test(trimmed))) {
    return `${field} must be a Windows registry path or value name.`;
  }
  if (fieldClass === FIELD_CLASS.REGEX) {
    try {
      new RegExp(trimmed);
    } catch {
      return `${field} must be a valid regular expression.`;
    }
  }
  return "";
}

export function sanitizePayload(value, field = "body", fieldClass = classifyInputField(field)) {
  if (Array.isArray(value)) {
    const errors = [];
    const sanitized = value.map((item, index) => {
      const result = sanitizePayload(item, `${field}[${index}]`, fieldClass);
      errors.push(...result.errors);
      return result.value;
    });
    return { value: sanitized, errors };
  }

  const fileLike = typeof File !== "undefined" && value instanceof File;
  const blobLike = typeof Blob !== "undefined" && value instanceof Blob;
  if (value && typeof value === "object" && !fileLike && !blobLike) {
    const errors = [];
    const sanitized = {};
    for (const [key, child] of Object.entries(value)) {
      const childClass = classifyInputField(key);
      const result = sanitizePayload(child, key, childClass);
      errors.push(...result.errors.map((error) => ({ ...error, field: error.field.includes(".") ? error.field : key })));
      sanitized[key] = result.value;
    }
    return { value: sanitized, errors };
  }

  if (typeof value !== "string") return { value, errors: [] };
  const error = validateInputValue(field, value, fieldClass);
  if (error) return { value, errors: [{ field, message: error }] };

  if ([FIELD_CLASS.PLAIN_SINGLE_LINE, FIELD_CLASS.IDENTIFIER, FIELD_CLASS.SLUG, FIELD_CLASS.HOST, FIELD_CLASS.URL].includes(fieldClass)) {
    return { value: sanitizeSingleLineInput(value), errors: [] };
  }
  if (fieldClass === FIELD_CLASS.PLAIN_MULTILINE) {
    return { value: sanitizeMultilineInput(value), errors: [] };
  }
  return { value, errors: [] };
}

export function validateBorealisFetchRequest(input, init = {}, baseOrigin = globalThis.window?.location?.origin || "http://localhost") {
  const urlText = typeof input === "string" || input instanceof URL ? String(input) : String(input?.url || "");
  let parsed;
  try {
    parsed = new URL(urlText, baseOrigin);
  } catch {
    return { input, init, errors: [] };
  }
  const base = new URL(baseOrigin);
  if (parsed.origin !== base.origin || !parsed.pathname.startsWith("/api/") || parsed.pathname.startsWith("/api/internal/") || parsed.pathname === "/api/server/k3s/operator") {
    return { input, init, errors: [] };
  }

  const errors = [];
  const decodedPath = safeDecodeURIComponent(parsed.pathname);
  if (/[\u0000-\u001f\u007f]/.test(decodedPath) || UNSAFE_MARKUP_RE.test(decodedPath)) {
    errors.push({ field: "path", message: "path cannot include executable markup or control characters." });
  }
  for (const [key, value] of parsed.searchParams.entries()) {
    const error = validateInputValue(`query.${key}`, value, classifyInputField(key));
    if (error) errors.push({ field: `query.${key}`, message: error });
  }

  let nextInit = init;
  const body = init?.body;
  const contentType = headerValue(init?.headers, "content-type");
  if (typeof body === "string" && (contentType.includes("json") || /^[\s\r\n]*[\[{]/.test(body))) {
    try {
      const parsedBody = JSON.parse(body);
      const result = sanitizePayload(parsedBody);
      errors.push(...result.errors);
      if (!errors.length) {
        nextInit = {
          ...init,
          body: JSON.stringify(result.value),
          headers: withJSONContentType(init?.headers),
        };
      }
    } catch {
      errors.push({ field: "body", message: "Request body must be valid JSON." });
    }
  } else if (typeof FormData !== "undefined" && body instanceof FormData) {
    for (const [key, value] of body.entries()) {
      if (typeof File !== "undefined" && value instanceof File) continue;
      const error = validateInputValue(key, value, classifyInputField(key));
      if (error) errors.push({ field: key, message: error });
    }
  }

  return { input, init: nextInit, errors };
}

export function installBorealisInputValidationGuard(win = globalThis.window) {
  if (!win || win.__BorealisInputValidationGuardInstalled || typeof win.fetch !== "function") return;
  const originalFetch = win.fetch.bind(win);
  win.fetch = async (input, init = {}) => {
    const result = validateBorealisFetchRequest(input, init, win.location?.origin || "http://localhost");
    if (result.errors.length) {
      return new Response(JSON.stringify({ error: "validation_failed", errors: result.errors }), {
        status: 400,
        headers: { "Content-Type": "application/json" },
      });
    }
    return originalFetch(result.input, result.init);
  };
  Object.defineProperty(win, "__BorealisInputValidationGuardInstalled", {
    value: true,
    configurable: true,
  });
}

function headerValue(headers, key) {
  if (!headers) return "";
  if (typeof headers.get === "function") return String(headers.get(key) || "").toLowerCase();
  const found = Object.entries(headers).find(([name]) => name.toLowerCase() === key.toLowerCase());
  return String(found?.[1] || "").toLowerCase();
}

function withJSONContentType(headers) {
  if (!headers) return { "Content-Type": "application/json" };
  if (typeof Headers !== "undefined" && headers instanceof Headers) {
    const next = new Headers(headers);
    if (!next.has("Content-Type")) next.set("Content-Type", "application/json");
    return next;
  }
  return { ...headers, "Content-Type": headerValue(headers, "content-type") || "application/json" };
}

function safeDecodeURIComponent(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function isHostInput(value) {
  if (IPV4_RE.test(value) || HOST_RE.test(value)) return true;
  const parts = value.split("/");
  if (parts.length > 2) return false;
  const [address, prefix] = parts;
  if (prefix !== undefined && (!/^\d+$/.test(prefix) || Number(prefix) > 128)) return false;
  if (!address.includes(":")) return false;
  try {
    new URL(`http://[${address}]`);
    return true;
  } catch {
    return false;
  }
}
