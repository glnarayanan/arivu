import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Readability } from '@mozilla/readability';
import { JSDOM } from 'jsdom';
import { chromium } from 'playwright';
import { assessQuality } from './quality.mjs';
import { startProxyRelay } from './relay.mjs';

const imageMIMEs = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp']);

export async function capturePage(request, signal) {
  const relay = await startProxyRelay(request.proxy_socket);
  let temp;
  let browser;
  let monolith;
  const abort = () => {
    void browser?.close().catch(() => {});
    void relay.close().catch(() => {});
    monolith?.kill('SIGKILL');
  };
  signal?.addEventListener('abort', abort, { once: true });
  try {
    if (signal?.aborted) throw coded('capture_cancelled');
    temp = await mkdtemp(join(tmpdir(), 'arivu-helper-'));
    try {
      browser = await chromium.launch({
        headless: true,
        proxy: { server: `http://127.0.0.1:${relay.port}`, username: 'arivu', password: request.proxy_token },
        args: ['--disable-quic', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--force-webrtc-ip-handling-policy=disable_non_proxied_udp'],
      });
      if (signal?.aborted) {
        await browser.close();
        throw coded('capture_cancelled');
      }
    } catch {
      throw coded('browser_launch_failed');
    }
    const context = await browser.newContext({
      serviceWorkers: 'block',
      javaScriptEnabled: true,
      userAgent: browserUserAgent(browser.version()),
      locale: 'en-US',
      viewport: { width: 1440, height: 900 },
    });
    const page = await context.newPage();
    await page.route('**/*', async (route) => {
      const protocol = new URL(route.request().url()).protocol;
      if (protocol === 'http:' || protocol === 'https:' || protocol === 'data:' || protocol === 'blob:') await route.continue();
      else await route.abort('blockedbyclient');
    });
    try {
      await page.goto(request.url, { waitUntil: 'domcontentloaded', timeout: request.navigation_timeout_ms });
    } catch (error) {
      throw coded(error?.name === 'TimeoutError' ? 'navigation_timeout' : 'navigation_failed');
    }
    await page.waitForLoadState('networkidle', { timeout: Math.min(5000, request.navigation_timeout_ms) }).catch(() => {});
    await boundedScroll(page);

    const finalURL = page.url();
    const renderedHTML = await page.content();
    if (Buffer.byteLength(renderedHTML) > request.max_file_bytes) throw coded('rendered_html_too_large');
    const extracted = extractPage(renderedHTML, finalURL);
    const projection = extracted.projection;
    if (!projection?.content || !projection?.textContent?.trim()) throw coded('readability_failed');
    const readerHTML = projection.content;
    const readerText = projection.textContent.trim();
    const html = Buffer.from(readerHTML);
    const text = Buffer.from(readerText);
    if (html.length > request.max_file_bytes || text.length > request.max_file_bytes) throw coded('reader_output_too_large');
    const quality = assessQuality({ text: readerText, title: projection.title, html: readerHTML });

    const artifacts = [];
    const artifactPayloads = [];
    let acceptedBytes = html.length + text.length;
    if (acceptedBytes > request.max_total_bytes) throw coded('reader_output_too_large');
    const capturedMedia = await collectReaderMedia(page.request, readerImageURLs(readerHTML, finalURL), request, acceptedBytes);
    const media = capturedMedia.media;
    const mediaPayloads = capturedMedia.payloads;
    acceptedBytes += capturedMedia.total;
    const components = { browser: { status: 'complete', error_code: '' }, readability: { status: 'complete', error_code: '' } };
    let errorCode = '';
    for (const format of request.formats) {
      try {
        const artifact = await buildArtifact(format, { page, renderedHTML, finalURL, request, relayPort: relay.port, temp, signal, setMonolith: (child) => { monolith = child; } });
        if (acceptedBytes + artifact.body.length > request.max_total_bytes) throw coded(`${format}_too_large`);
        artifacts.push({ type: format, mime: artifact.mime, size: artifact.body.length });
        artifactPayloads.push(artifact.body);
        acceptedBytes += artifact.body.length;
        components[format] = { status: 'complete', error_code: '' };
      } catch (error) {
        const code = safeCode(error, `${format}_failed`);
        components[format] = { status: 'failed', error_code: code };
        if (!errorCode) errorCode = code;
      }
    }

    const response = {
      version: 2,
      token: request.token,
      engine_version: 'arivu-capture/0.2.0',
      metadata: extracted.metadata,
      content: {
        html: { mime: 'text/html', size: html.length },
        text: { mime: 'text/plain', size: text.length },
        quality_status: quality.status,
        quality_score: quality.score,
        quality_reasons: quality.reasons,
        challenge: quality.challenge,
      },
      artifacts,
      media,
      components,
      error_code: errorCode,
    };
    return { response, payloads: [html, text, ...artifactPayloads, ...mediaPayloads] };
  } finally {
    signal?.removeEventListener('abort', abort);
    await browser?.close().catch(() => {});
    await relay.close().catch(() => {});
    if (temp) await rm(temp, { recursive: true, force: true });
  }
}

export function failedResponse(request, error) {
  const errorCode = safeCode(error, 'capture_failed');
  const readerFailure = errorCode === 'readability_failed' || errorCode === 'reader_output_too_large';
  const components = readerFailure
    ? { browser: { status: 'complete', error_code: '' }, readability: { status: 'failed', error_code: errorCode } }
    : { browser: { status: 'failed', error_code: errorCode } };
  return {
    version: 2,
    token: request.token,
    engine_version: 'arivu-capture/0.2.0',
    metadata: { final_url: '', canonical_url: '', title: '', description: '', byline: '', site_name: '', language: '', published_at: '' },
    content: { html: { mime: '', size: 0 }, text: { mime: '', size: 0 }, quality_status: 'failed', quality_score: 0, quality_reasons: [], challenge: false },
    artifacts: [],
    media: [],
    components,
    error_code: errorCode,
  };
}

export function extractPage(renderedHTML, finalURL) {
  const dom = new JSDOM(renderedHTML, { url: finalURL, contentType: 'text/html' });
  try {
    const document = dom.window.document;
    const canonical = document.querySelector('link[rel~="canonical"]')?.href ?? '';
    const publisherDescription = document.querySelector('meta[name="description" i]')?.content?.trim()
      || document.querySelector('meta[property="og:description" i]')?.content?.trim()
      || '';
    const clone = document.cloneNode(true);
    for (const element of clone.querySelectorAll('script, noscript, template')) element.remove();
    const projection = new Readability(clone).parse();
    return {
      projection,
      metadata: {
        final_url: finalURL,
        canonical_url: isHTTPURL(canonical) ? canonical : '',
        title: projection?.title ?? document.title ?? '',
        description: publisherDescription || projection?.excerpt || '',
        byline: projection?.byline ?? '',
        site_name: projection?.siteName ?? '',
        language: projection?.lang ?? document.documentElement.lang ?? '',
        published_at: projection?.publishedTime ?? '',
      },
    };
  } finally {
    dom.window.close();
  }
}

export function readerImageURLs(readerHTML, baseURL) {
  const dom = new JSDOM(readerHTML, { url: baseURL, contentType: 'text/html' });
  try {
    const result = new Set();
    for (const image of dom.window.document.querySelectorAll('img[src]')) {
      if (isHTTPURL(image.src)) result.add(image.src);
    }
    return result;
  } finally {
    dom.window.close();
  }
}

export async function collectReaderMedia(apiRequest, sourceURLs, limits, acceptedBytes) {
  const media = [];
  const payloads = [];
  let total = 0;
  for (const sourceURL of sourceURLs) {
    if (media.length >= limits.max_media_files) break;
    let response;
    try {
      response = await apiRequest.get(sourceURL, {
        failOnStatusCode: false,
        maxRedirects: 5,
        headers: { 'Accept-Encoding': 'identity' },
      });
      if (!response.ok()) continue;
      const headers = response.headers();
      const mime = headers['content-type']?.split(';', 1)[0].trim().toLowerCase();
      const length = Number(headers['content-length'] ?? 0);
      if (!imageMIMEs.has(mime) || length > limits.max_media_file_bytes || total + length > limits.max_media_total_bytes || acceptedBytes + total + length > limits.max_total_bytes) continue;
      const body = await response.body();
      if (body.length < 1 || body.length > limits.max_media_file_bytes || total + body.length > limits.max_media_total_bytes || acceptedBytes + total + body.length > limits.max_total_bytes) continue;
      media.push({ source_url: sourceURL, role: 'reader_image', width: 0, height: 0, mime, size: body.length });
      payloads.push(body);
      total += body.length;
    } catch {
      // A failed reader image must not discard usable article text.
    } finally {
      await response?.dispose?.().catch(() => {});
    }
  }
  return { media, payloads, total };
}

export function browserUserAgent(version) {
  return `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${version} Safari/537.36`;
}

async function boundedScroll(page) {
  let previous = 0;
  for (let step = 0; step < 10; step += 1) {
    const height = await page.evaluate(() => document.documentElement.scrollHeight);
    if (height <= previous) break;
    previous = height;
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    await page.waitForTimeout(200);
  }
  await page.evaluate(() => window.scrollTo(0, 0));
}

async function buildArtifact(format, options) {
  let body;
  let mime;
  if (format === 'screenshot') {
    body = await options.page.screenshot({ type: 'jpeg', quality: 80, fullPage: true });
    mime = 'image/jpeg';
  } else if (format === 'pdf') {
    body = await options.page.pdf({ printBackground: true, preferCSSPageSize: true });
    mime = 'application/pdf';
  } else {
    body = await runMonolith(options);
    mime = 'text/html';
  }
  if (body.length < 1 || body.length > options.request.max_file_bytes) throw coded(`${format}_too_large`);
  return { body, mime };
}

async function runMonolith({ renderedHTML, finalURL, request, relayPort, temp, signal, setMonolith }) {
  const command = process.env.ARIVU_MONOLITH_PATH || 'monolith';
  const proxy = `http://arivu:${encodeURIComponent(request.proxy_token)}@127.0.0.1:${relayPort}`;
  const child = spawn(command, ['-I', '-j', '-f', '-F', '-a', '-v', '-M', '-b', finalURL, '-'], {
    cwd: temp,
    env: { ...process.env, HTTP_PROXY: proxy, HTTPS_PROXY: proxy, http_proxy: proxy, https_proxy: proxy, NO_PROXY: '', no_proxy: '' },
    stdio: ['pipe', 'pipe', 'ignore'],
  });
  setMonolith(child);
  if (signal?.aborted) child.kill('SIGKILL');
  child.stdin.on('error', () => {});
  const chunks = [];
  let size = 0;
  let timer;
  if (Number.isSafeInteger(request.navigation_timeout_ms) && request.navigation_timeout_ms > 0 && request.navigation_timeout_ms <= 30_000) {
    timer = setTimeout(() => child.kill('SIGKILL'), request.navigation_timeout_ms);
  } else {
    timer = setTimeout(() => child.kill('SIGKILL'), 30_000);
  }
  child.stdout.on('data', (chunk) => {
    size += chunk.length;
    if (size > request.max_file_bytes) child.kill('SIGKILL');
    else chunks.push(chunk);
  });
  child.stdin.end(renderedHTML);
  let exitCode;
  try {
    [exitCode] = await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', (...args) => resolve(args));
    });
  } finally {
    clearTimeout(timer);
    setMonolith(undefined);
  }
  if (exitCode !== 0 || size > request.max_file_bytes) throw coded('monolith_failed');
  return Buffer.concat(chunks, size);
}

function safeCode(error, fallback) {
  return typeof error?.code === 'string' && /^[a-z0-9_]{1,64}$/.test(error.code) ? error.code : fallback;
}

function coded(code) {
  return Object.assign(new Error(code), { code });
}

function isHTTPURL(value) {
  try {
    const protocol = new URL(value).protocol;
    return protocol === 'http:' || protocol === 'https:';
  } catch {
    return false;
  }
}
