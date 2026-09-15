# REST API & WebSocket 仕様書 (`MockCam`)

本ドキュメントは、MockCam の管理用 REST API および WebSocket リアルタイム通信仕様を記載する。機械可読な定義は [`openapi.yaml`](openapi.yaml)（OpenAPI 3.1）にあり、稼働中のインスタンスでは `/openapi.yaml` で配信、`/api/docs`（Scalar）で閲覧できる。

すべての API はデフォルトでポート `8080`（`server.http_port`）で提供される。管理 API は認証を要求しない（ONVIF SOAP と RTSP のみ `server.auth_type` に従う）。

* リクエストボディは JSON（最大 1 MiB）。**未知のフィールドは 400 エラー**になる（設定名のタイプミス検出のため）。
* エラーは `{"error": "<message>"}` 形式で返る。
* 許可されないメソッドは `405 Method Not Allowed`。

---

## 1. システム

### 1.1 稼働状態取得
`GET /api/status`

```json
{
  "version": "1.6.0",
  "model": "MC-Pro-S",
  "uptime_seconds": 128,
  "profiles_count": 2,
  "rtsp_clients": 4,
  "packets_sent": 123456,
  "bytes_sent": 98765432,
  "bitrate_kbps": 4120.5,
  "auth_type": "digest",
  "auth_user": "admin",
  "log_level": "INFO",
  "rtsp_port": 8554,
  "http_port": 8080,
  "onvif_port": 3702,
  "workers": [
    { "token": "Profile_1", "running": true, "restarts": 0, "pid": 225 },
    { "token": "Profile_2", "running": true, "restarts": 1, "pid": 13 }
  ],
  "previews": [
    { "token": "Profile_1", "frames": 41, "last_frame_bytes": 50918, "last_frame_at": "2026-09-15T08:35:42Z" }
  ]
}
```

`workers` は FFmpeg ワーカーの状態（`restarts` は自動再起動回数）、`previews` はプロファイルごとに受信した JPEG プレビューフレームの統計。

### 1.2 設定の取得・更新
`GET /api/config` — `settings.json` 全体を返す。

`PUT /api/config` — `server` セクションのみを更新する（`profiles` / `ptz` は無視）。`config.ValidateServer` によりポート範囲・認証方式・ログレベルが検証され、不正な場合は 400。`log_level` を含めると即座にログレベルが切り替わる。

```json
{ "server": { "rtsp_port": 8554, "http_port": 8080, "onvif_port": 3702, "auth_type": "digest", "auth_user": "admin", "auth_pass": "admin1234", "log_level": "INFO", "device_info": { "...": "..." } } }
```

### 1.3 ファクトリーリセット
`POST /api/config/reset` — 設定を初期状態に戻し、全プロファイルのワーカーを再起動する。

### 1.4 ログ
`GET /api/logs` — 直近 200 件のログ（`timestamp`, `level`, `source`, `message`）。

`PUT /api/logs` — `{"level": "DEBUG|INFO|WARN|ERROR"}` でログレベルを変更。

### 1.5 診断バンドル
`GET /api/diagnostics/export` — 設定・統計・ワーカー状態・接続クライアント・PTZ 状態・直近 500 件のログを 1 つの JSON にまとめてダウンロード（`Content-Disposition: attachment`）。

### 1.6 ライセンス情報
`GET /api/licenses` — アプリケーション情報と、同梱／利用しているサードパーティコンポーネントの一覧。

```json
{
  "application": { "name": "MockCam", "version": "1.6.0", "license": "MIT", "url": "https://github.com/miyabiver39/mockcam" },
  "components": [
    { "name": "github.com/bluenviron/gortsplib/v5", "version": "v5.6.5", "license": "MIT", "copyright": "...", "url": "...", "kind": "go" },
    { "name": "HTS Voice tohoku-f01 (neutral)", "license": "CC BY 4.0", "kind": "tts", "notes": "This product uses ..." }
  ]
}
```

`kind` は `go` / `frontend` / `runtime` / `tts` / `font`。

---

## 2. プロファイル

### 2.1 一覧・作成
`GET /api/profiles` — プロファイル配列。

`POST /api/profiles` — 新規作成。`token` は `[A-Za-z0-9_-]{1,64}` 必須。`config.ValidateProfile` で解像度・フレームレート・コーデック・音声モードなどを検証（不正なら 400、重複トークンは 409）。成功時 `201 Created`。

### 2.2 取得・更新・削除
`GET /api/profiles/{token}`
`PUT /api/profiles/{token}`
`DELETE /api/profiles/{token}`

* `PUT`: 設定を保存し、当該プロファイルの RTSP ストリームをクローズ（プレイヤーが新しい SDP で再接続できるように）した上で、**該当プロファイルの FFmpeg サブプロセスのみ**を再起動する（`SIGTERM` → 応答がなければ `Kill`）。ボディ内の `token` は無視され URL が優先される。
* `DELETE`: 最後の 1 件は削除不可（400）。存在しないトークンは 404。

