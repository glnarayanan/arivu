chrome.commands.onCommand.addListener((command) => {
  if (command === 'save-bookmark') {
    chrome.action.openPopup();
  }
});

chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
  if (request.action === 'saveTokens') {
    chrome.storage.session.set({
      accessToken: request.accessToken,
      refreshToken: request.refreshToken,
    });
    sendResponse({ success: true });
  }
  return true;
});
