# tmux-sidebar 設計ドキュメント

## 概要

tmux のサイドバーペインに全セッション・ウィンドウの一覧と Claude Code の状態を表示し、
キーボード選択で対象ウィンドウへ即座に移動できる Go 製 TUI ツール。

`split-window -hfb` で左端に常駐させ、tmux の `after-new-window` フックで各ウィンドウに自動生成する。

---

## 背景・経緯

### dotfiles における「Claude セッション俯瞰」の変遷

複数の tmux セッションで Claude Code を並列実行する際に、
全セッションの状態を常時確認できる仕組みを段階的に模索してきた。

| ADR | 手段 | 結果 |
|-----|------|------|
| ADR-045 | tmux statusbar に常時表示 | 表示・操作領域コストが大きく撤廃 |
| ADR-046 | popup（`prefix+s`）に集約 | 常時表示を諦める方向へ |
| ADR-047 | Ghostty AppleScript サイドバー Spike | `command =` との相性問題で断念 |
| ADR-050 | Fish スクリプト + `split-window -hfb` | 常時表示は実現。後述の限界あり |

### ADR-050（Fish 実装）で実現できたこと

[hiroppy/tmux-agent-sidebar](https://github.com/hiroppy/tmux-agent-sidebar) の調査で
`split-window -hfb` + `after-new-window` フックという実現方式が判明した。
ADR-050 では Rust/外部依存なしに同等の仕組みを Fish スクリプトで実装した。

- `split-window -hfb -l 22% -t {leftmost_pane}` でサイドバーペインを左端に作成
- 1 秒ポーリングで `/tmp/claude-pane-state/pane_N` を読み、Claude Code の状態を表示
- `@pane_role = "sidebar"` でペインを識別し `prefix+e` で toggle
- `after-new-window` フックで各ウィンドウに自動生成

**状態ファイル（ADR-007 の仕組み）:**

```
/tmp/claude-pane-state/
  pane_101        # "idle" | "running" | "permission" | "ask"
  pane_107        # "running"
  pane_107_started  # running 開始時刻（epoch）
```

Claude Code の hook（`claude-pane-state.sh`）が各ペインの状態をこのファイルに書き出す。
sidebar はこのファイルを読んで状態を表示するだけであり、hook 側の変更は不要。

### ADR-050 の限界（Go 実装への移行理由）

Fish + `tput cup 0 0` の passive display では以下の機能を実現できない：

| 要件 | Fish での実現可否 | 理由 |
|------|-----------------|------|
| 通常のペイン移動対象から除外 | × | tmux にネイティブ機能なし |
| キーボード選択 + Enter で移動 | × | fzf reload でカーソルリセット問題 |
| 全 session+window 表示（Claude 以外も含む） | ○ | 実装可能だが上記と組み合わせ困難 |

インタラクティブな TUI（カーソル移動・Enter 確定・ペイン入力キャプチャ）は
Go の TUI ライブラリ（[bubbletea](https://github.com/charmbracelet/bubbletea) 等）で実装するのが適切と判断した。

---

## 要件

### 機能要件

#### 1. 全 session + window の表示

- すべての tmux セッション・ウィンドウを階層表示する（Claude Code がいないウィンドウも含む）
- Claude Code が存在するウィンドウには状態バッジを付ける（`running` / `idle` / `permission` / `ask`）
- 状態は `/tmp/claude-pane-state/pane_N` から取得する（ADR-007 の仕組みを継続利用）
- 1 秒ポーリングで自動更新

**表示イメージ:**

```
 Sessions
 ─────────────────────────
 ishii1648_dotfiles
   1: main
 ▶ 2: 2.1.101        [running 3m]
 manaflow-ai_cmux
   1: 2.1.97          [idle]
 affaan-m_everything
   1: main
   2: fish
```

#### 2. キーボード操作

| キー | 動作 |
|------|------|
| `j` / `↓` | 次の行へ移動 |
| `k` / `↑` | 前の行へ移動 |
| `Enter` | 選択ウィンドウへ `switch-client` + `select-window` |
| `q` / `Esc` | サイドバーから操作を抜ける（passive display モードに戻る） |

#### 3. 通常ペイン移動からの除外

- `prefix+hjkl` や `prefix+矢印` などの標準ペイン移動でサイドバーがフォーカスされない
- サイドバーへのフォーカスは専用キー（`prefix+e` 等）のみで行う

#### 4. toggle（表示 / 非表示）

- `prefix+e` でサイドバーを表示・非表示切り替え
- 非表示時は kill-pane、表示時は再 split-window

#### 5. 自動生成

- tmux の `after-new-window` フックで各ウィンドウにサイドバーを自動生成
- 起動コマンド: `tmux-sidebar` バイナリを直接 split-window で起動

### 非機能要件

- **依存関係**: バイナリ単体で動作すること。追加の tmux プラグインや外部ツールは不要
- **インストール**: aqua（`aqua.yaml`）でバージョン管理できるよう GitHub Releases にバイナリを公開する
- **パフォーマンス**: 1 秒ポーリングで CPU 使用率が無視できる程度であること
- **macOS 優先**: Linux でも動作することが望ましいが必須ではない

---

## アーキテクチャ

### dotfiles との分担

| 管理場所 | 内容 |
|---------|------|
| このリポジトリ（`ishii1648/tmux-sidebar`） | Go 実装・リリースバイナリ |
| dotfiles `aqua.yaml` | バイナリバージョン管理 |
| dotfiles `configs/fish/functions/` | Fish wrapper（`claude-sidebar-create/toggle`） |
| dotfiles `configs/tmux/tmux.conf` | `prefix+e` keybind・`after-new-window` フック |
| dotfiles `configs/claude/scripts/claude-pane-state.sh` | 状態ファイル書き出し（変更不要） |

### 状態ファイル仕様（ADR-007 継続）

```
/tmp/claude-pane-state/pane_{TMUX_PANE_NUMBER}
```

- 値: `running` | `idle` | `permission` | `ask`
- `pane_{N}_started`: running 開始時刻（epoch）。経過分数の表示に使用

### 参考実装

- [hiroppy/tmux-agent-sidebar](https://github.com/hiroppy/tmux-agent-sidebar) — Rust (Ratatui) による同等実装。ペイン除外・Enter 選択・全セッション表示の3機能すべてを満たす。本プロジェクトはこれを Go で再実装したもの。

---

## 未決定事項

- [ ] Go TUI ライブラリの選定（[bubbletea](https://github.com/charmbracelet/bubbletea) vs [tview](https://github.com/rivo/tview) vs 自前）
- [ ] 「通常ペイン移動からの除外」の実装方式（`after-select-pane` hook vs TUI 内でのキャプチャ）
- [ ] passive display（表示のみ）と interactive モードの切り替え方式
- [ ] `cmd+s` でのフォーカス対応（Ghostty キーマップ設定が必要）
