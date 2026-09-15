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
   * **EndpointReference/Address**: `urn:uuid:<v5 UUID>`（`device_info.serial_number` から導出される固定値）
   * **Scopes**:
     * `onvif://www.onvif.org/type/NetworkVideoTransmitter`
     * `onvif://www.onvif.org/name/MockCam`
     * `onvif://www.onvif.org/hardware/{Model}`
     * `onvif://www.onvif.org/location/any`
   * **XAddrs**: `http://{HostIP}:{http_port}/onvif/device_service`
   * **Types**: `dn:NetworkVideoTransmitter tds:Device`（`dn` / `tds` の名前空間はエンベロープで宣言済み）

---

## 2. SOAP サービスエンドポイント一覧

| サービス名 | エンドポイントパス | 主な機能 |
|---|---|---|
| Device Service | `/onvif/device_service` | デバイス情報、時刻同期、ケイパビリティ、サービス一覧、ネットワーク情報 |
| Media Service | `/onvif/media_service` | プロファイル一覧、RTSP URL取得、スナップショットURL取得、エンコーダー/ソース設定 |
| PTZ Service | `/onvif/ptz_service` | 仮想パン・チルト・ズーム制御、ホーム、プリセット、ステータス取得 |

### 2.1 アクションの判定
SOAP Body の先頭要素のローカル名でアクションを判定する。`SOAPAction` ヘッダー（および `Content-Type` の `action` パラメータ）は Body が空の場合のフォールバックとしてのみ使用する（一部クライアントの WSDL は `.../wsdlGetVideoSources/` のような不正な soapAction を送るため）。

### 2.2 認証
`server.auth_type` が `none` 以外のとき、以下のいずれかで認証する。

| 方式 | 内容 |
|---|---|
| **WS-Security UsernameToken** | SOAP Header の `wsse:Security/wsse:UsernameToken`。`PasswordDigest`（`Base64(SHA1(nonce + created + password))`）と `PasswordText` の両方に対応。`Created` はサーバー時刻との差が **±5 分以内**であること。ONVIF Device Manager・各社 VMS・`onvif-zeep` はこの方式を使う |
| **HTTP Basic / Digest** | `Authorization` ヘッダー。`auth_type` に従い Basic または Digest チャレンジを返す |

* 認証情報が無い場合: HTTP `401` + `WWW-Authenticate` チャレンジ + SOAP Fault（`ter:NotAuthorized`）を返す。HTTP 認証クライアントはリトライでき、SOAP クライアントは XML の Fault を受け取れる。
* UsernameToken が不正な場合: HTTP `400` + SOAP Fault（`env:Sender` / `ter:NotAuthorized`）。
* **認証不要（PRE_AUTH）アクション**: `GetSystemDateAndTime`、`GetCapabilities`。クライアントは `GetSystemDateAndTime` で時刻を同期してから UsernameToken を生成する。

### 2.3 SOAP Fault
ONVIF Core Specification に従い SOAP 1.2 Fault を返す。

| 状況 | HTTP | Code | Subcode |
|---|---|---|---|
| 未対応アクション | 500 | `s:Receiver` | `ter:ActionNotSupported` |
| XML 不正 | 400 | `s:Sender` | `ter:WellFormed` |
| 認証失敗 | 401 / 400 | `s:Sender` | `ter:NotAuthorized` |
| 存在しない ProfileToken | 400 | `s:Sender` | `ter:InvalidArgVal` → `ter:NoProfile` |
| 存在しない ConfigurationToken | 400 | `s:Sender` | `ter:InvalidArgVal` → `ter:NoConfig` |
| 存在しない PresetToken | 400 | `s:Sender` | `ter:InvalidArgVal` → `ter:NoToken` |
| 存在しない NodeToken | 400 | `s:Sender` | `ter:InvalidArgVal` → `ter:NoEntity` |

2 段の Subcode は `<s:Subcode><s:Value>ter:InvalidArgVal</s:Value><s:Subcode><s:Value>ter:NoProfile</s:Value></s:Subcode></s:Subcode>` のようにネストして出力する。

---

## 3. Device Service (`/onvif/device_service`)

| アクション | 内容 |
|---|---|
| `GetDeviceInformation` | `settings.json` の `server.device_info`（`Manufacturer`, `Model`, `FirmwareVersion`, `SerialNumber`, `HardwareId`）を返却 |
| `GetSystemDateAndTime` | サーバーの現在 UTC 時刻（`DateTimeType=Manual`, `TZ=UTC`）。認証不要 |
| `GetCapabilities` | Device / Media の XAddr と、`ptz.enabled` が true のときのみ PTZ の XAddr。認証不要 |
| `GetServices` | Device / Media（/ PTZ）の Namespace・XAddr・バージョン |
| `GetServiceCapabilities` | Network / Security（`UsernameToken="true"`, `HttpDigest="true"`）/ System |
| `GetScopes` | WS-Discovery と同じスコープ一覧 |
| `GetHostname` | `MockCam` |
| `GetNetworkInterfaces` | 仮想インターフェース `eth0`。IPv4 アドレスはリクエストの `Host` が IP リテラルならその値、そうでなければ `0.0.0.0` |
| `GetNetworkProtocols` | HTTP（`http_port`）、HTTPS（無効）、RTSP（`rtsp_port`） |
| `GetDNS` / `GetNTP` | 静的設定（エントリ無し） |
| `GetDiscoveryMode` | `Discoverable` |
| `GetUsers` | `auth_user` を `Administrator` として返却（パスワードは含まない） |
| `GetWsdlUrl` | `http://www.onvif.org/` |

