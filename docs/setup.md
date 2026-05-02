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
- [§8. Agent (Claude Code / Codex CLI) の状態ファイル（推奨）](#8-agent-claude-code--codex-cli-の状態ファイル推奨)
- [§9. セッションの固定 (Pin) と非表示 (Hidden)（任意）](#9-セッションの固定-pin-と非表示-hidden任意)
- [§10. Popup picker（`tmux-sidebar new`）の前提（任意）](#10-popup-pickertmux-sidebar-new-の前提任意)

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

## 8. Agent (Claude Code / Codex CLI) の状態ファイル（推奨）

状態バッジ（`🔄`/`💬`）・経過時間表示・`pane_N_path` 経由の Git/PR 表示・prompt preview は、すべて agent (Claude Code / Codex CLI) の hook が `/tmp/agent-pane-state/` に状態ファイルを書き出すことで成立します（ADR-063 Phase A 以降の形式）。これらが無いとサイドバーは session/window 切替器としてしか機能しないため、設定を強く推奨します。

サイドバーが読むファイルは以下の通りです（真実の出典: `internal/state/state.go`）。

| ファイル | 内容 |
|---|---|
| `pane_N` | 1 行目: status (`running` / `idle` / `permission` / `ask`)<br>2 行目: agent kind (`claude` / `codex`)。空行や未対応文字列は unknown として扱われる |
| `pane_N_started` | `running` に遷移した unix epoch 秒。`🔄Nm` の経過分数の起点になる |
| `pane_N_path` | agent セッションの起動ディレクトリ。サイドバーの Git/PR 表示はこのパスを基準にする |
| `pane_N_session_id` | agent session の UUID。サイドバー右下の prompt プレビューを引くキーになる |

これらの状態ファイルを書き出すために `tmux-sidebar hook <status>` サブコマンドが用意されています。サブコマンドは `$TMUX_PANE` から pane 番号を取り出し、agent が stdin に渡す JSON ペイロード（`session_id` / `cwd` を共通フィールドとして含む）をパースして `pane_N` / `pane_N_started` / `pane_N_path` / `pane_N_session_id` を一貫した形式で書き出します。`pane_N_path` は最初に `running` に遷移したときだけ書かれ、以降は上書きされません。`<status>` には `running` / `idle` / `permission` / `ask` のいずれかを指定します。

### Claude Code

`~/.claude/settings.json` の `hooks` キーに以下を追加してください：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "tmux-sidebar hook running" }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "tmux-sidebar hook idle" }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "tmux-sidebar hook idle" }
        ]
      }
    ]
  }
}
```

### Codex CLI

Codex CLI の hook 設定は `~/.codex/hooks.json` に置きます（`~/.codex/config.toml` の inline `[hooks]` でも同等です）。Claude Code と同じ shape の JSON で、`--kind codex` を付けて呼び分けます：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "tmux-sidebar hook running --kind codex" }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "tmux-sidebar hook idle --kind codex" }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "tmux-sidebar hook idle --kind codex" }
        ]
      }
    ]
  }
}
```

> Codex CLI は `~/.codex/hooks.json`（user-level）と `<repo>/.codex/hooks.json`（project-level、要 trust）を両方読み込みます。両方の event protocol（stdin の `session_id` / `cwd`）は Claude Code と共通です。

### doctor による自動チェック

`tmux-sidebar doctor` は両 agent の settings ファイルを検査し、以下を報告します：

- 必要な hook event（`PreToolUse` / `PostToolUse` / `Stop`）が未設定 → WARN
- 旧 inline shell 形式が残っている → WARN（`--yes` でサブコマンド形式へ自動置換）
- legacy state dir (`/tmp/claude-pane-state`) を参照している → WARN（同上）

`tmux-sidebar doctor --yes` で auto-fix を実行できます。

## 9. セッションの固定 (Pin) と非表示 (Hidden)（任意）

サイドバーの session 並びは、`~/.config/tmux-sidebar/` 配下の 2 ファイルで永続化する。
両ファイルとも **1 行 1 session 名 / `#` 始まりはコメント / 空行は無視** の形式。
ディレクトリが無ければ書き込み時に自動生成される。

| ファイル | 役割 |
|---|---|
| `pinned_sessions` | サイドバー上部に **持ち上げる** session（行順 = 表示順） |
| `hidden_sessions` | サイドバーから **完全に隠す** session |

### `pinned_sessions`

pin した session には `📌 <name>` が付き、unpinned 群との境界に区切り線が入る。
編集はエディタで直接行う（pin/unpin の頻度は低く、キーバインドは提供しない）。
ファイルを開いて上から並べたい順に session 名を書く。tmux の session 列挙順とは独立しているため、運用優先度に合わせて並べ替えられる。

例:

```
# ~/.config/tmux-sidebar/pinned_sessions
# 上から順に sidebar 上部に並ぶ
infra
work
tmux-sidebar
```

