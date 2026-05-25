# Codex sidebar badge が flicker し launcher 種別と running 経過時間が安定しない

Created: 2026-05-25
Completed: 2026-05-25
Model: GPT-5

## 概要

Codex session を sidebar で監視すると、agent badge が安定して表示されない。

具体的には、dispatch 直後に launcher 種別が出ない startup gap があり、Codex の tool 実行後には `PostToolUse` hook が `idle` を書くため running badge が一瞬消える。さらに `running` hook が発火するたびに `pane_N_started` が更新されるため、表示されても running 経過時間が `0s` に戻る。

sidebar は agent の現在状態を一目で確認する control surface なので、Codex でも Claude と同様に launcher 種別、running elapsed、permission 待ちが安定して表示される必要がある。

## 根拠

`docs/spec.md` の Status バッジ仕様では以下を期待している。

- running: `🔄Ns` / `🔄Nm`
- permission / ask: `💬`
- idle: status badge 非表示
- agent kind: Claude / Codex を行末 tag で区別

現状の Codex 経路には以下の問題がある。

- dispatch 直後は Codex hook がまだ発火しておらず、sidebar に Codex tag が出るまで遅延する
- Codex は tool 実行後も応答生成中のことがあるため、`PostToolUse -> idle` は実状態より早く idle を書く
- permission 待ちは `PermissionRequest` で表現する必要があるが、doctor の必須 hook になっていない
- `hook running` が既に running の pane に対しても `pane_N_started` を上書きし、elapsed 表示がリセットされる

PR #57 はこの issue に対応する実装として、Codex hook 推奨設定、doctor の自動修復、dispatch の初期 state 書き込み、running started timestamp の保持をまとめて扱う。

## 対応方針

Codex は Claude と同じ hook event 構成として扱わず、Codex の実行ライフサイクルに合わせて state file を更新する。

- `PreToolUse`: `running --kind codex`
- `PermissionRequest`: `permission --kind codex`
- `Stop`: `idle --kind codex`
- `PostToolUse`: idle へ戻さない。既存設定に残っていれば stale として doctor が削除する

また、dispatch が新規 pane を作った時点で初期 state を書き、hook 発火前でも sidebar に launcher tag を出す。`hook running` は running 継続中なら既存の `pane_N_started` を保持し、idle / permission / ask などから running に戻った時だけ timestamp を更新する。

## 変更箇所

| ファイル | 変更内容 |
|---|---|
| `docs/setup.md` | Codex hook の推奨設定を `PreToolUse` / `PermissionRequest` / `Stop` に更新 |
| `internal/doctor/doctor.go` | stale Codex `PostToolUse` idle hook の検出・削除 |
| `internal/dispatch/dispatch.go` | dispatch 作成直後の初期 pane state 書き込み |
| `internal/hook/hook.go` | running 継続中の `pane_N_started` 保持 |
| `internal/*_test.go` | 上記挙動の回帰テスト |

## 実装チェックリスト

- [x] Codex `PostToolUse -> idle` を doctor で stale として検出
- [x] `doctor --yes` で stale hook を削除し `PermissionRequest` を追加
- [x] dispatch 直後に Codex pane tag が表示されるよう初期 state を書く
- [x] running 継続中は `pane_N_started` を保持
- [x] `go test ./...` を実行

## 解決方法

PR #57 で対応済み。

- `docs/setup.md`: Codex hook の推奨設定を `PreToolUse` / `PermissionRequest` / `Stop` に更新し、`PostToolUse` で idle に戻さない方針を明文化
- `internal/doctor/doctor.go`: Codex の stale な `PostToolUse -> idle` hook を WARN として検出し、`--yes` で削除できるようにした
- `internal/dispatch/dispatch.go`: dispatch 作成直後に初期 pane state を書き、hook 発火前でも launcher tag が表示されるようにした
- `internal/hook/hook.go`: running 継続中は既存の `pane_N_started` を保持し、elapsed が `0s` に戻らないようにした
- `internal/*_test.go`: doctor / dispatch / hook / UI の回帰テストを追加

検証として `go test ./...` を実行し、実 tmux sidebar capture でも Codex pane が `[x]🔄...` / `[x]💬` として描画されることを確認した。
