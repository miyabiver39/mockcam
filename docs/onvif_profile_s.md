# ONVIF Profile S 仕様書 (`MockCam`)

本ドキュメントは、MockCam における ONVIF Profile S（SOAP / WS-Discovery）準拠の仕様およびエンドポイント定義をまとめたものである。

---

## 1. WS-Discovery 仕様

* **プロトコル**: UDP マルチキャスト
* **マルチキャストアドレス**: `239.255.255.250`
* **ポート**: `3702`
* **Action**: `http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe`

### 動作フロー
1. クライアント（VMS、ONVIF Device Manager など）がネットワーク上に `Probe` メッセージをマルチキャスト送信。
2. MockCam がパケットを受信し、送信元クライアントのユニキャスト UDP ポートへ `ProbeMatches` を返信。
3. 返信に含まれる主要フィールド:
   * **Scopes**:
     * `onvif://www.onvif.org/type/NetworkVideoTransmitter`
     * `onvif://www.onvif.org/name/MockCam`
     * `onvif://www.onvif.org/hardware/{Model}`
     * `onvif://www.onvif.org/location/any`
   * **XAddrs**: `http://{HostIP}:{http_port}/onvif/device_service`
   * **Types**: `dn:NetworkVideoTransmitter tds:Device`

---

## 2. SOAP サービスエンドポイント一覧

| サービス名 | エンドポイントパス | 主な機能 |
|---|---|---|
| Device Service | `/onvif/device_service` | デバイス情報、時刻同期、ケイパビリティ、サービス一覧取得 |
| Media Service | `/onvif/media_service` | プロファイル一覧、RTSP URL取得、スナップショットURL取得 |
| PTZ Service | `/onvif/ptz_service` | 仮想パン・チルト・ズーム制御、ステータス取得 |

---

## 3. Device Service (`/onvif/device_service`)

### 3.1 `GetDeviceInformation`
`settings.json` の `server.device_info` に定義されたハードウェア情報を返却する。
* レスポンス要素: `Manufacturer`, `Model`, `FirmwareVersion`, `SerialNumber`, `HardwareId`

### 3.2 `GetSystemDateAndTime`
サーバーの現在の UTC 時刻を ISO/ONVIF フォーマットで返却する。
* 時刻方式: `Manual` または `NTP`
* レスポンス要素: `UTCDateTime` (Year, Month, Day, Hour, Minute, Second)

### 3.3 `GetCapabilities`
MockCam が提供する各サービスのエンドポイント XAddr を返却する。
* `Device`: `http://{Host}:{Port}/onvif/device_service`
* `Media`: `http://{Host}:{Port}/onvif/media_service`
* `PTZ`: `http://{Host}:{Port}/onvif/ptz_service`

---

## 4. Media Service (`/onvif/media_service`)

### 4.1 `GetProfiles`
ONVIF Profile S 互換の完全なプロファイル XML を返却する。
各プロファイルには以下が含まれる:
* `VideoSourceConfiguration`: 解像度バウンズ、フレームレート
* `AudioSourceConfiguration`: 音声ソース定義（有効時）
* `VideoEncoderConfiguration`: H.264 / H.265 コーデック、解像度、ビットレート、GOP長、品質
* `AudioEncoderConfiguration`: AAC コーデック、ビットレート、サンプリングレート（有効時）
* `PTZConfiguration`: 紐づく PTZ ノードトークン

### 4.2 `GetStreamUri`
指定された `ProfileToken` に対応する RTSP 配信 URI を返却する。
* 返却値: `rtsp://{Host}:{rtsp_port}/live/{token}`

### 4.3 `GetSnapshotUri`
指定された `ProfileToken` の静止画スナップショット URL を返却する。
* 返却値: `http://{Host}:{http_port}/api/snapshot/{token}`

---

## 5. PTZ Service (`/onvif/ptz_service`)

MockCam は内部メモリ上に仮想座標ステートマシンを保持し、VMS からの PTZ コマンドに応答する。

### 5.1 仮想座標系
* **Pan**: `-1.0`（最左） 〜 `+1.0`（最右）
* **Tilt**: `-1.0`（最下） 〜 `+1.0`（最上）
* **Zoom**: `0.0`（広角等倍） 〜 `1.0`（2倍望遠）

### 5.2 サポートアクション
* `GetStatus`: 現在のパン・チルト・ズーム座標および移動状態（`IDLE` / `MOVING`）を返却。
* `ContinuousMove`: 速度ベクトル（毎秒10%更新）を受け取り、仮想移動を開始。
* `AbsoluteMove`: 指定した座標へ瞬時に仮想移動。
* `Stop`: 継続的な仮想移動を即座に停止。
* `GetNodes` / `GetConfigurations`: サポートする空間座標レンジ（[-1.0, 1.0]）とノード定義を返却。

### 5.3 リアルタイム連動
ONVIF 経由で PTZ 座標が更新されると、Web ダッシュボードへ WebSocket（`/ws`）を通じて即時にブロードキャストされ、レーダー画面上のカメラポインターが同期連動する。
