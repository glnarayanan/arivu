# Capture a note

Capture a note lets a user create an account, save a titled standalone note from Capture or Notes, and confirm it from a second user-facing view plus stored state.

## Sub-features

- `auth-signup` creates the verify account from `/auth`.
- `capture-open` opens the Capture dialog from the global Capture button.
- `capture-note-save` persists a note kind from that dialog.
- `notes-workspace-save` persists the same note shape from `/notes`.
- `capture-reopen` confirms the note from the Notes list and note detail.

## How to get to it (user POV)

- Open `{URL}/auth`, fill Email and Password, choose `Create account`.
- Choose the `Capture` button in the top bar, or press `Q` while focus is outside a field.
- Choose `Notes` in the Primary nav, then use the `New note` form.
- From Home, choose the `New note` link under Fast capture.

## Driving it with control-arivu

Preconditions:

- `control-arivu doctor` reports a healthy isolated URL and disposable database.
- No user `verify@arivu.test` exists yet (fresh launch).
- No note is titled `Release checklist`.

- **Create account.** Open `{URL}/auth`. Fill `Email` with `verify@arivu.test` and `Password` with `VerifyPass9`. Choose `Create account`. Toast `Account created` appears and `#route-title` reads `Home`. The brand link `Arivu home` is visible.
- **Notes workspace.** Choose `Notes` in `nav` named `Primary`. `#route-title` reads `Notes`. Fill `Title` with `Release checklist` and `Body` with `Tag and publish`. Choose `Save note`. Toast `Note saved` appears and a link heading `Release checklist` is in the list.
- **Reopen.** Choose the `Release checklist` link. `#route-title` reads `Release checklist`. The `Title` field shows `Release checklist` and `Body` shows `Tag and publish`.
- **Capture dialog entry.** Return to any authenticated page. Choose `Capture`. A dialog named `Capture` appears. Set `What are you capturing?` to `Note`. Fill `Title` with `Capture dialog note` and `Note` with `From global capture`. Choose `Save to Arivu`. Toast `Captured` appears and the app opens `/notes/:id` with heading `Capture dialog note`.
- **Stored state.** Run `control-arivu login` then `control-arivu api GET /api/notes`. The JSON `notes` array includes both titles. Run `control-arivu db -- "SELECT title FROM notes ORDER BY title;"` and expect `Capture dialog note` and `Release checklist`.
- **Proof.** From `/notes`, capture `.cursor/skills/verify-arivu/artifacts/$RUN_ID/capture-note/list.aria.txt` and `list.png`. Both identify Arivu, heading `Notes`, and `Release checklist`. Also save `notes.json` from the API GET.

## Gotchas

- `Create account` is a button, not the form submit. Submit is `Sign in` and will fail if the account does not exist.
- Some agent browser harnesses block clicking **Create account**. Prefer Playwright MCP; if blocked, run `control-arivu signup` then **Sign in** in the browser, and record that substitution in the artifact META.
- Password must be at least 8 characters. `VerifyPass9` meets that.
- Pressing `Q` while a textbox has focus types the character instead of opening Capture.
- A toast alone is insufficient proof. Reopen from the Notes list and read SQLite or `/api/notes`.
- Do not use `POST /api/notes` as the action under test. The API GET is a second view only.
- `arivu save` creates bookmarks, not notes. Do not treat CLI save as this feature.
- Link capture fetches the URL through the SSRF shield and is a different path; do not substitute it here.
