# AGENTS.md — MockCam 開発ガイド（AI エージェント向け）

このファイルは、MockCam リポジトリで作業するすべての AI コーディングエージェント（Claude Code、GitHub Copilot、Codex、Cursor など）に共通する指針です。ツール固有の補足は `CLAUDE.md` および `.github/copilot-instructions.md` を参照してください。

## 1. プロジェクト概要

MockCam は VMS / NVR の開発・負荷検証用の **仮想ネットワークカメラエミュレーター** です。Go 1.26 製の単一静的バイナリで、以下を 1 プロセスで提供します。

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
  config/                  settings.json の読み書き・型定義・デフォルト値 (types.go / config.go)、入力検証 (validate.go)
  auth/                    Basic / Digest 認証（HTTP と RTSP で共用、realm="MockCam"）
  logger/                  リングバッファ付き構造化ロガー。Web UI へ WebSocket でライブ配信される
  supervisor/              プロファイルごとの FFmpeg ワーカー管理（起動・自動再起動・ホットリロード）
    ffmpeg_cmd.go          設定 → FFmpeg 引数列を組み立てる純粋関数 BuildFFmpegArgs
    supervisor.go          CommandFactory を注入可能（テストはフェイクプロセスで実行）
  rtsp/                    gortsplib/v5 ベースの RTSP サーバー（server.go）、認証の純粋関数（auth.go）、統計（stats.go）
  onvif/                   SOAP ディスパッチ (server.go)、Device/Media ハンドラ、PTZ 状態機械 (ptz.go)、WS-Discovery
  timesignal/              117 時報の音声生成（phrase / tones / pcm / tts / service に分割、Runner・Synthesizer で外部プロセスを抽象化）
  frames/                  FFmpeg の MJPEG サイド出力を JPEG に分割（Splitter）し最新フレームを保持（Store）
  camera/                  アプリケーションコア Controller。REST / WebSocket / MCP が共有する業務ロジックと合成プレビュー
  mcpserver/               Model Context Protocol サーバー（公式 go-sdk）。ツール/リソースは Controller の薄いラッパー
  licenses/                サードパーティ帰属表示の単一情報源（/api/licenses と README に反映）
  web/                     HTTP サーバー (server.go)、REST/WS ハンドラ (api_*.go)、OpenAPI/Scalar (api_docs.go)、埋め込み UI (static/index.html)
