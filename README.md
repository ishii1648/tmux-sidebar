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
  'run-shell "p=$(tmux split-window -hfb -l 35 -P -F \"#{pane_id}\" tmux-sidebar); tmux set-option -p -t \"$p\" @pane_role sidebar"'

# サイドバーへの誤フォーカスを防ぐ（通常の prefix+hjkl でスキップされる）
set-hook -g after-select-pane \
  'run-shell "tmux-sidebar focus-guard"'
```

> `split-window -P -F "#{pane_id}"` で生成したペイン ID を取得し、`set-option -p @pane_role sidebar` でペインオプションを設定することで `focus-guard` やトグルキーバインドがサイドバーを識別できます。

### 2. toggle キーバインド（任意）

```tmux
# prefix+e でサイドバーを表示・非表示切り替え
bind-key e run-shell '\
  if tmux list-panes -F "#{@pane_role}" | grep -q "^sidebar$"; then \
    tmux kill-pane -t $(tmux list-panes -F "#{pane_id} #{@pane_role}" | awk '"'"'/sidebar/{print $1}'"'"'); \
  else \
    p=$(tmux split-window -hfb -l 35 -P -F "#{pane_id}" tmux-sidebar); \
    tmux set-option -p -t "$p" @pane_role sidebar; \
  fi'
```

### 3. サイドバーへのフォーカスキーバインド（任意）

通常の pane 移動（`prefix+hjkl` 等）ではサイドバーはスキップされます。
専用キーでのみサイドバーにフォーカスを当てたい場合は以下を設定してください。

```tmux
# 任意のキーでサイドバーにフォーカス（prefix なし）
# <key> は端末エミュレータ側で割り当てた escape sequence に合わせて変更する
bind-key -n <key> run-shell 'tmux-sidebar focus-sidebar'
```

> `tmux-sidebar focus-sidebar` は現在ウィンドウのサイドバー pane を探して
> フォーカスします。サイドバーが存在しないウィンドウでは何もしません。

#### iTerm2 で `cmd+s` に割り当てる場合

1. **iTerm2** の `Preferences > Keys > Key Bindings` を開く
2. `+` で新しいバインドを追加：
   - **Keyboard Shortcut**: `⌘S`
   - **Action**: `Send Escape Sequence`
   - **Esc+**: `[18~`（`F7` に相当するシーケンス）
3. `~/.tmux.conf` に以下を追加：

```tmux
bind-key -n F7 run-shell 'tmux-sidebar focus-sidebar'
```

> 別のシーケンスを使う場合は `tmux list-keys -N` や `cat -v` で
> 送信されるシーケンスを確認してから対応するキー名を設定してください。

### 4. Claude Code の状態ファイル（任意）

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
  # セッション起動ディレクトリを記録（初回のみ）
  # サイドバーの Git 情報はこのパスを基準に表示される
  [ -f "$dir/pane_${num}_path" ] || pwd > "$dir/pane_${num}_path"
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
