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
  * 設定変更時は該当プロファイルのサブプロセスのみを安全にホットリロード（`SIGTERM` → 応答がなければ `Kill`）。他ストリームを一切中断させません。
  * FFmpeg が異常終了した場合は自動的に再起動します。
* **🎞️ テストパターン & オーバーレイ**:
  * 映像ソースは `testsrc2` / `smptebars` / `allrgb` / `mptestsrc` から選択、または動画ファイルをループ再生（`source_mode: "file"`）。
  * PTS タイムコード（`HH:MM:SS.mmm`）、任意の OSD テキスト、センサーノイズ、VMS の動体検知テスト用の移動バウンディングボックスを重畳可能。
* **⏱️ 時報同期音声**:
  * 時報モード（`time_signal`）、無音（`silent`）、ホワイトノイズ（`noise`）、チャイム（`chime`）の 4 種類の音声モードを搭載。
* **📡 ONVIF Profile S 完全準拠**:
  * **WS-Discovery**: UDP 3702（マルチキャスト `239.255.255.250`）による自動検出に対応。
  * **SOAP サービス群**: Device Service、Media Service、PTZ Service を規格に準拠して実装。
* **🧭 リアルタイム PTZ 仮想ステートマシン**:
  * メモリ上で Pan/Tilt/Zoom 座標を管理。VMS からの PTZ 操作や Web UI からの操作をリアルタイムに処理。
  * Web ダッシュボード上の SVG レーダー画面と WebSocket（`/ws`）で双方向リアルタイム同期。
  * PTZ プリセットの保存・呼び出し（`settings.json` に永続化）。
* **💻 ビルドステップ不要の埋め込み Web ダッシュボード**:
  * Go の `embed` 機能により、単一バイナリ内に Tailwind CSS + Alpine.js ダッシュボードを完全内包。
  * ライブ診断ログ（FFmpeg の stderr を含む）、接続中 RTSP クライアント一覧、設定のエクスポート / ファクトリーリセット、診断情報の一括エクスポートに対応。

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

# 設定ファイルのパスを明示する場合
./mockcam -config ./config/settings.json
```

> 設定ファイルの探索順は `-config` フラグ → 環境変数 `CONFIG_PATH` → 既定パス（Linux/macOS: `/config/settings.json`、Windows: `./config/settings.json`）です。ファイルが存在しない場合はデフォルト設定が自動生成されます。
>
> Windows では WS-Discovery のマルチキャスト待受に失敗することがありますが、警告ログのみで起動は継続します（RTSP / Web / SOAP は利用可能）。

---

## 📡 接続方法ガイド

### 1. Web 管理ダッシュボード
ブラウザで以下の URL を開きます。
```text
http://localhost:8080
```
* **ステータスパネル**: 稼働時間、接続中の RTSP クライアント数、送出パケット数・ビットレート、稼働プロファイル数を確認。
* **プロファイル管理**: 解像度、FPS、GOP、ビットレート（CBR/VBR）、テストパターン、OSD テキスト、音声モードを GUI 上で即座に変更・ホットリロード。プロファイルの追加・削除も可能。
* **PTZ レーダー & プリセット**: 十字キーやスライダーでカメラポインターを動かすと、リアルタイムに連動します。現在位置をプリセットとして保存・呼び出しできます。
* **ライブスナップショット**: PTZ 位置を反映した JPEG プレビューを一定間隔で自動更新。
* **診断ログ & クライアント一覧**: システムログと FFmpeg の出力をリアルタイム表示、接続中の RTSP セッションを一覧化。
* **設定のバックアップ / リセット**: `settings.json` のエクスポート、ファクトリーリセット、診断情報（設定・統計・ログ）の一括エクスポート。

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

設定ファイルは `/config/settings.json`（Windows では `./config/settings.json`、または `-config` フラグ / 環境変数 `CONFIG_PATH` で指定したパス）に保存されます。Web UI や REST API から変更した内容は即座に同ファイルへ書き戻されます。

初回起動時に自動生成されるデフォルト設定（メイン / サブの 2 プロファイル）:

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
        "quality": 5,
        "show_clock": false,
        "enable_noise": false,
        "enable_motion_box": false
      },
      "audio": {
        "enabled": true,
        "mode": "time_signal",
        "codec": "AAC",
        "bitrate_kbps": 128,
        "sample_rate": 44100
      }
    },
    {
      "token": "Profile_2",
      "name": "SubStream-VBR-720p",
      "source_mode": "generate",
      "source_path": "",
      "video": {
        "codec": "H264",
        "resolution": { "width": 1280, "height": 720 },
        "framerate": 15,
        "gop_size": 30,
        "bitrate_mode": "VBR",
        "bitrate_limit_kbps": 1000,
        "quality": 3,
        "show_clock": false,
        "enable_noise": false,
        "enable_motion_box": false
      },
      "audio": {
        "enabled": true,
        "mode": "silent",
        "codec": "AAC",
        "bitrate_kbps": 64,
        "sample_rate": 44100
      }
    }
  ],
  "ptz": {
    "enabled": true,
    "node_token": "PTZNode_1",
    "pan": 0,
    "tilt": 0,
    "zoom": 0
  }
}
```

### 設定項目一覧

#### `server`

