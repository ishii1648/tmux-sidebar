# Setup

tmux-sidebar を tmux に組み込むための設定手順。`tmux.conf` に追記するフックと、agent (Claude Code / Codex CLI) の状態ファイルを書き出す hook 設定をまとめている。

各項目には **必須 / 推奨 / 任意** のラベルを付けている。最低限 §1 と §2 を設定すればサイドバーは動作する。

- [§1. サイドバー自動生成（必須）](#1-サイドバー自動生成必須)
- [§2. sidebar への誤フォーカス防止 + カーソル追従通知（必須）](#2-sidebar-への誤フォーカス防止--カーソル追従通知必須)
- [§3. サイドバーのみ残ったウィンドウの自動削除（推奨）](#3-サイドバーのみ残ったウィンドウの自動削除推奨)
- [§4. ディスプレイ移動時のサイドバー幅維持（推奨）](#4-ディスプレイ移動時のサイドバー幅維持推奨)
- [§5. SIGUSR1 による即時更新通知（推奨）](#5-sigusr1-による即時更新通知推奨)
- [§6. toggle キーバインド（任意）](#6-toggle-キーバインド任意)
- [§7. サイドバーへのフォーカスキーバインド（任意）](#7-サイドバーへのフォーカスキーバインド任意)
- [§8. Agent (Claude Code / Codex CLI) の状態ファイル（任意）](#8-agent-claude-code--codex-cli-の状態ファイル任意)

## 1. サイドバー自動生成（必須）

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

## 2. sidebar への誤フォーカス防止 + カーソル追従通知（必須）

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

## 3. サイドバーのみ残ったウィンドウの自動削除（推奨）

作業ペインを全て閉じた後に空のサイドバーウィンドウが残るのを防ぐ。ペイン削除後にサイドバーが拡大する場合も自動で幅を修正する。

```tmux
set-hook -g pane-exited     'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
set-hook -g after-kill-pane 'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
```

> **注意**: `-ga`（append）ではなく `-g`（上書き）を使用してください。`pane-exited` はプロセスが自然終了した場合、`after-kill-pane` は `kill-pane` で強制削除した場合に発火します。両方設定すると `kill-pane` 時に cleanup が2回走りますが、`cleanup-if-only-sidebar` は冪等なため問題ありません。

## 4. ディスプレイ移動時のサイドバー幅維持（推奨）

tmux はクライアントウィンドウのリサイズ時に全ペインを **比例的にスケール** するため、
異なる解像度のディスプレイ間を移動するとサイドバーの幅（列数）が変動し、
比率が一定に保てない。`tmux-sidebar relayout` で全ウィンドウのレイアウトを再計算するフックを設定すると解消される。

```tmux
# client-resized: ウィンドウ全体のサイズ変化時に発火
# tmux-sidebar relayout が全ウィンドウのレイアウトを再構築し、
# sidebar を設定幅 (デフォルト 40 列) に固定したうえで残り幅を非 sidebar ペインに均等再配分する
set-hook -g client-resized 'run-shell "tmux-sidebar relayout"'
```

> **なぜ `resize-pane -x 40` ではなく `relayout` か**
>
> 単純な `tmux resize-pane -t <sidebar> -x 40` を hook で回す方法だと、3 ペイン以上の window で **右端ペインに累積ドリフト** が発生する。`resize-pane -x` は浮いた / 不足した列数を **直右隣のペイン** にだけ押し付けるため、ディスプレイ切替や popup 開閉のたびに右端ペインだけが極端に狭くなっていく（例: sidebar=40 / 中央=199 / 右端=71、本来は中央=右端=135 が期待値）。
>
> `tmux-sidebar relayout` は `select-layout` で window レイアウト全体を再構築し、sidebar を固定幅に、残りを非 sidebar ペインに均等配分するため、特定のペインだけがドリフトすることがない。nested split を含むレイアウトや sidebar が leaf top-level でないレイアウトはサポート外で、その場合は従来どおり `resize-pane -x` で sidebar 幅だけ戻すフォールバックが走る。

幅をカスタマイズする場合は環境変数 `TMUX_SIDEBAR_WIDTH=N` を設定する。`relayout` はこの値を読むので、設定したら §1 の `after-new-window` / `after-new-session` の `-l 40` も同じ値に揃える。

## 5. SIGUSR1 による即時更新通知（推奨）

```tmux
set-hook -g window-linked   'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
set-hook -g window-unlinked 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
set-hook -g session-created 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
set-hook -g session-closed  'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 \$(cat \"$f\") 2>/dev/null; done"'
```

> **注意**: `-ga`（append）ではなく `-g`（上書き）を使用してください。`-ga` だと `source-file` で設定をリロードするたびにフックが重複蓄積し、`run-shell` の同期実行により tmux 全体にラグが生じます。

> 未設定でも動作しますが、ウィンドウの追加・削除がサイドバーに反映されるまで最大10秒かかります。

## 6. toggle キーバインド（任意）

```tmux
bind-key e run-shell 'tmux-sidebar toggle'
```

## 7. サイドバーへのフォーカスキーバインド（任意）

```tmux
# サイドバーがなければ作成してフォーカス、あればフォーカス移動
bind-key -n <key> run-shell 'tmux-sidebar focus-or-open'
```

> `<key>` は端末エミュレータ側で割り当てた escape sequence に合わせて変更してください。

## 8. Agent (Claude Code / Codex CLI) の状態ファイル（任意）

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
