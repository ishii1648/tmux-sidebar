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

```sh
gh release download --repo ishii1648/tmux-sidebar --pattern '*darwin_arm64*' --output - | tar xz
mv tmux-sidebar ~/.local/bin/
```

> OS/アーキテクチャに合わせてパターンを変更してください（例: `*linux_amd64*`）。
> リリース一覧は `gh release list --repo ishii1648/tmux-sidebar` で確認できます。

## Setup

### 1. サイドバー自動生成（必須）

新しいウィンドウ・セッションの作成時にサイドバーを自動生成する。

```tmux
# 新しいウィンドウを作るたびにサイドバーを自動生成
# -hfb で左端に作成し {left} ターゲットで @pane_role を設定
# ペイン数が 1 の場合のみ起動（二重作成を防ぐ）
set-hook -g after-new-window \
  'run-shell "[ $(tmux list-panes -t \"#{window_id}\" | wc -l) -eq 1 ] && { tmux split-window -hfb -d -l 40 -t \"#{window_id}\" tmux-sidebar; tmux set-option -p -t \"#{window_id}.{left}\" @pane_role sidebar; } || true"'

# new-session の初期ウィンドウにもサイドバーを自動生成
# after-new-window は new-session の初期ウィンドウには発火しないため別途必要
set-hook -g after-new-session \
  'run-shell "[ $(tmux list-panes -t \"#{window_id}\" | wc -l) -eq 1 ] && { tmux split-window -hfb -d -l 40 -t \"#{window_id}\" tmux-sidebar; tmux set-option -p -t \"#{window_id}.{left}\" @pane_role sidebar; } || true"'
```

> **注意**: `split-window -P -F "#{pane_id}"` で新ペインの ID を `$()` でキャプチャする方法は、`run-shell` 内では stdout が返らないため動作しません。`-hfb` で常に左端に作成されることを利用し、`{left}` ターゲットで確実にサイドバーペインを特定してください。

ポイント:
- `-d`: サイドバー作成時にフォーカスを奪わない
- `-t "#{window_id}"`: 外部セッションからの `new-window` でも正しいウィンドウに作成される
- `[ ... -eq 1 ]`: hook の二重実行でサイドバーが2つ作られるのを防ぐ

### 2. sidebar への誤フォーカス防止 + カーソル追従通知（必須）

```tmux
# ウィンドウ切替後:
#   - 常に SIGUSR1 でサイドバーにウィンドウ切替を通知（カーソル追従）
#   - アクティブペインが sidebar なら右隣へ移動（誤フォーカス防止）
set-hook -g after-select-window \
  'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"\$f\" ] && kill -USR1 \$(cat \"\$f\") 2>/dev/null; done; if [ \"#{@pane_role}\" = sidebar ]; then tmux select-pane -R; fi"'

# セッション切替後も同様
set-hook -g client-session-changed \
  'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"\$f\" ] && kill -USR1 \$(cat \"\$f\") 2>/dev/null; done; if [ \"#{@pane_role}\" = sidebar ]; then tmux select-pane -R; fi"'

```

### 3. サイドバーのみ残ったウィンドウの自動削除（推奨）

作業ペインを全て閉じた後に空のサイドバーウィンドウが残るのを防ぐ。ペイン削除後にサイドバーが拡大する場合も自動で幅を修正する。

```tmux
set-hook -g pane-exited     'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
set-hook -g after-kill-pane 'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
```

> **注意**: `-ga`（append）ではなく `-g`（上書き）を使用してください。`pane-exited` はプロセスが自然終了した場合、`after-kill-pane` は `kill-pane` で強制削除した場合に発火します。両方設定すると `kill-pane` 時に cleanup が2回走りますが、`cleanup-if-only-sidebar` は冪等なため問題ありません。

### 4. ディスプレイ移動時のサイドバー幅維持（推奨）

