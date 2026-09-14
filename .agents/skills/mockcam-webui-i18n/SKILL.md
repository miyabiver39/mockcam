---
name: mockcam-webui-i18n
description: >-
  Use this skill when modifying or extending the embedded Web Dashboard in internal/web/static/index.html.
  Covers Alpine.js state management, adding new i18n translations, WebSocket message handling,
  compact 2-column layout guidelines, and asset referencing.
---

# MockCam Web UI & i18n Guide

The MockCam frontend is a single-file static application embedded into the binary using `go:embed`:
- File path: `internal/web/static/index.html`
- Frameworks: Tailwind CSS (CDN) + Alpine.js (CDN)

## 1. i18n Translation Conventions

All UI text is internationalized via Alpine.js `t(key)` helper.

### Supported Languages
1. `ja` (Japanese) - Default
2. `en` (English)
3. `zh` (Simplified Chinese)
4. `ko` (Korean)
5. `es` (Spanish)
6. `fr` (French)
7. `de` (German)

### Adding New Keys
Whenever a new button, setting, option, or tooltip is added:
1. Add the key to `i18nData` for all 7 languages:
   ```javascript
   ja: { ... my_new_key: '説明テキスト' },
   en: { ... my_new_key: 'Description text' },
   zh: { ... my_new_key: '说明文字' },
   ko: { ... my_new_key: '설명 텍스트' },
   es: { ... my_new_key: 'Texto descriptivo' },
   fr: { ... my_new_key: 'Texte descriptif' },
   de: { ... my_new_key: 'Beschreibungstext' }
   ```
2. Reference in HTML using:
   ```html
   <span x-text="t('my_new_key')">Fallback</span>
   ```

## 2. Layout Guidelines: Compact 2-Column Structure

To prevent excessive vertical scrolling:
- Group forms into 2-column grids (`grid grid-cols-1 md:grid-cols-2 gap-4`).
- Group related video controls (resolution, framerate, bitrate, GOP) in one card and audio/source controls in the adjacent column.
- Use badge chips and compact input rows (`text-xs` or `text-sm`, `py-1.5 px-2.5`).

## 3. Real-Time Streaming & WebSocket Updates

- **WebSocket (`/ws`)**:
  - Receives live JSON events: `type === 'ptz'`, `type === 'status'`, `type === 'log'`.
  - Pushes PTZ updates to server: `ws.send(JSON.stringify({ action: 'ptz_move', pan: ..., tilt: ..., zoom: ... }))`.
- **Live In-Browser MJPEG (`/api/mjpeg/:token`)**:
  - Live video preview stream via `multipart/x-mixed-replace`.
  - Toggle between static snapshot polling and live stream using `liveMJPEG` boolean state.
- **PTZ Keyboard Controls**:
  - Bound in `setupKeyboardPTZ()`: `W/A/S/D`, arrow keys, `+/-` zoom, `Space` home.
  - Automatically disabled when focusing `input`, `textarea`, or `select` elements.

## 4. About / Licenses Modal

- The header "ℹ️" button and the footer link call `openLicenses()`, which lazily fetches `GET /api/licenses` and opens `showLicenseModal`.
- Component data comes from `internal/licenses/licenses.go` (single source of truth). Do **not** hard-code library names in the HTML; add new dependencies there and `licenses_test.go` will cross-check `go.mod` and the CDN `<script>` tags.
- Group headings use the keys `licenses_kind_go|frontend|runtime|tts|font` — add translations for all 7 languages when adding a kind.

## 5. Static Asset References

- Assets reside in `internal/web/static/` and are served at root (`/`):
  - `favicon.png` -> `<link rel="icon" type="image/png" href="/favicon.png">`
  - `favicon.ico` -> `<link rel="shortcut icon" href="/favicon.ico">`
  - Emblem image -> `<img src="/favicon.png" class="w-8 h-8 rounded-lg shadow-md ...">`
