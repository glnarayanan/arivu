function normalizeApiUrl(value) {
  const parsed = new URL(value);
  if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
    throw new Error('Use an HTTP or HTTPS Arivu URL');
  }
  parsed.hash = '';
  parsed.search = '';
  parsed.pathname = parsed.pathname.replace(/\/+$/, '');
  if (!parsed.pathname.endsWith('/api')) {
    parsed.pathname = `${parsed.pathname}/api`.replace(/\/+/g, '/');
  }
  return parsed.toString().replace(/\/$/, '');
}

function apiOriginPattern(value) {
  const parsed = new URL(value);
  return `${parsed.protocol}//${parsed.hostname}/*`;
}

function builtInApiOrigin(pattern) {
  return pattern === 'https://arivu.app/*' || pattern === 'http://localhost/*';
}

function senderOriginAllowed(senderUrl, apiUrl) {
  if (!senderUrl) return false;
  const allowedOrigins = new Set(['https://arivu.app', 'http://localhost']);
  try {
    allowedOrigins.add(new URL(apiUrl).origin);
  } catch {
    // Invalid custom URL should not block the built-in origins.
  }
  try {
    const parsed = new URL(senderUrl);
    return parsed.hostname === 'localhost' || allowedOrigins.has(parsed.origin);
  } catch {
    return false;
  }
}

globalThis.ArivuExtensionURL = {
  normalizeApiUrl,
  apiOriginPattern,
  builtInApiOrigin,
  senderOriginAllowed,
};
