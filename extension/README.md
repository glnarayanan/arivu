# Arivu Browser Extension

Save bookmarks directly into Arivu from Chrome or Firefox.

## What It Does

- Saves current tab URL to Arivu with optional quick note and tags
- Saves from the page, link, or selected-text context menu without opening the popup
- Sends selected text as a quote annotation
- Lets users pick a target collection
- Uses extension session tokens issued by Arivu (`/api/auth/extension-token`)
- Supports custom/self-hosted API URL through popup settings

## Installation

### Chrome
1. Open `chrome://extensions/`
2. Enable `Developer mode`
3. Click `Load unpacked`
4. Select the `extension/` directory in this repo

### Firefox
1. Open `about:debugging#/runtime/this-firefox`
2. Click `Load Temporary Add-on`
3. Select `extension/manifest.json`

## Default Endpoints

- Default API URL in popup: `https://arivu.app/api`
- Built-in host permissions: `https://arivu.app/*` and `http://localhost/*`
- Self-hosted origins are requested from the browser when the API URL is saved

## Self-Hosted Setup

### 1. Set API URL in Popup

1. Open the extension popup
2. Click `Settings`
3. Set API URL to `https://your-domain.example/api`
4. Approve the browser permission prompt for that Arivu origin

This value is stored in `chrome.storage.local` as `apiUrl`. The extension then
registers the token content script for that origin without requiring a manifest
edit.

### 2. Authenticate

1. Log into your self-hosted Arivu web app
2. Visit the app in the same browser
3. The content script requests extension tokens from `/api/auth/extension-token`
4. Tokens are stored in `chrome.storage.session`

## Troubleshooting

### "Log in to save bookmarks" persists

- Confirm you are logged into Arivu on the same browser profile
- Open your Arivu app once and refresh the page
- Verify your `apiUrl` setting matches your deployed domain

### Save fails with 401

- Session token expired; open Arivu and re-authenticate
- Ensure backend `/api/auth/extension-token` is reachable

### Save fails on self-hosted domain

- Reopen popup settings and confirm the API URL is saved
- Confirm the browser permission prompt was approved for your Arivu origin
- Confirm API URL ends with `/api`

### Context menu save fails

- Open Arivu once while logged in so the content script can refresh extension tokens
- Selected text is stored as a quote annotation on the saved bookmark
- Use the popup save path when you need to choose a collection

## Privacy and Storage

- Access/refresh tokens are stored in `chrome.storage.session`
- Custom API URL is stored in `chrome.storage.local`
- Extension only sends data when the user saves from the popup, keyboard shortcut, or context menu
- Selected text is sent directly with the save request when available and is not stored by the extension
- Extension bearer tokens are accepted only by audience-scoped extension API routes, including `/api/extension/bookmarks` and `/api/extension/collections`