リクエスト例 (PUT):
```json
{
  "token": "Profile_1",
  "name": "MainStream-CBR-1080p",
  "source_mode": "generate",
  "source_path": "",
  "video": {
    "codec": "H264",
    "resolution": { "width": 1920, "height": 1080 },
    "framerate": 30,
    "gop_size": 30,
    "bitrate_mode": "CBR",
    "bitrate_limit_kbps": 4000,
    "quality": 5.0,
    "pattern": "testsrc2",
    "osd_text": "",
    "show_clock": true,
    "enable_noise": false,
    "enable_motion_box": false
  },
  "audio": {
    "enabled": true,
    "mode": "time_signal_ja",
    "codec": "AAC",
    "bitrate_kbps": 128,
    "sample_rate": 44100
  }
}
```

---

## 3. 映像プレビュー

### 3.1 JPEG スナップショット
`GET /api/snapshot/{token}`（`HEAD` も可）— **実際に RTSP で配信している映像の最新フレーム**を JPEG で返す（`image/jpeg`、`Cache-Control: no-store`）。FFmpeg ワーカーの低レート MJPEG サイド出力から取得し、幅 1280 px を超える場合は縮小。ONVIF `GetSnapshotUri` が返す URL でもある。トークン省略時は `Profile_1`。

ワーカーがまだフレームを出していない間（起動直後、FFmpeg 不在時）は PTZ 状態を反映した合成プレビューを返す。レスポンスヘッダー `X-MockCam-Source` が `live` / `synthetic` のどちらかを示す。

### 3.2 MJPEG ライブプレビュー
`GET /api/mjpeg/{token}` — `multipart/x-mixed-replace; boundary=frame` で実映像を約 5 fps で配信（エンコーダがフレームを出すたびに即時プッシュ、無変化時は 2 秒ごとに直前フレームを再送）。ブラウザの `<img src>` や VMS の MJPEG カメラ入力で直接利用可能。ライブフレームが無い間は合成プレビューを 5 fps で送る。

### 3.3 時報 PCM ストリーム（内部用）
`GET /api/audio/timesignal?lang=ja|en` — `audio/l16; rate=48000; channels=1` の無限 PCM ストリーム。`time_signal_ja` / `time_signal_en` モードの FFmpeg ワーカーが音声入力として利用する。

---

## 4. PTZ

### 4.1 座標操作
`GET /api/ptz` — `{ "pan", "tilt", "zoom", "is_moving" }`

`POST /api/ptz`
```json
{ "action": "absolute", "pan": 0.5, "tilt": -0.2, "zoom": 0.4 }
{ "action": "continuous", "vel_pan": 1.0, "vel_tilt": 0.0, "vel_zoom": 0.0 }
{ "action": "stop" }
```
pan/tilt は `-1.0〜1.0`、zoom は `0.0〜1.0` にクランプされる。`continuous` は速度ベクトル × 10 %/秒 で座標を更新し続ける。

### 4.2 プリセット
`GET /api/ptz/presets` — `[{ "name", "pan", "tilt", "zoom" }]`

`POST /api/ptz/presets`
```json
{ "action": "save_current", "name": "Lobby" }   // 省略時は Preset_N。同名は上書き
{ "action": "goto", "name": "Lobby" }            // 存在しなければ 404
{ "action": "delete", "name": "Lobby" }
```

### 4.3 接続クライアント
`GET /api/clients` — RTSP リーダーセッション一覧 `[{ "id", "remote_ip", "path", "transport", "duration_seconds" }]`

---

## 5. MCP (`/mcp`)

`POST /mcp` — Model Context Protocol（Streamable HTTP トランスポート、JSON-RPC 2.0）。MCP クライアントをこの URL に向けると、REST API と同じ操作をツールとして利用できる（`get_status`, `list_profiles`, `update_profile`, `ptz_move`, `get_snapshot` など 20 種）。リソースは `mockcam://config`, `mockcam://status`, `mockcam://logs`, `mockcam://licenses`, `mockcam://openapi`, `mockcam://snapshot/{token}`。同じサーバーを `mockcam -mcp-stdio` で stdin/stdout でも提供できる（ログは stderr）。

## 6. WebSocket (`/ws`)

* **URL**: `ws://{Host}:{http_port}/ws`
* 接続直後に現在の PTZ 状態が 1 件送られる。

### 6.1 サーバーからのプッシュ
```json
{ "type": "ptz", "pan": 0.25, "tilt": -0.10, "zoom": 0.50, "is_moving": false }
{ "type": "log", "entry": { "timestamp": "2026-09-15 10:00:00.000", "level": "INFO", "source": "rtsp", "message": "..." } }
```

### 6.2 クライアントからの操作
```json
{ "action": "move", "pan": 0.0, "tilt": 0.5, "zoom": 0.2 }   // AbsoluteMove
{ "action": "stop" }
```
