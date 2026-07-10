(() => {
  const DEFAULT_API_URL = 'https://arivu.app/api';
  const MAX_QUOTE_LENGTH = 4000;
  let composer = null;

  function selectedPageText() {
    const selection = window.getSelection?.();
    if (!selection || selection.rangeCount === 0 || !document.body) return null;
    const range = selection.getRangeAt(0);
    const start = range.startContainer.nodeType === Node.ELEMENT_NODE ? range.startContainer : range.startContainer.parentElement;
    const end = range.endContainer.nodeType === Node.ELEMENT_NODE ? range.endContainer : range.endContainer.parentElement;
    const common = range.commonAncestorContainer.nodeType === Node.ELEMENT_NODE ? range.commonAncestorContainer : range.commonAncestorContainer.parentElement;
    const editable = common?.closest?.('input, textarea, select, [contenteditable=""], [contenteditable="true"], [contenteditable="plaintext-only"]');
    const quote = selection.toString().replace(/\s+/g, ' ').trim().slice(0, MAX_QUOTE_LENGTH);
    if (!start || !end || !document.body.contains(start) || !document.body.contains(end) || editable || !quote) return null;
    return { quote, range };
  }

  function closeComposer() {
    composer?.remove();
    composer = null;
  }

  function positionComposer(range) {
    const rect = range.getBoundingClientRect();
    const width = Math.min(352, window.innerWidth - 32);
    composer.style.left = `${Math.max(16, Math.min(rect.left, window.innerWidth - width - 16))}px`;
    composer.style.top = `${Math.min(window.innerHeight - 16, Math.max(16, rect.bottom + 12))}px`;
    if (window.innerWidth <= 760) {
      composer.style.top = 'auto';
      composer.style.right = '16px';
      composer.style.bottom = 'max(16px, env(safe-area-inset-bottom))';
      composer.style.left = '16px';
      composer.style.width = 'auto';
    } else if (composer.getBoundingClientRect().bottom > window.innerHeight - 16) {
      composer.style.top = `${Math.max(16, rect.top - composer.offsetHeight - 12)}px`;
    }
  }

  function openComposer() {
    const selection = selectedPageText();
    if (!selection) {
      closeComposer();
      return;
    }
    if (composer?.dataset.quote === selection.quote) return;
    closeComposer();
    composer = document.createElement('section');
    composer.dataset.arivuAnnotationComposer = 'true';
    composer.dataset.quote = selection.quote;
    composer.setAttribute('role', 'dialog');
    composer.setAttribute('aria-label', 'Annotate selected passage');
    composer.style.cssText = 'position:fixed;z-index:2147483646;width:min(22rem,calc(100vw - 2rem));max-height:min(32rem,calc(100vh - 2rem));overflow:auto;border:2px solid #0f0f0f;background:#fff;box-shadow:8px 8px 0 #0f0f0f;padding:16px;color:#0f0f0f;font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;';
    composer.innerHTML = `<form style="display:grid;gap:12px">
      <p style="margin:0;font:700 11px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;letter-spacing:.08em;text-transform:uppercase">Selected passage</p>
      <blockquote data-annotation-quote style="margin:0;max-height:9rem;overflow:auto;border:2px solid rgba(15,15,15,.28);background:#fffbeb;padding:12px;font-size:14px;line-height:1.45"></blockquote>
      <label style="display:grid;gap:6px;font:700 11px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;letter-spacing:.08em;text-transform:uppercase">Your note<textarea data-annotation-note rows="3" placeholder="Why this matters" style="width:100%;resize:vertical;border:2px solid #0f0f0f;padding:10px;font:14px/1.4 ui-sans-serif,-apple-system,BlinkMacSystemFont,&quot;Segoe UI&quot;,sans-serif"></textarea></label>
      <p data-annotation-status aria-live="polite" hidden style="margin:0;border:2px solid #0f0f0f;padding:8px;font:700 12px/1.35 ui-monospace,SFMono-Regular,Consolas,monospace"></p>
      <p style="display:flex;flex-wrap:wrap;gap:8px;margin:0"><button type="button" data-annotation-cancel style="min-height:40px;border:2px solid #0f0f0f;background:#fff;color:#0f0f0f;padding:0 12px;font:700 12px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace">Cancel</button><button type="submit" style="min-height:40px;border:2px solid #0f0f0f;background:#f97316;color:#fff;padding:0 12px;font:700 12px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;box-shadow:4px 4px 0 #0f0f0f">Save annotation</button></p>
    </form>`;
    composer.querySelector('[data-annotation-quote]').textContent = selection.quote;
    document.documentElement.append(composer);
    positionComposer(selection.range);

    const form = composer.querySelector('form');
    const note = composer.querySelector('[data-annotation-note]');
    const status = composer.querySelector('[data-annotation-status]');
    composer.querySelector('[data-annotation-cancel]').addEventListener('click', closeComposer);
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const save = form.querySelector('button[type="submit"]');
      save.disabled = true;
      save.textContent = 'Saving annotation';
      status.hidden = true;
      try {
        const response = await chrome.runtime.sendMessage({
          action: 'captureAnnotation',
          url: location.href,
          title: document.title,
          quote: selection.quote,
          note: note.value,
        });
        if (!response?.success) throw new Error(response?.error || 'Could not save annotation');
        status.textContent = 'Annotation saved';
        status.style.background = '#dcfce7';
        status.hidden = false;
        window.setTimeout(closeComposer, 700);
      } catch (error) {
        status.textContent = error.message || 'Could not save annotation';
        status.style.background = '#fee2e2';
        status.hidden = false;
        save.disabled = false;
        save.textContent = 'Save annotation';
      }
    });
    window.requestAnimationFrame(() => note.focus());
  }

  async function initialize() {
    const settings = await chrome.storage.local.get(['apiUrl']).catch(() => ({}));
    try {
      if (location.origin === new URL(settings.apiUrl || DEFAULT_API_URL).origin) return;
    } catch {
      return;
    }
    document.addEventListener('pointerup', () => window.requestAnimationFrame(openComposer));
    document.addEventListener('keyup', (event) => {
      if (event.shiftKey || event.key === 'Shift') window.requestAnimationFrame(openComposer);
    });
    document.addEventListener('pointerdown', (event) => {
      if (composer && !composer.contains(event.target)) closeComposer();
    });
    document.addEventListener('keydown', (event) => {
      if (composer && event.key === 'Escape') {
        event.preventDefault();
        closeComposer();
      }
    });
  }

  initialize();
})();
