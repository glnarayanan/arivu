'use strict';

let data;
const token = location.pathname.split('/')[2];
const esc = (value) => {
  const element = document.createElement('span');
  element.textContent = String(value || '');
  return element.innerHTML;
};

function render() {
  if (!data) return;
  const query = q.value.toLowerCase();
  const items = data.items.filter((item) => `${item.title} ${item.description} ${item.domain} ${item.text}`.toLowerCase().includes(query));
  items.sort((left, right) => sort.value === 'title'
    ? left.title.localeCompare(right.title)
    : sort.value === 'old'
      ? left.created_at.localeCompare(right.created_at)
      : right.created_at.localeCompare(left.created_at));
  resultCount.textContent = `${items.length} shared ${items.length === 1 ? 'item' : 'items'}`;
  list.innerHTML = items.map((item) => `<article>
    <p class="source">${esc(item.domain)}</p>
    <h2><a rel="noopener noreferrer" href="${esc(item.url)}">${esc(item.title)}</a></h2>
    ${item.description ? `<p class="description">${esc(item.description)}</p>` : ''}
    <div class="reader">${item.reader_html}</div>
    ${item.artifacts.length ? `<p class="artifacts" aria-label="Preserved artifacts">${item.artifacts.map((artifact) => `<a rel="noopener noreferrer" href="${esc(artifact.url)}">${esc(artifact.type)}</a>`).join(' ')}</p>` : ''}
  </article>`).join('') || '<p class="empty">No shared items match this search.</p>';
}

q.addEventListener('input', render);
sort.addEventListener('change', render);

fetch(`/api/public/shares/${encodeURIComponent(token)}`)
  .then((response) => {
    if (!response.ok) throw new Error();
    return response.json();
  })
  .then((result) => {
    data = result;
    t.textContent = data.title;
    d.textContent = data.description;
    render();
  })
  .catch(() => {
    document.body.innerHTML = '<main class="unavailable"><p class="brand">Arivu</p><h1>This share is unavailable.</h1><p>It may have expired or been revoked.</p></main>';
  });