| キー | 説明 | デフォルト値 |
|---|---|---|
| `rtsp_port` | RTSP サーバー待受ポート | `8554` |
| `http_port` | Web UI, REST API, SOAP 待受ポート | `8080` |
| `onvif_port` | WS-Discovery マルチキャストポート | `3702` |
| `auth_type` | 認証方式 (`digest`, `basic`, `none`)。RTSP と ONVIF SOAP に共通で適用 | `digest` |
| `auth_user` / `auth_pass` | 認証ユーザー名 / パスワード | `admin` / `admin1234` |
| `log_level` | ログ出力レベル (`DEBUG`, `INFO`, `WARN`, `ERROR`)。省略時は `INFO` | （省略） |
| `device_info.*` | ONVIF `GetDeviceInformation` で返すメーカー・モデル・ファームウェア・シリアル・ハードウェア ID | 上記参照 |

#### `profiles[]`

| キー | 説明 | デフォルト値 |
|---|---|---|
| `token` | プロファイル識別子。RTSP パス `/live/<token>` および ONVIF プロファイルトークンになる | `Profile_1` |
| `name` | 表示名 | `MainStream-CBR-1080p` |
| `source_mode` | `generate`（FFmpeg のテストパターン生成）または `file`（動画ファイルをループ再生） | `generate` |
| `source_path` | `file` モード時の動画ファイルパス（コンテナでは `/media` 配下）。存在しない場合は `generate` にフォールバック | `""` |
| `video.codec` | 映像コーデック (`H264`, `H265`) | `H264` |
| `video.resolution` | `width` / `height` | `1920x1080` |
| `video.framerate` | フレームレート (fps) | `30` |
| `video.gop_size` | GOP 長（キーフレーム間隔） | `30` |
| `video.bitrate_mode` | ビットレート制御 (`CBR`, `VBR`) | `CBR` |
| `video.bitrate_limit_kbps` | ビットレート上限 (kbps)。CBR では固定値、VBR では `maxrate` | `4000` |
| `video.quality` | 予約項目（現在のエンコード処理では未使用） | `5` |
| `video.pattern` | テストパターン (`testsrc2`, `smptebars`, `allrgb`, `mptestsrc`)。`generate` モードのみ | `testsrc2` |
| `video.osd_text` | 左上に重畳する任意の OSD テキスト | `""` |
| `video.show_clock` | PTS タイムコード（`HH:MM:SS.mmm`）を画面下部に描画。`osd_text` が空の場合は常に描画 | `false` |
| `video.enable_noise` | センサーノイズ（グレイン）を付加 | `false` |
| `video.enable_motion_box` | 動体検知テスト用の赤い移動バウンディングボックスを描画 | `false` |
| `audio.enabled` | 音声トラックの有無 | `true` |
| `audio.mode` | 音声モード (`time_signal`: 880Hz 時報, `silent`: 無音, `noise`: ホワイトノイズ, `chime`: チャイム) | `time_signal` |
| `audio.codec` | 音声コーデック（現在は `AAC` のみ） | `AAC` |
| `audio.bitrate_kbps` / `audio.sample_rate` | 音声ビットレート (kbps) / サンプリングレート (Hz) | `128` / `44100` |

#### `ptz`

| キー | 説明 | デフォルト値 |
|---|---|---|
| `enabled` | PTZ サービスの有効化 | `true` |
| `node_token` | ONVIF PTZ ノードトークン | `PTZNode_1` |
| `pan` / `tilt` | 仮想座標（`-1.0`〜`1.0`）。移動のたびに現在値が保存される | `0` |
| `zoom` | 仮想ズーム（`0.0`〜`1.0`） | `0` |
| `speed` | 予約項目（現在未使用。ContinuousMove は速度ベクトル × 10%/秒 で座標を更新） | （省略） |
| `presets[]` | 保存済みプリセット `{ "name", "pan", "tilt", "zoom" }` の配列。Web UI / `/api/ptz/presets` から管理 | （省略） |

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
* [AI コーディングエージェント向け開発ガイド](AGENTS.md)（Claude Code は `CLAUDE.md`、GitHub Copilot は `.github/copilot-instructions.md` 経由で参照）

---

## 📄 ライセンス

本ソフトウェアは [MIT License](LICENSE) の下で公開されています。

### サードパーティライブラリ・データセット

本ソフトウェアには、以下のサードパーティコンポーネントが含まれています。

| コンポーネント | ライセンス | 著作権者 |
|---|---|---|
| [Open JTalk](https://open-jtalk.sourceforge.net/) | Modified BSD License | Copyright (C) 2008-2016 Nagoya Institute of Technology |
| [HTS Engine API](https://hts-engine.sourceforge.net/) | Modified BSD License | Copyright (C) 2001-2015 HTS Working Group |
| NAIST-jdic (open_jtalk_dic_utf_8-1.11) | BSD License | Copyright (C) 2008-2016 Nagoya Institute of Technology |
| HTS Voice nitech_jp_atr503_m001 | [CC BY 3.0](https://creativecommons.org/licenses/by/3.0/) | HTS Working Group, Nagoya Institute of Technology |
| [gortsplib/v4](https://github.com/bluenviron/gortsplib) | MIT License | bluenviron |
| [gorilla/websocket](https://github.com/gorilla/websocket) | BSD-2-Clause License | The Gorilla Authors |
| [google/uuid](https://github.com/google/uuid) | BSD-3-Clause License | Google LLC |

> **HTS Voice nitech_jp_atr503_m001 (CC BY 3.0) Attribution**:
> This product uses the HTS voice model `nitech_jp_atr503_m001` created by the HTS Working Group at Nagoya Institute of Technology, licensed under the [Creative Commons Attribution 3.0 Unported License](https://creativecommons.org/licenses/by/3.0/).
