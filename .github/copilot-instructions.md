# GitHub Copilot 向けリポジトリ指針 — MockCam

MockCam は Go 製の仮想ネットワークカメラエミュレーターです。FFmpeg サブプロセスが生成した映像を自プロセスの RTSP サーバー（`gortsplib/v4`）へ publish し、多数のクライアントへファンアウトします。ONVIF Profile S（SOAP + WS-Discovery）と Web ダッシュボード / REST API / WebSocket も同一バイナリで提供します。詳細はリポジトリ直下の `AGENTS.md` を参照してください。

## 構成

- `cmd/mockcam/main.go`: エントリポイント。config → auth/PTZ → rtsp → supervisor → onvif/discovery → web の順に起動。
- `internal/config`: `settings.json` の型定義とデフォルト値、`Manager`（RWMutex、`Get()` はディープコピー、`Update*` は即永続化）。
- `internal/supervisor`: プロファイルごとの FFmpeg ワーカー。`BuildFFmpegArgs` が引数を組み立てる純粋関数。`WithCommandFactory` でプロセス生成を注入可能。
- `internal/rtsp`: gortsplib/v5 ベースの RTSP サーバー。パスは `/live/<token>`。ループバックからの publish は認証免除（`authorizeRequest` は純粋関数）。
- `internal/timesignal`: 117 時報。Open JTalk はネイティブ 48 kHz で合成（`-s`/`-a`/`-fm` 禁止）、880 Hz の時報音、`Runner`/`Synthesizer` で外部プロセスを抽象化。
- `internal/licenses`: サードパーティ帰属の単一情報源（`/api/licenses`、UI の情報モーダル、README）。
- `internal/onvif`: SOAP ディスパッチ（`/onvif/device_service` など）、PTZ 仮想状態機械、WS-Discovery（UDP 3702）。
- `internal/web`: HTTP サーバー、`/api/*`（`api_*.go` に分割）と `/ws`、`go:embed` された `static/index.html`（Tailwind CDN + Alpine.js、ビルド不要）。supervisor/rtsp はインターフェースで受け取る。
- `internal/auth`: Basic / Digest 認証（HTTP・RTSP 共用）。`internal/logger`: リングバッファ + WebSocket 配信ロガー。
- `docs/rest_api.md`, `docs/onvif_profile_s.md`: API / ONVIF 仕様（日本語）。

## コーディング規約

- Go 1.26、モジュール名 `mockcam`。標準ライブラリを優先し、新規依存の追加は避ける。追加した場合は `internal/licenses/licenses.go` にも登録する。
- エクスポート識別子には英語の doc コメントを付ける。コメントは英語、`README.md` / `docs/` は日本語。
- エラーは `fmt.Errorf("...: %w", err)` でラップする。
- 共有状態は `mu.Lock(); defer mu.Unlock()` と `*Locked` サフィックスの内部メソッドで扱う。goroutine には `context` と `WaitGroup` で停止経路を用意する。
- ダッシュボードに出すログは `logger.Infof(source, format, ...)` を使う（`source` はパッケージ名）。
- 設定項目を追加したら `types.go` のタグ、`DefaultConfig()`、`README.md` の設定表、`docs/rest_api.md` をすべて更新する。
- API / ONVIF アクションを追加・変更したら `docs/` と `index.html` を同時に更新する。
- PTZ 座標は pan/tilt `-1.0〜1.0`、zoom `0.0〜1.0` に必ず `clamp` する。
- 認証免除（ループバック publish、ONVIF の `GetSystemDateAndTime` / `GetCapabilities`）の範囲を広げない。
- ホットリロードは `Supervisor.RestartProfile(token)` で該当プロファイルのみ再起動する。

## テスト

- `gofmt -l .`、`go vet ./...`、`go mod tidy`（差分なし）、`govulncheck ./...`、`go test -race ./...` が CI の必須チェック。
- テストは FFmpeg やネットワーク待受なしで動くようにする（`t.TempDir()` で設定を分離、`httptest` を使用、supervisor / rtsp はフェイクまたは `nil`、外部コマンドは `Runner` / `CommandFactory` のフェイク）。
- FFmpeg 引数を変えたら `supervisor_test.go`、ONVIF レスポンスを変えたら `onvif_test.go` に検証を追加する。
- 編集したファイルのみ `gofmt` を適用する。

## コミット

- Conventional Commits（`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`）。
