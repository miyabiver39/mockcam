# AGENTS.md — MockCam 開発ガイド（AI エージェント向け）

このファイルは、MockCam リポジトリで作業するすべての AI コーディングエージェント（Claude Code、GitHub Copilot、Codex、Cursor など）に共通する指針です。ツール固有の補足は `CLAUDE.md` および `.github/copilot-instructions.md` を参照してください。

## 1. プロジェクト概要

MockCam は VMS / NVR の開発・負荷検証用の **仮想ネットワークカメラエミュレーター** です。Go 製の単一静的バイナリで、以下を 1 プロセスで提供します。

| 機能 | ポート | 実装パッケージ |
|---|---|---|
| RTSP 配信（1:N ファンアウト、`/live/<token>`） | TCP 8554 | `internal/rtsp` |
| Web ダッシュボード / REST API / WebSocket (`/ws`) | TCP 8080 | `internal/web` |
| ONVIF SOAP（Device / Media / PTZ、`/onvif/*_service`） | TCP 8080（同居） | `internal/onvif` |
| ONVIF WS-Discovery（マルチキャスト `239.255.255.250`） | UDP 3702 | `internal/onvif/discovery.go` |

映像・音声は **FFmpeg サブプロセス**が生成し、ループバック経由で自プロセスの RTSP サーバーへ publish → `gortsplib/v4` の `ServerStream` がクライアントへゼロコピー配信、という構成です。

## 2. リポジトリ構成

```
cmd/mockcam/main.go        エントリポイント。各サブシステムを順に起動し、SIGINT/SIGTERM で graceful shutdown
internal/
  config/                  settings.json の読み書き・型定義・デフォルト値 (types.go / config.go)
  auth/                    Basic / Digest 認証（HTTP と RTSP で共用、realm="MockCam"）
  logger/                  リングバッファ付き構造化ロガー。Web UI へ WebSocket でライブ配信される
  supervisor/              プロファイルごとの FFmpeg ワーカー管理（起動・自動再起動・ホットリロード）
    ffmpeg_cmd.go          設定 → FFmpeg 引数列を組み立てる純粋関数 BuildFFmpegArgs
  rtsp/                    gortsplib ベースの RTSP サーバー（publish 受付・reader ファンアウト・統計）
  onvif/                   SOAP ディスパッチ (server.go)、Device/Media ハンドラ、PTZ 状態機械 (ptz.go)、WS-Discovery
  web/                     HTTP サーバー (server.go)、REST/WS ハンドラ (api.go)、埋め込み UI (static/index.html)
docs/                      REST API 仕様・ONVIF Profile S 仕様（日本語）
.github/workflows/ci.yml   go vet → go test → Docker multi-arch ビルド & ghcr.io push
Dockerfile / compose.yml   alpine + ffmpeg ランタイム
```

- Go モジュール名は `mockcam`（`import "mockcam/internal/..."`）。
- 外部依存は `gortsplib/v4`、`pion/rtp`・`pion/rtcp`、`gorilla/websocket`、`google/uuid` のみ。**新しい依存を追加する前に標準ライブラリで代替できないか検討**してください。
- フロントエンドは `internal/web/static/index.html` 1 ファイル（Tailwind CDN + Alpine.js）。ビルドステップは無く、`go:embed` でバイナリに同梱されます。

## 3. 開発コマンド

```bash
go mod download
go build -o mockcam ./cmd/mockcam        # ビルド
go run ./cmd/mockcam -config ./config/settings.json   # ローカル起動（FFmpeg が PATH に必要）
go vet ./...                             # CI と同じ静的検査
go test ./...                            # 単体テスト（FFmpeg 不要）
gofmt -l .                               # 未整形ファイルの一覧
docker compose up -d --build             # コンテナ起動
```

- **テストは FFmpeg 無しで動く**よう設計されています（`BuildFFmpegArgs` は純粋関数、Web テストは supervisor/rtsp に `nil` を渡す）。この性質を壊さないでください。
- 設定ファイルの既定パス: Linux は `/config/settings.json`、Windows は `./config/settings.json`、環境変数 `CONFIG_PATH` または `-config` フラグで上書き。存在しなければデフォルト設定が自動生成されます。
- 既定の認証情報は `admin` / `admin1234`（`config.DefaultConfig()`）。

## 4. アーキテクチャ上の重要な決まりごと

### 設定 (`config.Manager`)
- `Manager` は `sync.RWMutex` で保護され、`Get()` は **JSON 経由のディープコピー**を返します。取得した `Config` を書き換えても内部状態は変わりません。
- 変更は必ず `UpdateProfile` / `UpdateServerConfig` / `UpdatePTZ` / `AddProfile` / `DeleteProfile` などのメソッド経由で行い、各メソッドが即座に `settings.json` へ永続化します。
- 設定項目を追加する場合は `types.go` の構造体タグ、`DefaultConfig()`、`README.md` の設定表、`docs/rest_api.md` を揃えて更新してください。
- `video.quality` と `ptz.speed` は型定義・UI には存在しますが、現在 `BuildFFmpegArgs` / `PTZController` では参照されていません（予約項目）。

