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

function joinApiUrl(apiUrl, path) {
  const normalized = normalizeApiUrl(apiUrl);
  if (typeof path !== 'string' || !path.startsWith('/') || path.startsWith('//')) {
    throw new Error('API path must be root-relative');
  }
  const parsed = new URL(normalized);
  parsed.pathname = `${parsed.pathname.replace(/\/$/, '')}${path}`;
  return parsed.toString();
}

function appUrl(apiUrl, path = '/') {
  const parsed = new URL(normalizeApiUrl(apiUrl));
  parsed.pathname = parsed.pathname.replace(/\/api$/, '');
  const suffix = String(path || '/');
  if (!suffix.startsWith('/') || suffix.startsWith('//')) throw new Error('App path must be root-relative');
  parsed.pathname = `${parsed.pathname.replace(/\/$/, '')}${suffix}`;
  return parsed.toString();
}

function apiOriginPattern(value) {
  const parsed = new URL(value);
  return `${parsed.protocol}//${parsed.hostname}/*`;
}

function builtInApiOrigin(pattern) {
  return pattern === 'https://arivu.app/*';
}

function senderOriginAllowed(senderUrl, apiUrl) {
  try {
    return new URL(senderUrl).origin === new URL(normalizeApiUrl(apiUrl)).origin;
  } catch {
    return false;
  }
}

globalThis.ArivuExtensionURL = {
  normalizeApiUrl,
  joinApiUrl,
  appUrl,
  apiOriginPattern,
  builtInApiOrigin,
  senderOriginAllowed,
};
