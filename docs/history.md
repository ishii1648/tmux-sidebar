# tmux-sidebar 経緯

この文書は過去の実装・判断の背景を記録する。
現在のユーザ向け仕様は `docs/spec.md`、実装設計は `docs/design.md` を参照する。

---

## dotfiles における agent 俯瞰の変遷

複数の tmux session で Claude Code を並列実行する際に、
全 session の状態を常時確認できる仕組みを段階的に模索してきた。

| ADR | 手段 | 結果 |
|---|---|---|
| ADR-045 | tmux statusbar に常時表示 | 表示・操作領域コストが大きく撤廃 |
| ADR-046 | popup (`prefix+s`) に集約 | 常時表示を諦める方向へ |
| ADR-047 | Ghostty AppleScript sidebar spike | `command =` との相性問題で断念 |
| ADR-050 | Fish script + `split-window -hfb` | 常時表示は実現。操作面に限界 |
| ADR-063 | agent-pane-state 形式 | Claude Code / Codex CLI 両対応へ拡張 |

---

## ADR-050 Fish 実装

[hiroppy/tmux-agent-sidebar](https://github.com/hiroppy/tmux-agent-sidebar) の調査で
`split-window -hfb` + `after-new-window` hook という実現方式が判明した。
ADR-050 では Rust/外部依存なしに同等の仕組みを Fish script で実装した。

当時の実装は以下を行っていた。

- `split-window -hfb -l 22%` で sidebar pane を左端に作成
- 1 秒 polling で `/tmp/claude-pane-state/pane_N` を読む
- `@pane_role=sidebar` で pane を識別
- `prefix+e` で toggle
- `after-new-window` hook で各 window に自動生成

当時の状態ファイルは Claude Code 専用だった。

```
/tmp/claude-pane-state/
  pane_101
  pane_107
  pane_107_started
```

---

## Go 実装へ移行した理由

Fish + `tput cup 0 0` の passive display では、以下の体験を安定して実現しづらかった。

- キーボード選択と `Enter` による window 移動
- cursor 位置を保ったまま一覧を更新すること
- 全 session/window を表示しつつ agent 状態も重ねること
- pane focus と入力 capture の扱い

このため Bubble Tea を使った Go TUI として再実装した。

---

## `/tmp/agent-pane-state` への移行

初期設計は `/tmp/claude-pane-state` を読み、Claude Code の状態だけを扱っていた。
現在は Codex CLI も同じ sidebar に表示するため、状態 directory を
`/tmp/agent-pane-state` に変更し、`pane_N` の 2 行目に agent kind を追加した。

さらに以下の sidecar file を追加した。

- `pane_N_path`: Git/PR 表示の基準 directory
- `pane_N_session_id`: prompt preview の session key

古い unknown / legacy state は表示上 `[c]` に fallback する。

---

## 1 秒 polling をやめた理由

状態ファイルの変化は `fsnotify` で拾える。
tmux window/session の変化は hook から sidebar process に `SIGUSR1` を送れる。

そのため常時 1 秒ごとに tmux と file system を polling する必要はなくなった。
現在は以下の構成にしている。

- 状態ファイル変更: `fsnotify`
- tmux 変更: `SIGUSR1`
- running elapsed: 1 分 tick
- hook 失敗時の fallback: 10 秒 tick

---

## 幅管理の判断

`split-window -hfb -l 40` の `-l` は絶対セル数指定だが、tmux は client resize 時に
pane 幅を比例 scale する。そのため display 移動や terminal resize の後に
sidebar 幅が 40 列からずれることがある。

現在は sidebar 幅を絶対セル数として扱い、README で `client-resized` hook による
`resize-pane -x` の再適用を案内している。

`enforce-width` サブコマンドは作っていない。処理が単純な resize であり、
tmux.conf の hook だけで完結できるため。
