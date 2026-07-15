import assert from 'node:assert/strict';
import test from 'node:test';
import { browserUserAgent, collectReaderMedia, extractPage, readerImageURLs } from '../src/capture.mjs';
import { assessQuality } from '../src/quality.mjs';

const articleText = Array.from({ length: 240 }, (_, index) => `Rendered evidence sentence ${index + 1}.`).join(' ');

test('extracts a static article with useful metadata and figures', () => {
  const result = extractPage(`<!doctype html><html lang="en"><head>
    <title>Static article</title><meta name="description" content="A durable description">
    <link rel="canonical" href="https://example.com/canonical">
  </head><body><nav>Site navigation</nav><article><h1>Static article</h1>
    <p>${articleText}</p><figure><img src="/media/figure.jpg" alt="Evidence figure"><figcaption>Evidence caption</figcaption></figure>
  </article></body></html>`, 'https://example.com/articles/static');

  assert.equal(result.metadata.canonical_url, 'https://example.com/canonical');
  assert.equal(result.metadata.language, 'en');
  assert.match(result.projection.textContent, /Rendered evidence sentence 240/);
  assert.match(result.projection.content, /Evidence figure/);
});

test('prefers the publisher description over a generated reader excerpt', () => {
  const result = extractPage(`<!doctype html><html><head>
    <title>Guide 2.0</title><meta name="description" content="The publisher-authored summary.">
  </head><body><article><h1>Guide 2.0</h1><p>${articleText}</p></article></body></html>`, 'https://example.com/guide');

  assert.equal(result.metadata.description, 'The publisher-authored summary.');
});

test('browser identity tracks the installed Chromium version', () => {
  assert.match(browserUserAgent('149.0.7758.0'), /Chrome\/149\.0\.7758\.0 Safari/);
});

test('extracts the final DOM produced by a JavaScript application', () => {
  const result = extractPage(`<html><head><title>Rendered application</title></head><body>
    <div id="app"><main><article><h1>Client-rendered story</h1><p>${articleText}</p></article></main></div>
  </body></html>`, 'https://app.example/story');

  assert.equal(result.projection.title, 'Rendered application');
  assert.match(result.projection.content, /Client-rendered story/);
  assert.match(result.projection.textContent, /Rendered evidence sentence 200/);
  assert.equal(assessQuality({ text: result.projection.textContent, title: result.projection.title, html: result.projection.content }).status, 'complete');
});

test('resolves and deduplicates reader media without treating data URLs as fetches', () => {
  const urls = [...readerImageURLs(`<p>Story</p>
    <img src="/images/one.jpg"><img src="https://cdn.example/two.webp">
    <img src="/images/one.jpg"><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">`, 'https://example.com/story')];

  assert.deepEqual(urls, ['https://example.com/images/one.jpg', 'https://cdn.example/two.webp']);
});

test('fetches reader images directly instead of depending on viewport-loaded responses', async () => {
  const requested = [];
  const request = {
    async get(url) {
      requested.push(url);
      return {
        ok: () => true,
        url: () => url,
        headers: () => ({ 'content-type': 'image/webp', 'content-length': '5' }),
        body: async () => Buffer.from('image'),
        dispose: async () => {},
      };
    },
  };
  const limits = { max_media_files: 2, max_media_file_bytes: 10, max_media_total_bytes: 20 };

  const result = await collectReaderMedia(request, [
    'https://cdn.example/first.webp',
    'https://cdn.example/second.webp',
    'https://cdn.example/over-limit.webp',
  ], limits, 0);

  assert.deepEqual(requested, ['https://cdn.example/first.webp', 'https://cdn.example/second.webp']);
  assert.deepEqual(result.media.map((item) => item.source_url), requested);
  assert.equal(result.payloads.length, 2);
  assert.equal(result.total, 10);
});

test('classifies a rendered challenge as unsafe replacement evidence', () => {
  const result = extractPage(`<html><head><title>Checking your browser</title></head><body>
    <main><h1>Verify that you are human</h1><p>${articleText.slice(0, 200)}</p></main>
  </body></html>`, 'https://example.com/protected');
  const quality = assessQuality({ text: result.projection?.textContent ?? '', title: result.metadata.title, html: result.projection?.content ?? '' });

  assert.equal(quality.challenge, true);
  assert.ok(quality.reasons.includes('challenge_detected'));
  assert.notEqual(quality.status, 'complete');
});