tmux はクライアントウィンドウのリサイズ時に全ペインを **比例的にスケール** するため、
異なる解像度のディスプレイ間を移動するとサイドバーの幅（列数）が変動し、
比率が一定に保てない。絶対セル数を再適用するフックを設定すると解消される。

```tmux
# client-resized: ウィンドウ全体のサイズ変化時に発火
# 全ウィンドウの sidebar ペインを 40 列に resize し直す
set-hook -g client-resized \
  'run-shell "tmux list-panes -aF \"##{pane_id} ##{@pane_role}\" | while read pane role; do [ \"\$role\" = sidebar ] && tmux resize-pane -t \"\$pane\" -x 40; done"'
```

> `##{...}` の `##` はサイドバー側に `#{...}` として渡すためのエスケープ（tmux format 展開を抑止）。
> シェル変数の `\$role` / `\$pane` も、tmux config 層で `$` を保護するためにバックスラッシュでエスケープしている。

幅をカスタマイズする場合は、`tmux split-window -l 40` と `resize-pane -x 40` の数値を揃えて変更する
（§1 の `after-new-window` / `after-new-session` フックも含む）。

### 5. SIGUSR1 による即時更新通知（推奨）

```tmux
set-hook -g window-linked   'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
set-hook -g window-unlinked 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
set-hook -g session-created 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
set-hook -g session-closed  'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
```

> **注意**: `-ga`（append）ではなく `-g`（上書き）を使用してください。`-ga` だと `source-file` で設定をリロードするたびにフックが重複蓄積し、`run-shell` の同期実行により tmux 全体にラグが生じます。

> 未設定でも動作しますが、ウィンドウの追加・削除がサイドバーに反映されるまで最大10秒かかります。

### 6. toggle キーバインド（任意）

```tmux
bind-key e run-shell 'tmux-sidebar toggle'
```

### 7. サイドバーへのフォーカスキーバインド（任意）

```tmux
# サイドバーがなければ作成してフォーカス、あればフォーカス移動
bind-key -n <key> run-shell 'tmux-sidebar focus-or-open'
```

> `<key>` は端末エミュレータ側で割り当てた escape sequence に合わせて変更してください。

### 8. Claude Code の状態ファイル（任意）

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

## Subcommands

| サブコマンド | 説明 |
|---|---|
| (なし) | TUI サイドバーを起動 |
| `close` | サイドバーを閉じる |
| `toggle` | サイドバーの表示/非表示を切り替え |
| `focus-or-open` | サイドバーがあればフォーカス、なければ作成 |
| `cleanup-if-only-sidebar` | sidebar のみ残ったウィンドウを削除 |
| `doctor [--yes]` | tmux 設定をチェック（`--yes` で自動修正） |
| `version` | バージョンを表示 |

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
| `TMUX_SIDEBAR_WIDTH` | サイドバー幅の既定値（列数、デフォルト: `40`、最小: `20`） |
| `TMUX_SIDEBAR_NO_ALT_SCREEN` | 設定するとオルタネートスクリーンを無効化（E2E テスト用） |

## Configuration files

### hidden_sessions

サイドバーに表示しないセッション名を指定するファイルです。1行1エントリで記述し、`#` 以降はコメントとして無視されます。

**ファイルパス**: `~/.config/tmux-sidebar/hidden_sessions`

```
# 表示対象外にするセッション名（1行1エントリ、# はコメント）
main
```

上記の例では `main` セッションがサイドバーのセッション一覧から非表示になります。ファイルが存在しない場合は全セッションが表示されます。

### width

サイドバーの既定幅（列数）を指定するファイルです。`TMUX_SIDEBAR_WIDTH` 環境変数が優先されます。

**ファイルパス**: `~/.config/tmux-sidebar/width`

```
40
```

最小値は `20`。範囲外や不正値はデフォルト `40` にフォールバックします。§1 と §4 の tmux.conf 側の数値も合わせて変更してください。

## License

MIT