docs/                      openapi.yaml（埋め込まれ /openapi.yaml で配信）、REST API 仕様・ONVIF Profile S 仕様（日本語）
.agents/skills/            ワークスペーススキル（リリース手順、FFmpeg パイプライン、Web UI i18n）
.github/workflows/ci.yml   gofmt → go vet → go mod tidy → govulncheck → go test -race → Docker multi-arch ビルド & ghcr.io push
Dockerfile / compose.yml   alpine 3.22 + ffmpeg + Open JTalk + espeak-ng ランタイム
```

- Go モジュール名は `mockcam`（`import "mockcam/internal/..."`）。
- 外部依存は `gortsplib/v5`、`pion/rtp`・`pion/rtcp`、`gorilla/websocket`、`google/uuid`、`modelcontextprotocol/go-sdk`、`go.yaml.in/yaml/v3`（テストのみ）です。**新しい依存を追加する前に標準ライブラリで代替できないか検討**し、追加した場合は `internal/licenses/licenses.go` にも登録してください（`licenses_test.go` が go.mod と突き合わせます）。
- フロントエンドは `internal/web/static/index.html` 1 ファイル（Tailwind CDN + Alpine.js）。ビルドステップは無く、`go:embed` でバイナリに同梱されます。

## 3. 開発コマンド

```bash
go mod download
go build -o mockcam ./cmd/mockcam        # ビルド
go run ./cmd/mockcam -config ./config/settings.json   # ローカル起動（FFmpeg が PATH に必要）
gofmt -l .                               # 未整形ファイルの一覧（CI では出力があると失敗）
go vet ./...                             # CI と同じ静的検査
go mod tidy                              # CI では go.mod/go.sum に差分が出ると失敗
govulncheck ./...                        # 脆弱性スキャン（go install golang.org/x/vuln/cmd/govulncheck@latest）
go test -race -count=1 ./...             # 単体テスト（FFmpeg 不要。Windows で -race が使えない場合は外す）
docker compose up -d --build             # コンテナ起動
```

- **テストは FFmpeg 無しで動く**よう設計されています。各層は依存を注入できる形になっており、この性質を壊さないでください。
  - `supervisor`: `WithCommandFactory` でプロセス生成を差し替え（テストはテストバイナリ自身をフェイク FFmpeg として再実行）。
  - `timesignal`: `Runner`（外部コマンド）と `Synthesizer`（TTS）をインターフェース化。`NewServiceWith(synth, clock)` で時刻も注入可能。
  - `web`: `StreamSupervisor` / `StreamServer` / `PTZ` インターフェースを受け取る。`newAPIHandler` でロガー・時報サービスも差し替え可能。
  - `rtsp`: `authorizeRequest` は純粋関数。`CredentialValidator` インターフェースで認証器を差し替え。
  - `onvif`: `BuildProbeMatches` / `IsProbe` / `ProbeMessageID` は純粋関数。SOAP は `httptest` で検証。
  - `camera`: フェイクの supervisor / rtsp / frames を渡して Controller の業務ロジックを直接テスト。
  - `mcpserver`: `mcp.NewInMemoryTransports` でクライアントを接続しツール/リソースを検証（`httptest` + `StreamableClientTransport` で HTTP も）。
- 設定ファイルの既定パス: Linux は `/config/settings.json`、Windows は `./config/settings.json`、環境変数 `CONFIG_PATH` または `-config` フラグで上書き。存在しなければデフォルト設定が自動生成されます。
- 既定の認証情報は `admin` / `admin1234`（`config.DefaultConfig()`）。

## 4. アーキテクチャ上の重要な決まりごと

### 設定 (`config.Manager`)
- `Manager` は `sync.RWMutex` で保護され、`Get()` は **JSON 経由のディープコピー**を返します。取得した `Config` を書き換えても内部状態は変わりません。
- 変更は必ず `UpdateProfile` / `UpdateServerConfig` / `UpdatePTZ` / `AddProfile` / `DeleteProfile` などのメソッド経由で行い、各メソッドが即座に `settings.json` へ永続化します。
- 設定項目を追加する場合は `types.go` の構造体タグ、`DefaultConfig()`、`README.md` の設定表、`docs/rest_api.md` を揃えて更新してください。
- `video.quality` と `ptz.speed` は型定義・UI には存在しますが、現在 `BuildFFmpegArgs` / `PTZController` では参照されていません（予約項目）。
- 設定ファイル読み込み時に `FirmwareVersion` は `config.AppVersion` に同期されます。リリース時は `.agents/skills/mockcam-release-and-verify/SKILL.md` のチェックリストに従ってください。

### FFmpeg ワーカー (`supervisor`)
- `WithFrameSink` を渡すと FFmpeg の 2 番目の出力（`-map 0:v:0 -an ... -f mjpeg pipe:1`、約 5 fps・幅 1280 px 上限）が stdout に流れ、`frames.Splitter` が JPEG 単位に分割して Store へ publish します。`BuildFFmpegArgsWith` の `PreviewOptions` で制御。
- プロファイル 1 つにつきワーカー goroutine 1 つ。FFmpeg が落ちたら `restartDelay`（既定 1 秒）後に自動再起動し、`Status()` の `Restarts` が増えます。
- ホットリロードは `RestartProfile(token)` で **該当プロファイルのみ**停止→再起動します。他プロファイルを止めないでください。
- 停止手順は `stopWorker`: `SIGTERM` → `stopGrace` → `Kill` → `killGrace`。Windows では `SIGTERM` が効かず `Kill` にフォールバックします。
- FFmpeg 引数の変更は `BuildFFmpegArgs` に閉じ込め、`supervisor_test.go` に期待引数のアサーションを追加してください。時報 PCM のサンプルレートは `timesignal.SampleRate` を参照し、数値をハードコードしないでください。
- プロセス生命周期のテストは `lifecycle_test.go` のヘルパープロセスパターンを踏襲してください。

### 117 時報 (`timesignal`)
- Open JTalk は **モデルのネイティブ 48 kHz** で合成します。`-s` / `-a` / `-fm` を渡すとフォルマントが歪み不気味な声になるため、`OpenJTalk.Args` に追加しないでください（テストで禁止フラグを検査しています）。
- 時報音は 880 Hz に統一（`:07 :08 :09` ピップ、`:00` マーク）。エンベロープはレイズドコサインでクリック音を出さないこと。`tones_test.go` が周波数・無音区間・クリックを検証します。
- スケジュールは `AnnouncementFor(now)` が決めます。`Streamer.Chunk(ctx, now, dst)` は時刻を引数に取る決定的な関数なので、テストでは固定時刻を渡してください。
- WAV は必ず `ParseWAV` でチャンクを走査して読むこと（先頭 44 バイト決め打ちは LIST/fact チャンクで壊れます）。

### RTSP (`rtsp.Server`)
- gortsplib は **v5** を使用します（v4 は deprecated スタブ）。`ServerStream` は `&gortsplib.ServerStream{Server, Desc}` + `Initialize()` で生成します。
- publish 元は自プロセス内 FFmpeg のみを想定しており、**ループバックからの ANNOUNCE は認証免除**です（`authorizeRequest`）。この例外を外部アドレスへ広げないでください。
- publisher セッションが切れたらそのストリームを閉じ、reader に再接続させます（`OnSessionClose`）。
- パスは `/live/<token>` で、`normalizePath` が `live/` プレフィックスを剥がして `streams[token]` を引きます。プロファイルの `token` と RTSP パスは常に一致させます。
- reader 数・パケット数・ビットレートは `atomic` と `GetStats()` で集計され、`/api/status` と WebSocket へ流れます。

### ONVIF (`onvif`)
- SOAP のアクション判定は `readSOAPRequest`（`SOAPAction` ヘッダ → Body 先頭要素の順）。新アクションは `handle*Service` の `switch` に追加し、`ActionNotSupported` フォールトを維持します。
- レスポンス XML は `fmt.Sprintf` によるテンプレート文字列で組み立てられ、名前空間定数は `xml_types.go` にあります。
- `GetSystemDateAndTime` と `GetCapabilities` は認証不要（ONVIF 仕様上の要件）。それ以外は `auth.CheckHTTP` を通します。
- PTZ は `PTZController` がメモリ上の仮想座標（pan/tilt: -1.0〜1.0、zoom: 0.0〜1.0）を保持し、変更時にリスナー通知と設定永続化を非同期で行います。座標は必ず `clamp` してください。

### アプリケーションコア (`camera.Controller`)
- 業務ロジック（検証 → 保存 → ストリーム閉塞 → ワーカー再起動、フォールバックなど）は **必ず `camera.Controller` に置き**、Web ハンドラと MCP ツールはそれを呼ぶだけの薄いアダプタにしてください。両者の挙動がずれるのを防ぎます。
- 失敗は `camera.ErrInvalid` / `ErrNotFound` / `ErrConflict` でラップし、Web は `writeCoreError`（400/404/409）、MCP は `toolErr`（isError 結果）に変換します。

### Web / API (`web`)
- ルート登録は `APIHandler.RegisterRoutes`（REST / WS / OpenAPI）と `onvif.Server.RegisterRoutes`（SOAP）、`Server.Mount`（MCP など追加ハンドラ）の 3 箇所。`/` は埋め込み静的ファイル。ハンドラは `api_status.go` / `api_profiles.go` / `api_ptz.go` / `api_media.go` / `api_ws.go` / `api_docs.go` に分かれています。
- JSON ボディは `readJSON`（1 MiB 上限・未知フィールド拒否）で読み、書き込みは `writeJSON` / `writeError` を使います。検証は Controller 側で行われます。
- エンドポイントを追加・変更したら **`docs/openapi.yaml`**（`api_docs_test.go` がルートとの整合を検査）、`docs/rest_api.md`、`web_test.go`、`index.html` 側の呼び出しを同時に更新してください。
- スナップショット系: `/api/snapshot/{token}` と `/api/mjpeg/{token}` は `frames.Store` のライブフレームを優先し、無ければ合成プレビューにフォールバックします（`X-MockCam-Source` ヘッダー）。フレームは supervisor の `WithFrameSink` 経由で届きます。

### MCP (`mcpserver`)
- ツールを追加するときは `registerTools` に `mcp.AddTool`（型付き入力/出力）で登録し、`Annotations`（readOnly / idempotent / destructive）を必ず付けてください。`server_test.go` のツール一覧テストにも追加します。
- 画像を返すツールは `mcp.ImageContent{Data, MIMEType}` を使います（`get_snapshot` を参照）。
- `-mcp-stdio` では stdout が MCP トランスポートになるため、ログは `log.SetOutput(os.Stderr)` 済み。stdout に書く処理を追加しないでください。
- 依存ライブラリやランタイムツールを追加したら `internal/licenses/licenses.go` に帰属情報を追加し、README のライセンス表も更新してください（UI の「ℹ️ 情報」モーダルは `/api/licenses` を表示します）。
- WebSocket は PTZ 座標・ステータス・ログをサーバー push し、クライアントからの PTZ 操作を受け付けます。

### ログ
- ダッシュボードに表示したいログは `logger.Infof / Warnf / Errorf / Debugf(source, format, ...)` を使います（リングバッファ 1000 件 + WS 配信）。`source` は `"rtsp"`, `"supervisor"`, `"onvif"` などパッケージ名。
- 起動時の致命的エラーは `log.Fatalf`、それ以外の標準出力のみで良いものは `log.Printf("[pkg] ...")` という既存の使い分けに従ってください。

## 5. コーディング規約

- Go 標準の書き方に従い、**変更したファイルは `gofmt` を通す**（未変更ファイルの一括整形は別コミットにする）。
- エクスポートされた型・関数には英語の doc コメントを付ける。コード内コメントは英語。`README.md` は英語、`README.jp.md` は日本語で**両方を同期して更新**する。`docs/` の Markdown は日本語、`docs/openapi.yaml` は英語。
- エラーは `fmt.Errorf("...: %w", err)` でラップし、呼び出し元にコンテキストを渡す。
- 共有状態は既存の mutex パターン（`mu.Lock()` + `defer Unlock()`、`*Locked` サフィックスの内部メソッド）を踏襲する。
- goroutine を起動する場合は `context` と `sync.WaitGroup` で必ず停止経路を用意する（`supervisor`, `discovery` が参考実装）。
- テストは `*_test.go` を同一パッケージに置き、`t.TempDir()` で設定ファイルを分離する。ネットワーク待受や FFmpeg 起動を伴うテストは書かない。
- コミットメッセージは Conventional Commits（`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`）。

## 6. 変更時に確認すること

1. `gofmt -l .` が空、`go vet ./...`、`go mod tidy`（差分なし）、`govulncheck ./...`、`go test -race ./...` が通る。
2. 設定スキーマ・API・ONVIF アクションを変更した場合、対応するドキュメント（`README.md` / `docs/`）と `index.html` を更新した。
3. ホットリロード・graceful shutdown（`main.go` の teardown 順序）を壊していない。
4. 1,000 接続以上のスケールを前提としているため、リクエスト経路やパケット転送経路にロック競合やアロケーションを増やしていない。
5. 認証免除の範囲（ループバック publish、ONVIF の一部アクション）を広げていない。
6. 依存追加・更新時は `internal/licenses` と README のライセンス表を更新した。

## 7. 既知の注意点

- README のバッジ・イメージ名 `your-org/mockcam` はプレースホルダーです。
- Windows ではマルチキャスト待受 (`WS-Discovery`) が失敗することがありますが、警告のみで起動は継続します。
- Windows のローカル環境では `go test -race` が MinGW gcc の警告で失敗することがあります。その場合は `MSYS_NO_PATHCONV=1 docker run --rm -v "C:\path\to\mockcam:/src" -w /src -e GOFLAGS=-buildvcs=false golang:1.26 go test -race ./...` で Linux 上の race 検査を行ってから push してください。
- `internal/web/static/index.html` は CDN（Tailwind / Alpine.js）に依存するため、オフライン環境では UI のスタイルが崩れます。
