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
- [§9. セッションの固定 (Pin) と非表示 (Hidden)（任意）](#9-セッションの固定-pin-と非表示-hidden任意)
- [§10. Popup picker（`N`）の前提（任意）](#10-popup-pickern-の前提任意)

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

## 10. Popup picker（`N`）の前提（任意）

サイドバーの `N` キーは tmux popup を開いて新規 session を作成するピッカーを起動する（ghq repo 選択 → agent mode 選択 → tmux session 生成）。

### 必須環境

- **tmux 3.2 以上**: `display-popup` が必要。`tmux-sidebar doctor` で `tmux popup support` の行を確認する。3.2 未満の環境では `N` を押すと popup の起動に失敗し、サイドバー footer にエラーメッセージが出る
- **`ghq` が PATH に存在**: `ghq list -p` で repo を列挙する
- **`claude` / `codex` バイナリが PATH に存在**（選択した mode に対応するもののみ）
- **`git` が PATH に存在**（`dispatch` mode のみ。worktree 作成に使用）

### picker の挙動

`N` を押すと 2-step の wizard が popup で起動する:

1. **Step 1: repo + launcher 選択**
   - ghq repo を fuzzy filter（subsequence match）。すでに同名 session が開いている repo は dim 表示
   - `Tab` で claude ↔ codex を切替（ヘッダーに current launcher 表示）
   - `Enter`: open 中 repo → その session に switch して終了。それ以外 → Step 2 へ
2. **Step 2: prompt 入力**
   - 入力した prompt の先頭行から `feat/<slug>` 形式で branch 名を自動生成（入力中にプレビュー表示）
   - 複数行の prompt の入れ方:
     - **bracketed paste で貼り付け**（CR / CRLF は LF に正規化されるので、terminal が LF を CR に変換する場合でも見た目通り動く）
     - **Ctrl+J** で newline 挿入（terminal 非依存、確実に動く）
     - **Shift+Enter / Alt+Enter** で newline 挿入（kitty キーボードプロトコル等で識別できる terminal のみ。識別できない場合は plain Enter として確定される）
   - `Enter` で dispatch 実行 → worktree 作成 + tmux session 生成中は spinner + 「dispatching <repo>...」の status が表示される（処理中はキー入力が無視される）
   - `Tab` で launcher 再切替
   - `:<branch>` プレフィックスで先頭行を始めると **branch 接続モード**になる:
     - branch が **local に存在** → そのまま worktree にチェックアウト
     - branch が **remote (origin) のみ** に存在 → `git fetch origin <branch>:<branch>` で local ref を作ってから worktree にチェックアウト
     - branch が **どこにも存在しない** → `origin/<default>` から新規作成
     - prompt は launcher に渡されず（`--no-prompt`）、worktree 内で launcher が idle 起動する。プロンプトを後から手で入力する想定
   - `Enter` で `internal/dispatch.Launch` が走り:
     - ghq 短縮名から repo path を解決
     - `git worktree add <repo>@<branch-dirname>` で worktree 作成（既存 branch 再利用、衝突回避、`.claude/settings.local.json` コピー）
     - `tmux new-session -d` で新規 session を作成（必要なら名前衝突回避の suffix）
     - `tmux send-keys` で `cd <work_dir>; claude < <prompt-file>`（または codex 用）を投入
     - `tmux switch-client` で生成された session に切り替え
   - `Esc` で Step 1 に戻る

`~/.config/dispatch/no-worktree-repos` に repo 短縮名（例: `github.com/foo/bar`）を 1 行ずつ書くと、その repo は worktree を作らずメインリポジトリでデフォルトブランチに checkout する挙動になる（dotfiles の dispatch.sh と互換）。

「素の claude / codex を起動したい」（worktree も prompt 投入も無しで session だけ作りたい）場合は、picker からは行わず、CLI で `tmux-sidebar dispatch <repo> --no-prompt --launcher claude` を直接叩くか、tmux native の `prefix+c` を使う。

### tmux 側の設定

サイドバー経由（`N`）で起動する場合、tmux.conf の追加設定は不要。サイドバーは内部で `tmux display-popup -E -w 80 -h 24 'tmux-sidebar new --context=...'` を直接呼び出す。

サイドバーを開かずに直接ピッカーを起動したい場合は、tmux.conf に bind-key を書く:

```tmux
# prefix + N → popup picker
bind-key N display-popup -E -w 80 -h 24 'tmux-sidebar new'

# prefix なしで使いたいなら -n を付ける
# bind-key -n F2 display-popup -E -w 80 -h 24 'tmux-sidebar new'
```

popup の幅・高さは呼び出し側（bind-key）で決める設計になっている。`-w 100 -h 30` のように好みのサイズへ調整できる。

`tmux-sidebar new` は `tmux display-popup` で囲まずに直接実行すると、現在のターミナルでそのまま picker UI が動く（split-window や `-X new-window` から呼ぶデバッグ用途、または非 tmux 環境での動作確認に便利）。`--context=<file>` を渡さない場合は「すでに開いている session の重複検出」がスキップされるが、ピッカーとしては問題なく動作する。

### dotfiles 側の popup tmw / dispatch_launcher との並存

dotfiles で `tmw` (fish function) を popup から呼ぶ既存のキーバインドや、`prefix+S` の `dispatch_launcher.fish` がある場合は、そのまま残して fallback として使える。サイドバーの `N` が primary path、popup tmw / dispatch_launcher は fallback という棲み分け。

将来 dotfiles 側の `dispatch.sh` が `tmux-sidebar dispatch "$@"` の thin wrapper に置き換われば、Claude Code skill (`/dispatch` slash command) と sidebar の `N` が同じ Go engine（`internal/dispatch`）を共有することになり、挙動の乖離リスクが消える。
