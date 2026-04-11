# tmux-sidebar

全 tmux セッション・ウィンドウの一覧と Claude Code の状態をサイドバーペインに表示し、キーボード選択で対象ウィンドウへ即座に移動できる TUI ツール。

```
┌──────────────────────┬──────────────────────────────────────────┐
│ Sessions             │                                          │
│ ─────────────────────│                                          │
│ ishii1648_dotfiles   │         作業ペイン                        │
│   1: nvim  [idle]    │                                          │
│ ishii1648_work       │                                          │
│ ▶ 1: main  [running] │                                          │
│   2: fish            │                                          │
│ infra                │                                          │
│   1: deploy          │                                          │
└──────────────────────┴──────────────────────────────────────────┘
```

## Features

- 全セッション・ウィンドウを階層表示（Claude Code がいないウィンドウも含む）
- Claude Code の状態バッジ: `[running Nm]` / `[idle]` / `[permission]` / `[ask]`
- `j`/`k` でカーソル移動、`Enter` で対象ウィンドウへジャンプ
- `q` で passive モード（サイドバーを表示したままキー入力を通過させる）
- `i` で interactive モードに復帰
- `after-new-window` フックで新しいウィンドウに自動生成

## Installation

### aqua（推奨）

```yaml
# aqua.yaml
packages:
  - name: ishii1648/tmux-sidebar@v0.1.0
```

```sh
aqua install
```

### go install

```sh
go install github.com/ishii1648/tmux-sidebar@latest
```

### バイナリダウンロード

[Releases](https://github.com/ishii1648/tmux-sidebar/releases) から OS/アーキテクチャに合ったアーカイブをダウンロードして `$PATH` の通った場所に置く。

## Setup

### 1. tmux.conf

```tmux
# 新しいウィンドウを作るたびにサイドバーを自動生成
set-hook -g after-new-window \
  'run-shell "tmux split-window -hfb -l 35 -e @pane_role=sidebar tmux-sidebar"'

# サイドバーへの誤フォーカスを防ぐ（通常の prefix+hjkl でスキップされる）
set-hook -g after-select-pane \
  'run-shell "tmux-sidebar focus-guard"'
```

> `@pane_role=sidebar` を付けることで `focus-guard` がサイドバーペインを識別できます。

### 2. toggle キーバインド（任意）

```tmux
# prefix+e でサイドバーを表示・非表示切り替え
bind-key e run-shell '\
  if tmux list-panes -F "#{@pane_role}" | grep -q "^sidebar$"; then \
    tmux kill-pane -t $(tmux list-panes -F "#{pane_id} #{@pane_role}" | awk '"'"'/sidebar/{print $1}'"'"'); \
  else \
    tmux split-window -hfb -l 35 -e @pane_role=sidebar tmux-sidebar; \
  fi'
```

### 3. Claude Code の状態ファイル（任意）

状態バッジを表示するには Claude Code の hook が `/tmp/claude-pane-state/` に状態ファイルを書き出す必要があります。

`.claude/settings.json` の hooks に以下を追加してください：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "claude-pane-state.sh running" }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "claude-pane-state.sh idle" }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "claude-pane-state.sh idle" }
        ]
      }
    ]
  }
}
```

`claude-pane-state.sh` の例：

```sh
#!/bin/sh
# $TMUX_PANE の数値部分を使って状態ファイルを書く
num=$(echo "$TMUX_PANE" | tr -d '%')
dir=/tmp/claude-pane-state
mkdir -p "$dir"
echo "$1" > "$dir/pane_${num}"
if [ "$1" = "running" ]; then
  date +%s > "$dir/pane_${num}_started"
fi
```

## Keyboard shortcuts

| キー | 動作 |
|------|------|
| `j` / `↓` | 次のウィンドウ行へ |
| `k` / `↑` | 前のウィンドウ行へ |
| `Enter` | 選択ウィンドウへ移動 |
| `q` / `Esc` | passive モード（キー入力をペインに通過させる） |
| `i` | interactive モードに復帰 |
| `Ctrl+C` | 終了 |

## State badges

| バッジ | 意味 |
|--------|------|
| `[running Nm]` | Claude Code が実行中（N 分経過） |
| `[idle]` | Claude Code がアイドル状態 |
| `[permission]` | 権限確認待ち |
| `[ask]` | ユーザーへの確認待ち |

## Environment variables

| 変数 | 説明 |
|------|------|
| `TMUX_SIDEBAR_STATE_DIR` | 状態ファイルのディレクトリ（デフォルト: `/tmp/claude-pane-state`） |
| `TMUX_SIDEBAR_NO_ALT_SCREEN` | 設定するとオルタネートスクリーンを無効化（E2E テスト用） |

## License

MIT