XAddr のホスト部にはリクエストの `Host` ヘッダーのホストを使用するため、NAT 越しでもクライアントが到達したアドレスがそのまま返る。

---

## 4. Media Service (`/onvif/media_service`)

### 4.1 プロファイル
`GetProfiles` / `GetProfile` は `settings.json` の各プロファイルを ONVIF Profile S 互換の XML で返す。

* `VideoSourceConfiguration`（token `VSC_<token>`、SourceToken `VideoSource_1`）: 解像度バウンズ
* `AudioSourceConfiguration`（token `ASC_<token>`、SourceToken `AudioSource_1`）: 音声有効時のみ
* `VideoEncoderConfiguration`（token `VEC_<token>`）: `Encoding`（`H264` / `H265` / `JPEG`）、解像度、品質、フレームレート、ビットレート、H.264 の GOP 長
* `AudioEncoderConfiguration`（token `AEC_<token>`）: `Encoding`（`AAC` / `G711` / `G726` にマップ）、ビットレート、サンプリングレート
* `PTZConfiguration`（token `PTZConfig_1`、全プロファイルで共有）: `ptz.enabled` が true のときのみ。`GetConfigurations` の token と一致する

### 4.2 URI
| アクション | 返却値 |
|---|---|
| `GetStreamUri` | `rtsp://{Host}:{rtsp_port}/live/{token}` |
| `GetSnapshotUri` | `http://{Host}:{http_port}/api/snapshot/{token}` |

`ProfileToken` が空の場合は先頭プロファイルにフォールバックし、存在しないトークンは `ter:NoProfile` Fault になる。

### 4.3 その他のアクション
`GetVideoSources`、`GetVideoSourceConfigurations` / `GetVideoSourceConfiguration`、`GetVideoEncoderConfigurations` / `GetVideoEncoderConfiguration` / `GetVideoEncoderConfigurationOptions`、`GetAudioSources`、`GetAudioSourceConfigurations`、`GetAudioEncoderConfigurations` / `GetAudioEncoderConfiguration`、`GetServiceCapabilities`（`SnapshotUri="true"`, `RTP_RTSP_TCP="true"`）。

プロファイルは固定（`fixed="true"`）のため、`Set*` 系のアクションは提供しない（`ActionNotSupported`）。

---

## 5. PTZ Service (`/onvif/ptz_service`)

MockCam は内部メモリ上に仮想座標ステートマシンを保持し、VMS からの PTZ コマンドに応答する。

### 5.1 仮想座標系
* **Pan**: `-1.0`（最左） 〜 `+1.0`（最右）
* **Tilt**: `-1.0`（最下） 〜 `+1.0`（最上）
* **Zoom**: `0.0`（広角等倍） 〜 `1.0`（2倍望遠）

すべての座標は範囲に丸め（clamp）られる。

### 5.2 サポートアクション
| アクション | 内容 |
|---|---|
| `GetStatus` | 現在座標と移動状態（`IDLE` / `MOVING`）、UTC 時刻 |
| `AbsoluteMove` | 指定座標へ瞬時に移動。`PanTilt` / `Zoom` を省略した軸は現在値を維持 |
| `RelativeMove` | `Translation` を現在座標に加算 |
| `ContinuousMove` | 速度ベクトル（毎秒 10% 更新）で仮想移動を開始 |
| `Stop` | 継続移動を停止 |
| `GotoHomePosition` / `SetHomePosition` | ホーム位置へ移動 / 現在位置をホームに設定（既定は原点。メモリ上のみ保持） |
| `GetPresets` / `SetPreset` / `GotoPreset` / `RemovePreset` | プリセット操作。プリセット名をトークンとして使用し、`settings.json` の `ptz.presets` に永続化。REST API（`/api/ptz/presets`）・MCP と共通。最大 32 件 |
| `GetNodes` / `GetNode` | ノード `ptz.node_token`。Absolute / Relative / Continuous / Speed の各空間と `HomeSupported=true` |
| `GetConfigurations` / `GetConfiguration` / `GetConfigurationOptions` | 共有 PTZ 設定 `PTZConfig_1`（既定空間、タイムアウト、リミット） |
| `GetServiceCapabilities` | `MoveStatus="true"`, `StatusPosition="true"` |

### 5.3 リアルタイム連動
ONVIF 経由で PTZ 座標が更新されると、Web ダッシュボードへ WebSocket（`/ws`）を通じて即時にブロードキャストされ、レーダー画面上のカメラポインターが同期連動する。

---

## 6. 動作確認の例（onvif-zeep）

```bash
pip install onvif-zeep
python - <<'EOF'
from onvif import ONVIFCamera
cam = ONVIFCamera("192.168.10.5", 8080, "admin", "admin1234")
dev = cam.create_devicemgmt_service()
print(dev.GetDeviceInformation())
media = cam.create_media_service()
prof = media.GetProfiles()[0]
print(media.GetStreamUri({"StreamSetup": {"Stream": "RTP-Unicast", "Transport": {"Protocol": "RTSP"}}, "ProfileToken": prof.token}).Uri)
ptz = cam.create_ptz_service()
ptz.AbsoluteMove({"ProfileToken": prof.token, "Position": {"PanTilt": {"x": 0.5, "y": -0.5}, "Zoom": {"x": 0.25}}})
print(ptz.GetStatus({"ProfileToken": prof.token}).Position)
EOF
```
