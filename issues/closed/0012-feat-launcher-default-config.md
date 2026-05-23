# popup picker の初期 launcher を config で指定できるようにする

Created: 2026-05-23
Completed: 2026-05-23
Model: Opus 4.7

## 概要

`tmux-sidebar new` の popup picker は起動時の launcher が常に `claude` 固定で、`codex` を主に使うユーザは毎回 `Tab` を 1 回押して切り替える必要があった。`~/.config/tmux-sidebar/launcher` ファイルと `TMUX_SIDEBAR_LAUNCHER` 環境変数で初期値を指定できるようにする。`Tab` での実行時切替は従来どおり残す。

## 根拠

- `internal/picker/picker.go` の `New` が無条件に `m.launcher = dispatch.LauncherClaude` を入れていた
- 他の per-machine 設定（`hidden_sessions` / `pinned_sessions` / `width`）はすべて `~/.config/tmux-sidebar/` 配下にあるので、launcher も同じ場所に置くのが自然
- ワンショットで上書きしたいケースのため env var も用意（`TMUX_SIDEBAR_WIDTH` と同じ規約）

## 対応方針

- `config.LoadLauncher()` を追加: env → file → "" の順で解決。値は `claude` / `codex` に正規化（大小無視・前後 trim）、不正値は `""` を返す
- 既存の `loadWidth()` と同じ二段（env > file）スタイルに揃える
- picker 側は `(*Model).SetDefaultLauncher(name)` を生やし、`main.go` の `runNew` で `picker.New` 直後に呼ぶ。空文字・不正値はメソッド内で握り潰して既定（claude）を維持
- 設定一元化（TOML 等）は検討したが、list と scalar が混在する現状で破壊的変更に見合わないため見送り

却下案:
- `picker.New` のシグネチャに default launcher 引数を追加 — 22 箇所のテスト call site が壊れるためコスト過大
- picker 内で直接 `config.LoadLauncher()` を呼ぶ — テストがユーザの実 config を読んでしまうので不可

## 解決方法

- `internal/config/config.go`: `LauncherConfigPath()` / `LoadLauncher()` / `normalizeLauncher()` を追加
- `internal/picker/picker.go`: `(*Model).SetDefaultLauncher` を追加（不正値は無視）
- `main.go`: `runNew` で `model.SetDefaultLauncher(config.LoadLauncher())`
- `docs/spec.md`: Configuration files 表と Environment variables 表に launcher を追記
- `docs/setup.md`: `launcher` の節を追加し env var でのワンショット上書き例も併記
- テスト: env vs file 優先順位 / 正規化 / 不正値 / 未設定の 4 ケースと、picker 側の `SetDefaultLauncher` テーブルテスト