### FFmpeg ワーカー (`supervisor`)
- プロファイル 1 つにつきワーカー goroutine 1 つ。FFmpeg が落ちたら 1 秒後に自動再起動します。
- ホットリロードは `RestartProfile(token)` で **該当プロファイルのみ**停止→再起動します。他プロファイルを止めないでください。
- 停止手順は `stopWorker`: `SIGTERM` → 短いタイムアウト → `Kill`。Windows では `SIGTERM` が効かず `Kill` にフォールバックします。
- FFmpeg 引数の変更は `BuildFFmpegArgs` に閉じ込め、`supervisor_test.go` に期待引数のアサーションを追加してください。

### RTSP (`rtsp.Server`)
- publish 元は自プロセス内 FFmpeg のみを想定しており、**ループバックからの ANNOUNCE は認証免除**です（`checkAuth`）。この例外を外部アドレスへ広げないでください。
- パスは `/live/<token>` で、`normalizePath` が `live/` プレフィックスを剥がして `streams[token]` を引きます。プロファイルの `token` と RTSP パスは常に一致させます。
- reader 数・パケット数・ビットレートは `atomic` と `GetStats()` で集計され、`/api/status` と WebSocket へ流れます。

### ONVIF (`onvif`)
- SOAP のアクション判定は `readSOAPRequest`（`SOAPAction` ヘッダ → Body 先頭要素の順）。新アクションは `handle*Service` の `switch` に追加し、`ActionNotSupported` フォールトを維持します。
- レスポンス XML は `fmt.Sprintf` によるテンプレート文字列で組み立てられ、名前空間定数は `xml_types.go` にあります。
- `GetSystemDateAndTime` と `GetCapabilities` は認証不要（ONVIF 仕様上の要件）。それ以外は `auth.CheckHTTP` を通します。
- PTZ は `PTZController` がメモリ上の仮想座標（pan/tilt: -1.0〜1.0、zoom: 0.0〜1.0）を保持し、変更時にリスナー通知と設定永続化を非同期で行います。座標は必ず `clamp` してください。

### Web / API (`web`)
- ルート登録は `APIHandler.RegisterRoutes`（REST / WS）と `onvif.Server.RegisterRoutes`（SOAP）の 2 箇所。`/` は埋め込み静的ファイル。
- エンドポイントを追加・変更したら `docs/rest_api.md` と `index.html` 側の呼び出しを同時に更新してください。
- WebSocket は PTZ 座標・ステータス・ログをサーバー push し、クライアントからの PTZ 操作を受け付けます。

### ログ
- ダッシュボードに表示したいログは `logger.Infof / Warnf / Errorf / Debugf(source, format, ...)` を使います（リングバッファ 1000 件 + WS 配信）。`source` は `"rtsp"`, `"supervisor"`, `"onvif"` などパッケージ名。
- 起動時の致命的エラーは `log.Fatalf`、それ以外の標準出力のみで良いものは `log.Printf("[pkg] ...")` という既存の使い分けに従ってください。

## 5. コーディング規約

- Go 標準の書き方に従い、**変更したファイルは `gofmt` を通す**（未変更ファイルの一括整形は別コミットにする）。
- エクスポートされた型・関数には英語の doc コメントを付ける。コード内コメントは英語、ドキュメント（`README.md`, `docs/`）は日本語。
- エラーは `fmt.Errorf("...: %w", err)` でラップし、呼び出し元にコンテキストを渡す。
- 共有状態は既存の mutex パターン（`mu.Lock()` + `defer Unlock()`、`*Locked` サフィックスの内部メソッド）を踏襲する。
- goroutine を起動する場合は `context` と `sync.WaitGroup` で必ず停止経路を用意する（`supervisor`, `discovery` が参考実装）。
- テストは `*_test.go` を同一パッケージに置き、`t.TempDir()` で設定ファイルを分離する。ネットワーク待受や FFmpeg 起動を伴うテストは書かない。
- コミットメッセージは Conventional Commits（`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`）。

## 6. 変更時に確認すること

1. `go vet ./...` と `go test ./...` が通る。
2. 設定スキーマ・API・ONVIF アクションを変更した場合、対応するドキュメント（`README.md` / `docs/`）と `index.html` を更新した。
3. ホットリロード・graceful shutdown（`main.go` の teardown 順序）を壊していない。
4. 1,000 接続以上のスケールを前提としているため、リクエスト経路やパケット転送経路にロック競合やアロケーションを増やしていない。
5. 認証免除の範囲（ループバック publish、ONVIF の一部アクション）を広げていない。

## 7. 既知の注意点

- README のバッジ・イメージ名 `your-org/mockcam` はプレースホルダーです。
- Windows ではマルチキャスト待受 (`WS-Discovery`) が失敗することがありますが、警告のみで起動は継続します。
- `internal/web/static/index.html` は CDN（Tailwind / Alpine.js）に依存するため、オフライン環境では UI のスタイルが崩れます。
