# CLAUDE.md

@AGENTS.md

上記 `AGENTS.md` がプロジェクト共通の指針です。以下は Claude Code で作業する際の補足です。

## 作業環境

- 開発マシンは Windows（PowerShell / Git Bash）。ローカルのデフォルト設定パスは `./config/settings.json` になります（`config.GetDefaultPath`）。
- ローカルで `go run ./cmd/mockcam` を実行すると FFmpeg プロセスと UDP/TCP 待受が起動します。動作確認以外では起動せず、`gofmt -l .` / `go vet ./...` / `go test ./...` / `govulncheck ./...` で検証してください。
- 音声や実ストリームの確認が必要なときは `docker build -t mockcam:dev .` → `docker run -d --name mockcam-test -p 18080:8080 -p 18554:8554 mockcam:dev` で起動し、`docker exec mockcam-test ffprobe ...` や `ffmpeg -af astats` でレベル・クリップを数値で確認する（聴感の代わりになる客観指標）。
- `syscall.SIGTERM` による FFmpeg 停止は Windows では効かず `Kill` にフォールバックします。supervisor の停止ロジックを触る際は Linux（Docker）での挙動も念頭に置いてください。

## 作業の進め方

- 変更前に該当パッケージの `*_test.go` を読み、既存のテストパターン（`t.TempDir()` + `config.NewManager`、`httptest`）に合わせて追加する。
- FFmpeg 引数を変更したら `internal/supervisor/supervisor_test.go` に CBR / VBR 双方の期待値を追加する。プロセス生成に関わる変更は `lifecycle_test.go` のフェイク FFmpeg（ヘルパープロセス）で検証する。
- 時報（`internal/timesignal`）を変更するときは、Open JTalk にサンプルレート・α・ピッチ系フラグを追加しない。音の変更は `tones_test.go`（周波数・無音区間・クリック）と `service_test.go`（スケジュール）を更新する。
- ONVIF レスポンスを変更したら `internal/onvif/onvif_test.go` に検証を追加する（既存テストは SOAP エンベロープを `httptest` 経由で取得し `strings.Contains` で要素・属性を確認する形式）。
- `gofmt` は**自分が編集したファイルだけ**に適用する。リポジトリには未整形のファイルが一部残っているため、無関係な整形差分を混ぜない。
- `README.md` / `docs/` は日本語で書く。コード内コメントと doc コメントは英語。

## 完了報告時に含めること

- 実行した検証コマンドとその結果（`go vet` / `go test` の出力要約）。
- ドキュメント更新の要否と、更新した場合はそのファイル名。
- FFmpeg や実機 VMS を使った動作確認をしていない場合はその旨を明記する。
