async function fetchExtensionTokens() {
  try {
    const apiBase = window.location.origin + '/api';
    const response = await fetch(`${apiBase}/auth/extension-token`, {
      method: 'POST',
      credentials: 'include',
    });

    if (!response.ok) return;

    const data = await response.json();
    if (data.access_token && data.refresh_token) {
      chrome.runtime.sendMessage({
        action: 'saveTokens',
        accessToken: data.access_token,
        refreshToken: data.refresh_token,
      });
    }
  } catch (error) {
    // User not logged in or network error — silent fail
  }
}

fetchExtensionTokens();

window.addEventListener('message', (event) => {
  if (event.source !== window) return;

  if (event.data.type === 'ARIVU_SAVE_TOKENS') {
    chrome.runtime.sendMessage({
      action: 'saveTokens',
      accessToken: event.data.accessToken,
      refreshToken: event.data.refreshToken,
    });
  }

  // Legacy support
  if (event.data.type === 'ARIVU_SAVE_TOKEN') {
    chrome.runtime.sendMessage({
      action: 'saveTokens',
      accessToken: event.data.token,
      refreshToken: event.data.token,
    });
  }
});
