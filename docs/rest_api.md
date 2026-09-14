# REST API & WebSocket 仕様書 (`MockCam`)

本ドキュメントは、MockCam の管理用 REST API および WebSocket リアルタイム通信仕様を記載する。

---

## 1. REST API エンドポイント

すべての API はデフォルトでポート `8080`（`server.http_port`）で提供される。

### 1.1 システム稼働状態取得
`GET /api/status`

* **説明**: 送出プロファイル数、稼働時間、接続中の RTSP クライアント数、ポート一覧を返却。
* **レスポンス例**:
```json
{
  "profiles_count": 2,
  "uptime_seconds": 128,
  "rtsp_clients": 4,
  "version": "1.0.0",
  "model": "MC-Pro-S",
  "rtsp_port": 8554,
  "http_port": 8080,
  "onvif_port": 3702
}
```

---

### 1.2 設定情報取得
`GET /api/config`

* **説明**: 現在稼働中の `settings.json` の内容全体を取得。
* **レスポンス例**:
```json
{
  "server": { ... },
  "profiles": [ ... ],
  "ptz": { ... }
}
```

---

### 1.3 プロファイル設定取得・更新（ホットリロード）
`GET /api/profiles/{token}`
`PUT /api/profiles/{token}`

* **説明**:
  * `GET`: 指定されたプロファイルの現在のエンコード設定を取得。
  * `PUT`: プロファイル設定を更新。保存と同時に、該当プロファイルの FFmpeg サブプロセスのみに `SIGTERM`（2秒後に `Kill`）を送信し、他プロファイルの中断なく即座に新設定で再起動（ホットリロード）する。
* **リクエスト例 (PUT)**:
```json
{
  "token": "Profile_1",
  "name": "MainStream-CBR-1080p",
  "source_mode": "generate",
  "source_path": "",
  "video": {
    "codec": "H264",
    "resolution": { "width": 1920, "height": 1080 },
    "framerate": 60,
    "gop_size": 30,
    "bitrate_mode": "CBR",
    "bitrate_limit_kbps": 6000,
    "quality": 5.0
  },
  "audio": {
    "enabled": true,
    "mode": "time_signal",
    "codec": "AAC",
    "bitrate_kbps": 128,
    "sample_rate": 44100
  }
}
```

---

### 1.4 スナップショット取得
`GET /api/snapshot/{token}`

* **説明**: 指定プロファイルに対応した静止画（JPEG形式、`image/jpeg`）をリアルタイムに生成して返却。
* **レスポンス**: バイナリ画像データ (`image/jpeg`)
* **ヘッダー**: `Cache-Control: no-cache, no-store, must-revalidate`

---

### 1.5 PTZ 座標操作
`GET /api/ptz`
`POST /api/ptz`

* **説明**:
  * `GET`: 現在の仮想パン・チルト・ズーム座標および移動フラグを取得。
  * `POST`: Web から直接 PTZ 移動を実行（`absolute` または `continuous` または `stop`）。
* **リクエスト例 (POST)**:
```json
{
  "action": "absolute",
  "pan": 0.5,
  "tilt": -0.2,
  "zoom": 0.4
}
```

---

## 2. WebSocket 仕様 (`/ws`)

### 2.1 接続確立
* **URL**: `ws://{Host}:{http_port}/ws`
* **認証**: 必要に応じて HTTP Cookie または Header を伝搬。

### 2.2 サーバーからのプッシュ通知
PTZ 座標が更新された際（ONVIF 経由または Web API 経由）、接続されている全クライアントへ即時ブロードキャストされる。

* **メッセージ形式**:
```json
{
  "type": "ptz",
  "pan": 0.25,
  "tilt": -0.10,
  "zoom": 0.50,
  "is_moving": false
}
```

### 2.3 クライアントからの操作送信
WebSocket 経由でも PTZ 操作コマンドを送信可能。
```json
{
  "action": "move",
  "pan": 0.0,
  "tilt": 0.5,
  "zoom": 0.2
}
```
または停止コマンド:
```json
{
  "action": "stop"
}
```