### `hidden_sessions`

スクラッチ session など、サイドバーに常時出したくないものを除外する。
こちらは `p` のような toggle キーは無いので、エディタで直接編集する。

```
# ~/.config/tmux-sidebar/hidden_sessions
scratch
tmw-popup
```

### 反映タイミング

ファイル編集後の反映は sidebar 内部の reload tick（最大 10 秒間隔）で自動的に行われる。
即時反映したい場合は §5 の SIGUSR1 hook が設定されていれば tmux 経由で発火させるか、`tmux-sidebar restart` でサイドバーを再起動する。

### kill との関係（削除保護）

pin は **削除保護** も兼ねる。pinned session の消滅につながる kill 操作は **ブロックされる**。

| 操作 | pinned session | 挙動 |
|---|---|---|
| `D`（session kill） | 対象 | ブロック |
| `d`（window kill） | **最後の window** | ブロック（消すと session 消滅 = `D` バイパス） |
| `d`（window kill） | 最後ではない window | 通常どおり通す |

ブロック時は footer に以下のメッセージが出るので、`pinned_sessions` から該当行を削除してから改めて押す。`📌` プレフィックスで pin が原因であることが視覚的に分かる（対象 session 名はカーソル位置で文脈的に示される）。

```
📌 unpin in config to kill        (D が pinned session を狙ったとき)
📌 last window — unpin in config  (d が pinned session の最後の window を狙ったとき)
```

これにより:
- 重要な session を `D` / `d` の単打で誤爆できない
- 設定ファイルが「実在しない session 名の残骸」で汚れない（kill が通らないので残骸が出ない）

「session 全部畳みたい」場合は `pinned_sessions` から行を消してから kill する 2 段階が必要。これは pin の意味（重要なので保護）に対する明示的なオプトアウト操作で、誤爆耐性とのトレードオフ。

### 競合時の優先

- `hidden` は `pinned` より強い。両方に書かれている session は **隠される**
- 表示される pinned session は `pinned_sessions` の行順で並ぶ（tmux 列挙順より優先）
- 区切り線は pinned 群と unpinned 群が **両方とも非空** のときだけ描画する

## 10. Popup picker（`tmux-sidebar new`）の前提（任意）

`tmux-sidebar new` は ghq repo + launcher (claude / codex) + prompt を選んで新規 session を作成する 2 段ピッカーを起動する。詳細な挙動は [spec.md の Popup picker mode](./spec.md#popup-picker-mode) を参照。

### 必須環境

- **tmux 3.2 以上**: `display-popup` が必要。`tmux-sidebar doctor` の `tmux popup support` 行で確認できる
- **`ghq`**: repo 一覧（`ghq list -p`）
- **`claude` / `codex`**: 選択した launcher に対応するもののみ
- **`git`**: worktree 作成（`dispatch` mode）

### tmux 側の設定（bind-key）

popup として起動するには tmux.conf に bind-key を書く:

```tmux
# prefix + N → popup picker
bind-key N display-popup -E -w 80 -h 24 'tmux-sidebar new'

# prefix なしで使いたいなら -n を付ける
# bind-key -n F2 display-popup -E -w 80 -h 24 'tmux-sidebar new'
```

popup の幅・高さは bind-key 側で決める（`-w 100 -h 30` 等で調整可能）。

`tmux-sidebar new` を `tmux display-popup` で囲まずに直接実行すると、現在のターミナルでそのまま picker UI が動く（split-window や `-X new-window` から呼ぶデバッグ用途、または非 tmux 環境での動作確認用）。

### worktree を作らない repo の指定

`~/.config/dispatch/no-worktree-repos` に repo 短縮名（例: `github.com/foo/bar`）を 1 行ずつ書くと、その repo は worktree を作らずメインリポジトリでデフォルトブランチに checkout する挙動になる（dotfiles の dispatch.sh と互換）。

「素の claude / codex を起動したい」（worktree も prompt 投入も無しで session だけ作りたい）場合は、picker からは行わず、CLI で `tmux-sidebar dispatch <repo> --no-prompt --launcher claude` を直接叩くか、tmux native の `prefix+c` を使う。

### dotfiles 側の popup tmw / dispatch_launcher との並存

dotfiles で `tmw` (fish function) を popup から呼ぶ既存のキーバインドや、`prefix+S` の `dispatch_launcher.fish` がある場合は、そのまま残して fallback として使える。`tmux-sidebar new` の bind-key を primary、既存 popup を fallback の棲み分けで運用できる。

将来 dotfiles 側の `dispatch.sh` が `tmux-sidebar dispatch "$@"` の thin wrapper に置き換われば、Claude Code skill (`/dispatch` slash command) と picker が同じ Go engine（`internal/dispatch`）を共有することになり、挙動の乖離リスクが消える。
