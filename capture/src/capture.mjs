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
    const context = await browser.newContext({ serviceWorkers: 'block', javaScriptEnabled: true });
    const page = await context.newPage();
    const imageResponses = new Map();
    const imageTasks = new Set();
    let capturedImageBytes = 0;
    let capturedImages = 0;
    page.on('response', (response) => {
      const task = (async () => {
        try {
          const mime = response.headers()['content-type']?.split(';', 1)[0].trim().toLowerCase();
          if (!imageMIMEs.has(mime) || capturedImages >= request.max_media_files) return;
          const length = Number(response.headers()['content-length'] ?? 0);
          if (length > request.max_media_file_bytes || capturedImageBytes + length > request.max_media_total_bytes) return;
          const body = await response.body();
          if (body.length < 1 || body.length > request.max_media_file_bytes || capturedImages >= request.max_media_files || capturedImageBytes + body.length > request.max_media_total_bytes) return;
          const image = { mime, body };
          imageResponses.set(response.url(), image);
          for (let redirected = response.request().redirectedFrom(); redirected; redirected = redirected.redirectedFrom()) imageResponses.set(redirected.url(), image);
          capturedImages += 1;
          capturedImageBytes += body.length;
        } catch {
          // A failed optional image body must not discard usable reader content.
        }
      })();
      imageTasks.add(task);
      task.finally(() => imageTasks.delete(task));
    });
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
    await Promise.allSettled([...imageTasks]);

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

    const media = [];
    const mediaPayloads = [];
    let mediaTotal = 0;
    for (const sourceURL of readerImageURLs(readerHTML, finalURL)) {
      const image = imageResponses.get(sourceURL);
      if (!image || media.length >= request.max_media_files || mediaTotal + image.body.length > request.max_media_total_bytes || acceptedBytes + image.body.length > request.max_total_bytes) continue;
      media.push({ source_url: sourceURL, role: 'reader_image', width: 0, height: 0, mime: image.mime, size: image.body.length });
      mediaPayloads.push(image.body);
      mediaTotal += image.body.length;
      acceptedBytes += image.body.length;
    }

    const response = {
      version: 2,
      token: request.token,
      engine_version: 'arivu-capture/0.1.0',
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
    engine_version: 'arivu-capture/0.1.0',
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
    const clone = document.cloneNode(true);
    for (const element of clone.querySelectorAll('script, noscript, template')) element.remove();
    const projection = new Readability(clone).parse();
    return {
      projection,
      metadata: {
        final_url: finalURL,
        canonical_url: isHTTPURL(canonical) ? canonical : '',
        title: projection?.title ?? document.title ?? '',
        description: projection?.excerpt ?? document.querySelector('meta[name="description"]')?.content ?? '',
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
  const timer = setTimeout(() => child.kill('SIGKILL'), Math.min(request.navigation_timeout_ms, 30_000));
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
