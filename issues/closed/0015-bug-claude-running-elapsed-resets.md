# Claude の running 経過時間が tool 実行のたびに 0s にリセットされる

Created: 2026-05-31
Completed: 2026-05-31
Model: Opus 4.8

## 概要

Claude session を sidebar で監視すると、ユーザから見て連続して作業している 1 ターンの間でも、running バッジの経過時間 (`🔄Ns`) が tool 実行のたびに `0s` に戻る。

## 根拠

Claude の推奨 hook 設定 (`docs/setup.md`) は次の通り。

| event | status |
|---|---|
| PreToolUse | `running` |
| PostToolUse | `idle` |
| Stop | `idle` |

複数 tool を順に使う 1 ターンの実際の state 遷移は

```
running(tool1開始) → idle(tool1終了) → running(tool2開始) → idle(tool2終了) → …
```

となる。`internal/hook/hook.go` の経過時間保持ロジックは

```go
if prevStatus != state.StatusRunning || !fileExists(startedPath) {
    // pane_N_started を「今」に書き直す
}
```

で、`pane_N_started` を保持するのは **直前の status が running のときだけ**。Claude は tool 間に必ず `PostToolUse → idle` を挟むため、2 個目以降の `PreToolUse → running` は直前が `idle` と判定され、毎回 `pane_N_started` が現在時刻にリセットされる。結果として `state.go` が算出する elapsed が tool ごとに `0s` に戻る。

これは issue [0013](closed/0013-bug-codex-running-badge-resets.md) で Codex について報告・修正済みの問題と同型である。Codex は `PostToolUse → idle` を設定しない方針で回避したが、Claude 側は `PostToolUse → idle` が残っているため未解決のまま残っていた。

## 対応方針

`PostToolUse → idle` を維持したまま（tool 間に idle バッジを出す挙動は変えない）、idle を挟んでも経過時間がリセットされないようにする。

- `hook running`: 既存の `pane_N_started` があれば **prevStatus に関わらず保持** し、ファイルが無いときだけ現在時刻で作成する
- running 経過時間を区切る境界は **ターン終了 (Stop)** とし、Stop hook が明示的に `pane_N_started` を削除する。次のターンの最初の running が新しい timestamp を作る
- Stop の idle と PostToolUse の idle を区別するため、`hook` サブコマンドに `--reset` フラグを追加する。Stop hook を `tmux-sidebar hook idle --reset`（Codex は `--kind codex --reset`）に変更する

### 採用しなかった代替

- **PostToolUse → idle を Claude でもやめる**（Codex と同じ方針）: tool 間の「考え中」も running 表示になり、idle バッジが出なくなる副作用がある。tool 間の idle 表示は維持したいので却下。

## 変更箇所

| ファイル | 変更内容 |
|---|---|
| `internal/hook/hook.go` | running の started を prevStatus 非依存で保持。`--reset` 時に started を削除 |
| `main.go` | `hook` サブコマンドの `--reset` フラグ解析・help 更新 |
| `internal/doctor/doctor.go` | Claude/Codex の Stop 必須 hook を `--reset` 付きに更新し、旧 `hook idle` を置換 |
| `docs/setup.md` | Stop hook の推奨設定を `--reset` 付きに更新 |
| `docs/design.md` | `pane_N_started` のライフサイクル（idle を跨いで保持・Stop で削除）を明記 |
| `docs/history.md` | started リセット契機の設計前提反転と却下案を append |
| `internal/*_test.go` | 上記挙動の回帰テスト |

## 実装チェックリスト

- [x] `hook running` が idle→running でも `pane_N_started` を保持
- [x] `hook idle --reset` が `pane_N_started` を削除
- [x] doctor が Stop hook を `--reset` 付きに upgrade（旧 `hook idle` を重複させない）
- [x] `docs/setup.md` / `docs/design.md` / `docs/history.md` を更新
- [x] `go test ./...` を実行
- [x] `/verify-implementation` で実 sidebar の表示を確認

## 解決方法

`internal/hook/hook.go` の `pane_N_started` 保持条件を「直前が running のときだけ保持」から「**ファイルが無いときだけ作成（あれば prevStatus に関わらず保持）**」へ変更し、`Options.ResetElapsed`（`--reset`）が指定されたときだけ削除するようにした。これにより 1 ターン中の `running→idle→running…` を跨いで経過時間が累積する。

- `main.go`: `hook` サブコマンドに `--reset` フラグを追加（`runHook` で解析し `Options.ResetElapsed` に渡す）
- `internal/doctor/doctor.go`: Claude/Codex の Stop 必須 hook を `tmux-sidebar hook idle --reset` / `--kind codex --reset` に更新。`upsertHookGroup` を同一 (status, kind) の旧コマンドを置換するよう改良し、旧 `hook idle` → `hook idle --reset` への upgrade で重複が出ないようにした。`hookCmdParts` ヘルパを追加。WARN 文言を `subcommand mismatch` に一般化
- `docs/setup.md`: Claude/Codex の Stop hook 推奨設定を `--reset` 付きに更新、`pane_N_started` のライフサイクルを明記
- `docs/spec.md`: running 経過時間が「1 ターンの累積」であることを明記
- `docs/design.md`: `pane_N_started` のライフサイクルと `hook --reset` を明記
- `docs/history.md`: started リセット契機の設計前提反転と却下案（Claude でも PostToolUse→idle をやめる案 / UserPromptSubmit でリセットする案）を append
- 回帰テスト: `TestWriteRunningPreservesStartedAcrossIdle`（旧 `…ResetsStartedAfterIdle` を反転）/ `TestWriteResetClearsStarted` / `TestWriteResetMissingStartedIsNoError` / `TestUpsertHookGroup_ReplacesSameIdentity` / `TestHookCmdParts`

検証として `go test ./...` を実行し、実バイナリで `running→idle→running→idle --reset→running` シーケンスを再現して保持・リセットを確認、`doctor --yes` で旧 Stop hook が `--reset` 付き 1 つに置換されることを確認した。
