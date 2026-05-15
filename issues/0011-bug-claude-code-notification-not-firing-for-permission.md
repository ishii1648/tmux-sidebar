# Claude Code 2.1.142 で permission UI 表示時に Notification hook が発火せず `💬` が出ない

Created: 2026-05-15
Model: Opus 4.7

## 概要

`docs/spec.md:182-186` は permission/ask 状態のバッジを `💬` で表示すると規定している。これを成立させる前提は、Claude Code が permission UI を出した瞬間に `Notification` hook event (matcher `permission_prompt` / `elicitation_dialog`) を発火し、ユーザの hook が `/tmp/agent-pane-state/pane_N` の 1 行目を `permission` / `ask` に書き換えることである。

実機 (Claude Code 2.1.142, macOS 14, tmux 3.6a) で検証したところ、**permission UI 表示中・応答時・応答後のいずれのフェーズでも当該セッションからの `Notification` event が一切発火していない**。結果として `pane_N` は `running` のまま固定され、サイドバーは `💬` ではなく `🔄` (running) を表示し続ける。これは `spec.md` の仕様違反である。

## 根拠

### 実機検証

`~/.claude/settings.json` の `Notification` に **matcher 空 (全マッチ)** の trace hook を仕込み、stdin の JSON を `/tmp/notify-trace.log` に追記する構成で検証:

```json
"Notification": [
  { "hooks": [ { "type": "command", "command": "/bin/bash /tmp/notify-trace.sh" } ] },
  { "matcher": "permission_prompt", "hooks": [ ... "tmux-sidebar hook permission" ] },
  { "matcher": "elicitation_dialog", "hooks": [ ... "tmux-sidebar hook ask" ] }
]
```

`tmux new-session -d -s verify-perm` で新規セッション (session_id=`fdd3afaa-f43c-4f86-9f6f-a24ca9a77155`) を作成し、`claude` 起動 → `use bash to run: go install ./testpkg` (= `Bash(go install *)` は `~/.claude/settings.json` の `ask` リストに該当) を `tmux send-keys` 経由で投入:

| タイミング | 当該セッションの Notification 発火 |
|---|---|
| permission UI 表示中 (12 秒待機) | なし |
| Yes 応答 → tool 実行 | なし |
| tool 完了 → Stop | なし |

`/tmp/notify-trace.log` に書かれていた 2 件は **別セッション (`session_id=611e706d-..., cwd=sre-ai-agent`) からの `notification_type=idle_prompt`** のみで、当該セッションからの記録はゼロ。`pane_95` (verify-perm の main pane) は終始 `running\nclaude\n` で、Esc 応答後の Stop hook でようやく `idle\nclaude\n` に遷移した。

→ Notification event 機構自体は動作する (`idle_prompt` は捕捉できる) が、**permission UI 表示時には何も発火していない**。

### 公式ドキュメントとの乖離

- `https://code.claude.com/docs/en/hooks.md` の hooks reference は `matcher: "permission_prompt"` の例を載せている
- `claude-code-guide` subagent の docs ベース回答も「`Notification` + `matcher: permission_prompt` で permission UI 表示時に発火する」と説明
- `CHANGELOG.md` (`gh api repos/anthropics/claude-code/contents/CHANGELOG.md`) 全体 (2.0.37〜2.1.142) を確認したが、**permission_prompt が発火しなくなった旨の記述はない**
  - 2.0.37: 「Hooks: Added matcher values for Notification hook events」(matcher 機能追加)
  - 2.0.37: 「Fixed how idleness is computed for notifications」
  - 2.1.139 (5/11): 「hooks now run without terminal access」(関連可能性あり)
  - 2.1.141 (5/13): 「Added `terminalSequence` field to hook JSON output」

= docs と実装の乖離は確定。Claude Code 側のリグレッションまたは未文書化の挙動変更の可能性が濃厚。

### 経緯から見える「最近バグり出した」の整合

- ユーザの `~/.claude/scripts/agent-pane-state.sh` は 2026-04-29 (ADR-063 移行) 以降ほぼ不変
- ユーザの `~/.claude/settings.json` の `hooks` セクションも dotfiles 上は 2026-05-05 が最後の更新で hook 配線は不変
- 一方 Claude Code は同期間で 2.1.139〜2.1.142 のリリースがあり、hook 周りに複数の変更が入っている
- → ユーザの「今まで動いていた」は dotfiles 側の hook 設定 (`Notification permission_prompt → claude-pane-state.sh permission`) が以前は実際に発火していた、と整合する

## 対応方針

| 案 | 内容 | メリット | デメリット |
|---|---|---|---|
| A. 切り分けテストを増やす | `default` モード (acceptEdits 解除)、`PermissionRequest` hook、別ツール (Edit / WebFetch) など条件を変えて Notification 発火パターンを確定させる | 真の発火条件が分かれば matcher を合わせるだけで済む可能性 | Claude Code 側バグなら徒労 |
| B. Claude Code に bug 報告 | `anthropics/claude-code` issues に実機ログ付きで投げ、修正を待つ | 根本的な解決 | リードタイム不明、sidebar 側は何もできない期間が続く |
| C. transcript / supervisor state 経由で permission 検出 | `~/.claude/projects/<id>.jsonl` の最新エントリや `~/.claude/jobs/<id>/state.json` (#0009) に permission 状態が出ているかを reader で検出し、`/tmp/agent-pane-state/` の writer に流す | hook が発火しなくても sidebar 側だけで対処可能 | 公式 state.json はリサーチプレビュー、スキーマ未保証。Codex 側との対称性が崩れる |
| D. spec.md を現実に合わせる | 「permission/ask は Claude Code 側 event 未提供のため当面未実装」と仕様を後退させる | 矛盾は解消する | サイドバーの機能差別化が一つ失われる、ユーザ要望と逆行 |

複合案: **A で切り分け → 結果次第で B (Claude Code 側) と C (sidebar 側 fallback) を並行**、が現実的。

## 変更箇所

本 issue 単体では実装変更なし。対応方針確定後に以下が候補:

| パッケージ / ファイル | 変更内容 (案 C を採る場合) |
|---|---|
| `internal/state/` または新規 `internal/transcript/` | Claude 公式 transcript / state.json を読み、permission/ask 検出して `state.PaneState` を補強 |
| `internal/hook/hook.go` | 単一 writer 前提を崩さないよう、補助 writer の合流ポイントを設計 |
| `docs/design.md` | 状態取得経路の二重化を設計に追記 |
| `docs/history.md` | 「Claude Code 側で Notification が発火しなくなったため fallback 経路を導入」と方向反転を記録 |

## 実装チェックリスト

- [ ] 切り分けテスト (A)
  - [ ] `default` モードで `Bash(go install)` 系 → permission UI 出して trace 再現
  - [ ] `Edit` / `Write` ツールで `defaultMode` を default にした状態 → permission 発火するか
  - [ ] `PermissionRequest` hook event 名で trace を仕込んでも来るか
  - [ ] 別マシン (Linux など) で同じ Claude Code バージョンを再現できるか
- [ ] (A の結果次第) Claude Code issue 報告 (B) — 再現手順 + trace log + spec への影響まで添える
- [ ] (A で発火条件不明 / 修正待ち) sidebar 側 fallback 経路設計 (C) — `#0009` と並行検討
- [ ] spec.md の挙動更新 (該当する場合のみ)
- [ ] history.md に方向反転として追記 (該当する場合のみ)
