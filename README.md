# MockCam - 仮想ネットワークカメラエミュレーター

[![CI/CD Pipeline](https://github.com/miyabiver39/mockcam/actions/workflows/ci.yml/badge.svg)](https://github.com/miyabiver39/mockcam/actions)
[![Release](https://img.shields.io/github/v/release/miyabiver39/mockcam?include_prereleases&color=06b6d4)](https://github.com/miyabiver39/mockcam/releases)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go)](go.mod)
[![Docker](https://img.shields.io/badge/Docker-Multi--Arch-2496ED?logo=docker)](Dockerfile)

`MockCam` は、VMS（ビデオ管理システム）や NVR、監視カメラ連携システムの開発・負荷検証のために設計された、高スケーラブルな**仮想ネットワークカメラエミュレーター**です。

Go による完全静的リンクバイナリと `gortsplib/v4` による 1:N ゼロコピー転送アーキテクチャにより、1 プロセスで **1,000 台以上の同時 RTSP 接続** を低負荷・低遅延に配信します。

---

## 🌟 主要機能ハイライト

* **🚀 1,000 台以上の超高スケーラブル配信**:
  * `gortsplib/v4` を採用し、FFmpeg からの RTP パケットをノンブロッキングにクライアントへファンアウト。
* **🎥 マルチプロファイル & ホットリロード**:
  * メインストリーム（1080p CBR）、サブストリーム（720p VBR）など複数プロファイルを独立管理。
  * 設定変更時は該当プロファイルのサブプロセスのみを安全にホットリロード（`SIGTERM` → 2 秒後 `Kill`）。他ストリームを一切中断させません。
* **⏱️ 時報同期音声 & タイムコード描画**:
  * 映像にはミリ秒単位の PTS タイムコードを描画。
  * 音声には毎秒ピッ音・周期ビープを同期させた時報モード（`time_signal`）および無音モード（`silent`）を搭載。
* **📡 ONVIF Profile S 完全準拠**:
  * **WS-Discovery**: UDP 3702（マルチキャスト `239.255.255.250`）による自動検出に対応。
  * **SOAP サービス群**: Device Service、Media Service、PTZ Service を規格に準拠して実装。
* **🧭 リアルタイム PTZ 仮想ステートマシン**:
  * メモリ上で Pan/Tilt/Zoom 座標を管理。VMS からの PTZ 操作や Web UI からの操作をリアルタイムに処理。
  * Web ダッシュボード上の SVG レーダー画面と WebSocket（`/ws`）で双方向リアルタイム同期。
* **💻 ビルドステップ不要の埋め込み Web ダッシュボード**:
  * Go の `embed` 機能により、単一バイナリ内に Tailwind CSS + Alpine.js ダッシュボードを完全内包。

---

## 🚀 Quick Start

### 1. Docker Compose（推奨）

リポジトリ直下で以下のコマンドを実行するだけで即時起動します。

```bash
docker compose up -d
```

### 2. Docker Run（単体実行）

```bash
docker run -d \
  --name mockcam \
  -p 8554:8554 \
  -p 8080:8080 \
  -p 3702:3702/udp \
  -v $(pwd)/config:/config \
  ghcr.io/miyabiver39/mockcam:latest
```

### 3. ローカルビルド & 実行 (Go 1.23+)

FFmpeg がインストールされている環境であれば、直接ビルド・実行も可能です。

```bash
# 依存関係取得
go mod download

# ビルド
go build -o mockcam cmd/mockcam/main.go

# 起動
./mockcam
```

> 初回起動時、`/config/settings.json`（または `./config/settings.json`）が存在しない場合はデフォルト設定が自動生成されます。

---

## 📡 接続方法ガイド

### 1. Web 管理ダッシュボード
ブラウザで以下の URL を開きます。
```text
http://localhost:8080
```
* **ステータスパネル**: 稼働時間、接続中の RTSP クライアント数、稼働プロファイル数を確認。
* **設定変更**: 解像度、FPS、GOP、ビットレート（CBR/VBR）、音声モードを GUI 上で即座に変更・再起動。
* **PTZ レーダー**: 十字キーやスライダーでカメラポインターを動かすと、リアルタイムに連動します。

### 2. VLC / ffplay での直接視聴

デフォルトの認証情報（`admin` / `admin1234`）を指定してストリームを開きます。

```bash
# メインストリーム (1080p 30fps CBR)
ffplay rtsp://admin:admin1234@localhost:8554/live/Profile_1

# サブストリーム (720p 15fps VBR)
ffplay rtsp://admin:admin1234@localhost:8554/live/Profile_2
```

### 3. VMS / ONVIF Device Manager での探索
1. 同一ネットワーク内の VMS や `ONVIF Device Manager (ODM)` を起動。
2. WS-Discovery（UDP 3702）により自動的に `MockCam` が一覧に表示されます。
3. 手動追加する場合のサービスアドレス:
   ```text
   http://<Host-IP>:8080/onvif/device_service
   ```
4. 認証: ユーザー名 `admin`、パスワード `admin1234`

---

## ⚙️ 設定仕様 (`settings.json`)

設定ファイルは `/config/settings.json`（または環境変数 `CONFIG_PATH` で指定したパス）に保存されます。

```json
{
  "server": {
    "rtsp_port": 8554,
    "http_port": 8080,
    "onvif_port": 3702,
    "auth_type": "digest",
    "auth_user": "admin",
    "auth_pass": "admin1234",
    "device_info": {
      "manufacturer": "MockCam Standard",
      "model": "MC-Pro-S",
      "firmware_version": "1.0.0",
      "serial_number": "MC2026090001",
      "hardware_id": "v1.0"
    }
  },
  "profiles": [
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
  ],
  "ptz": {
    "enabled": true,
    "node_token": "PTZNode_1",
    "pan": 0.0,
    "tilt": 0.0,
    "zoom": 0.0
  }
}
```

### 設定項目一覧

| セクション | キー | 説明 | デフォルト値 |
|---|---|---|---|
| `server` | `rtsp_port` | RTSP サーバー待受ポート | `8554` |
| `server` | `http_port` | Web UI, REST API, SOAP 待受ポート | `8080` |
| `server` | `onvif_port` | WS-Discovery マルチキャストポート | `3702` |
| `server` | `auth_type` | 認証方式 (`digest`, `basic`, `none`) | `digest` |
| `server` | `auth_user` | 認証ユーザー名 | `admin` |
| `server` | `auth_pass` | 認証パスワード | `admin1234` |
| `video` | `codec` | 映像コーデック (`H264`, `H265`) | `H264` |
| `video` | `bitrate_mode`| ビットレート制御 (`CBR`, `VBR`) | `CBR` |
| `audio` | `mode` | 音声モード (`time_signal`, `silent`, `""`) | `time_signal` |
| `ptz` | `pan` / `tilt` / `zoom` | 初期 PTZ 仮想座標 | `0.0` |

---

## ⚡ 性能チューニングガイド（1,000台以上の高負荷接続時）

1,000台以上のクライアントを同時接続して負荷検証を行う場合は、OS（Linux ホスト）のカーネルパラメータおよびファイルディスクリプタの上限を調整してください。

### 1. ファイルディスクリプタ上限（FD）の拡張
`/etc/security/limits.conf` に以下を追加:
```text
* soft nofile 65535
* hard nofile 65535
```
または Docker コンテナ起動時に `--ulimit nofile=65535:65535` を指定。

### 2. ネットワークソケット & カーネルパラメータ
`/etc/sysctl.conf` に以下を追加して `sysctl -p` を実行:
```ini
# ソケット受信キューの拡張
net.core.somaxconn = 65535

# SYN backlog キューの拡張
net.ipv4.tcp_max_syn_backlog = 8192

# ポート枯渇を防ぐためのローカルポート範囲拡大
net.ipv4.ip_local_port_range = 1024 65535

# TIME_WAIT ソケットの再利用
net.ipv4.tcp_tw_reuse = 1

# 送受信バッファの拡大
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
```

---

## 📖 詳細ドキュメント

* [ONVIF Profile S 詳細仕様書](docs/onvif_profile_s.md)
* [REST API & WebSocket 仕様書](docs/rest_api.md)

---

## 📄 ライセンス

本ソフトウェアは [MIT License](LICENSE) の下で公開されています。
