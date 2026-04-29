# tmux-sidebar

全 tmux セッション・ウィンドウの一覧と agent (Claude Code / Codex CLI) の状態をサイドバーペインに表示し、キーボード選択で対象ウィンドウへ即座に移動できる TUI ツール。

```
┌──────────────────────┬──────────────────────────────────────────┐
│ Sessions             │                                          │
│ ─────────────────────│                                          │
│ ishii1648_dotfiles   │         作業ペイン                        │
│   1: nvim            │                                          │
│ ishii1648_work       │                                          │
│ ▶ 1: main  [c]🔄3m   │                                          │
│   2: fish            │                                          │
│ infra                │                                          │
│   1: deploy          │                                          │
└──────────────────────┴──────────────────────────────────────────┘
```

## Features

- 全セッション・ウィンドウを階層表示（agent がいないウィンドウも含む）
- agent (Claude Code / Codex CLI) の状態バッジ: 行頭に agent タグ (`[c]` / `[x]`)、続けて状態絵文字 (`🔄Nm` / `💬`)
- `j` / `k` / `↑` / `↓` でカーソル移動、`Enter` で対象ウィンドウへジャンプ
- 任意の文字を入力するとインクリメンタル検索フィルタが効く（`Esc` でクリア）
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

### 8. Agent (Claude Code / Codex CLI) の状態ファイル（任意）

状態バッジを表示するには、agent (Claude Code / Codex CLI) の hook が `/tmp/agent-pane-state/` に状態ファイルを書き出す必要があります（ADR-063 Phase A 以降の形式）。

サイドバーが読むファイルは以下の通りです（真実の出典: `internal/state/state.go`）。

| ファイル | 内容 |
|---|---|
| `pane_N` | 1 行目: status (`running` / `idle` / `permission` / `ask`)<br>2 行目: agent kind (`claude` / `codex`)。空行や未対応文字列は unknown として扱われる |
| `pane_N_started` | `running` に遷移した unix epoch 秒。`🔄Nm` の経過分数の起点になる |
| `pane_N_path` | agent セッションの起動ディレクトリ。サイドバーの Git/PR 表示はこのパスを基準にする |
| `pane_N_session_id` | agent session の UUID。サイドバー右下の prompt プレビューを引くキーになる |

`.claude/settings.json` の hooks に以下を追加してください（Codex CLI も同様に hook 経由で同じスクリプトを呼び出すか、kind だけ書き換えた専用版を呼び出します）：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "agent-pane-state.sh running" }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "agent-pane-state.sh idle" }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "agent-pane-state.sh idle" }
        ]
      }
    ]
  }
}
```

`agent-pane-state.sh` の例（Claude Code 用。Codex 用に流用する場合は `AGENT_KIND=codex` を環境変数なり引数なりで渡し、2 行目に書き分けてください）：

```sh
#!/bin/sh
# $TMUX_PANE の数値部分をペイン番号として使う
num=$(echo "$TMUX_PANE" | tr -d '%')
dir=/tmp/agent-pane-state
mkdir -p "$dir"

# pane_N: 1 行目=status, 2 行目=agent kind ("claude" | "codex")
# サイドバーは 2 行目を見て [c] / [x] のタグを描画する
kind="${AGENT_KIND:-claude}"
printf '%s\n%s\n' "$1" "$kind" > "$dir/pane_${num}"

if [ "$1" = "running" ]; then
  date +%s > "$dir/pane_${num}_started"
  # セッション起動ディレクトリを記録（初回のみ）
  # サイドバーの Git 情報はこのパスを基準に表示される
  [ -f "$dir/pane_${num}_path" ] || pwd > "$dir/pane_${num}_path"
fi

# agent session UUID をプレビュー用に書き出す（hook が JSON で渡してくる場合は
# それを jq で抜くか、あるいはエージェント側の env を参照する）
if [ -n "$CLAUDE_SESSION_ID" ]; then
  echo "$CLAUDE_SESSION_ID" > "$dir/pane_${num}_session_id"
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
| `restart` | 全 tmux ウィンドウのサイドバーペインを kill して再生成（バイナリ更新後に使う） |
| `doctor [--yes]` | tmux 設定をチェック（`--yes` で自動修正） |
| `upgrade` | GitHub Releases から最新バイナリをダウンロードしてインストール |
| `version` | バージョンを表示 |

## Keyboard shortcuts

フッターには `Esc:clear ^C:quit` が表示されます。

| キー | 動作 |
|------|------|
| `j` / `↓` | 次のウィンドウ行へ（検索クエリが空のときのみ。検索中は文字入力扱い） |
| `k` / `↑` | 前のウィンドウ行へ（検索クエリが空のときのみ） |
| `Enter` | 選択ウィンドウへ移動 |
| 任意の文字入力 | インクリメンタル検索フィルタ（セッション名・ウィンドウ名に対する大文字小文字非依存の部分一致） |
| `Backspace` | 検索クエリを 1 文字削除 |
| `Esc` | 検索クエリをクリア |
| `Ctrl+C` | 終了 |

## State badges

各ウィンドウ行は `<agent タグ><状態バッジ>` の形式で右端に表示されます。状態バッジは `idle` のときだけ非表示です。

### Agent タグ

| タグ | 装飾 | 意味 |
|---|---|---|
| `[c]` | 無着色 | Claude Code（`pane_N` の 2 行目が `claude` または unknown のフォールバック） |
| `[x]` | cyan | Codex CLI（`pane_N` の 2 行目が `codex`） |

### Status バッジ

| バッジ | 状態 | 意味 |
|---|---|---|
| `🔄Ns` / `🔄Nm` | `running` | 実行中。1 分未満は秒、1 分以上は分で表示 |
| `💬` | `permission` | 権限確認待ち（permission 用の色） |
| `💬` | `ask` | ユーザー応答待ち（ask 用の色。実装上 Claude のみ）|
| (非表示) | `idle` | バッジを描画しない |

## Environment variables

| 変数 | 説明 |
|------|------|
| `TMUX_SIDEBAR_STATE_DIR` | 状態ファイルのディレクトリ（デフォルト: `/tmp/agent-pane-state`） |
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
