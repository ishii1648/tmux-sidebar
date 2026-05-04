# tmux-sidebar 仕様

この文書はユーザ視点の振る舞いを記述する。実装方法や設計判断は
`docs/design.md`、過去の経緯は `docs/history.md` に記載する。

---

## 概要

tmux-sidebar は tmux の **cross-context 軸（session / window）** を司る常駐 control surface である。
左端 sidebar pane に全 session/window を一覧表示し、cursor 選択 + 単打鍵で
switch / close / pin などのライフサイクル操作を発行する。
新規 session 生成は別バイナリの popup picker (`tmux-sidebar new`) で行い、
ghq repo 選択 + agent mode 選択をワンフローで完結する（tmux.conf の bind-key 経由で起動する）。

pane 内部の操作（split-window, resize, zoom, copy-mode 等）と
server 境界（attach, detach, new-server）は対象外であり、tmux native の責務に残す。

## レンダリング先

| 面 | 用途 | サイズ |
|---|---|---|
| **pane mode** | 常駐 navigation + lifecycle 操作 | 既定 40 cols × 端末高 |
| **popup picker mode** | 新規 session wizard、その他の多段選択 | tmux popup（既定 80 × 24） |

両者は同一バイナリ。pane mode と popup picker mode は独立したエントリポイント（pane mode は引数なし起動、picker mode は `tmux-sidebar new`）で、tmux 経由でのみ状態を共有する。

## 入力モデル

vim 風の modal。

| モード | 動作 |
|---|---|
| **normal** | 単打キーで commands 発行（switch, close, pin など） |
| **search** | `/` で進入。任意文字でインクリメンタル検索。`Esc` で normal へ戻る |

## 表示

session を見出し、window を子行として階層表示する。
pinned session は上部に区切り線で隔てて並べる。

```
● Sessions
─────────────────────
📌 tmux-sidebar
   1: main          [c]💬
─────────────────────
work
   1: main
▶  2: feature       [c]🔄3m
infra
   1: deploy        [x]💬
```

- pinned session は `📌` を付与し、unpinned 群との境界に区切り線
- 現在の tmux window は背景色で highlight
- sidebar pane が focus されている場合は title が `● Sessions`、focus されていない場合は `○ Sessions`
- **作成直後 10 秒以内** の session は session ヘッダ行 (`▾ <name>`) と所属 window 行が緑系の前景色で強調表示される。dispatch 完了の合図として display-message に依存しない可視化（時間経過で自然に通常スタイルに戻る）

## 操作（normal mode）

### 移動・切替

| キー | 動作 |
|---|---|
| `j` / `↓` | 次の window 行へ |
| `k` / `↑` | 前の window 行へ |
| `Enter` | 選択 window へ移動（`switch-client` + `select-window`） |
| `Tab` / `Shift+Tab` | filter 切替（All / Waiting） |

### 検索

| キー | 動作 |
|---|---|
| `/` | search モード進入 |
| `Esc` | search クエリをクリア + normal モードへ |
| `Backspace` | 1 文字削除 |

### Lifecycle

| キー | 動作 | 備考 |
|---|---|---|
| `d` | カーソル window を close | running agent 検出時は強い confirm |
| `D` | カーソル session を close | 複数 window 影響のため必ず confirm |

destructive 操作（close 系）は **state file の `running` / `permission` / `ask` 状態を根拠に confirm 強度を変える**。

| 状態 | confirm |
|---|---|
| `idle` | 単純確認（`y/N`） |
| `running` | 「N 分前から running、本当に kill する？」 |
| `permission` / `ask` | 強い警告 + 直近の prompt を preview に表示 |

### Pin

pin は `~/.config/tmux-sidebar/pinned_sessions` ファイルでのみ管理する（キー操作は提供しない）。
pinned session は上部に持ち上げられ、`📌 <name>` で表示される。pinned 群と unpinned 群の境界には区切り線が入る。
ファイルの記述順がそのまま表示順になる。詳細は [Configuration files](#configuration-files) と `docs/setup.md` 参照。

**pin は削除保護を兼ねる**:
- pinned session に対する `D`（session kill）はブロックされる
- pinned session の **最後の window** に対する `d`（window kill）もブロックされる（tmux 標準では最後の window を消すと session が消えるため、これを許すと `d` 経由で削除保護がバイパスされる）
- pinned session に複数 window があるときの `d`（最後でない window）はブロックしない

ブロック時は footer に `pinned_sessions` から該当行を削除するよう促すメッセージが出る。

ファイル変更は sidebar 内部の reload tick（最大 10 秒間隔）で自動的に反映される。即時反映したい場合は tmux hook 経由で `SIGUSR1` を送るか、`tmux-sidebar restart` で再起動する。

### その他

| キー | 動作 |
|---|---|
| `Ctrl+C` | sidebar process 終了 |

## Popup picker mode

`tmux-sidebar new` で picker TUI が起動する。tmux.conf 側で `tmux display-popup -E -w 80 -h 24 'tmux-sidebar new'` を bind-key に割り当てて popup として呼び出すことを想定する（[setup.md](setup.md#10-popup-pickern-の前提任意) 参照）。サイドバー pane mode 自体には起動キーを設けない。

### Step 1: repo 選択

ghq 配下の repo を fuzzy filter で選ぶ。
すでに session として開いている repo は dim 表示し、`Enter` を押すと
**新規作成せずその session に switch する**（重複作成防止）。

Step 1 では `Tab` で **launcher (claude / codex)** を切り替えられる。選んだ launcher は次のステップに引き継がれる。`Enter` で次のステップへ進む。

### Step 2: prompt 入力

dispatch を発火するための prompt 入力欄が出る。レイアウトは:

```
tab: モード切替  enter: 実行  `:<branch>` で既存 remote branch を checkout
claude / codex  <repo>
─────────
> ▏
```

- 上の launcher 表示は active（bold + 緑）と inactive（faint）で示される。`Tab` で claude ↔ codex を切り替えられる
- `Enter` で git worktree 作成 + tmux session 生成 + prompt 投入が実行される
- branch 名は **dispatch 側（popup ではなく background process）** が prompt 内容から決定する。`claude -p` で短い `<type>/<slug>` を生成し、`claude` 不在 / 認証切れ / timeout / 不正な出力時は決定論的な `feat/<slug>` slugify にフォールバックする。実際の branch 名は新 session が sidebar に出現したタイミングで確認できる。popup 入力中はプレビューを出さない（実値と異なる「予想 slug」を見せると誤導になるため）。例外として `:<branch>` checkout モードのときだけ、入力した branch 名そのものを `checkout:` プレビューとして faint 表示する
- 複数行の prompt は **bracketed paste で貼り付け** て入れる、もしくは **Ctrl+J / Shift+Enter / Alt+Enter** で改行を挿入する（Shift+Enter / Alt+Enter は kitty キーボードプロトコル等で plain Enter と区別できる terminal でのみ動く。区別できない terminal では plain Enter として扱われ確定する）。CR / CRLF は LF に正規化される。先頭行が slugify フォールバック用に使われ、全文がそのまま launcher に渡る（LLM 命名は全文を見る）
- popup 横幅を超える長い行は **soft-wrap される**（runewidth で文字幅単位）。継続行の prefix は明示的な改行（`\n`）か折返しかで見分けられる:
    - 入力先頭: `  > ` （bold）
    - `\n` 直後の論理行先頭: `    │ ` （faint guide）
    - soft-wrap の継続行: `      ` （空 6 スペースの indent。`│` を出さないことで「ユーザが入れた改行ではない」ことを示す）

    例（popup width が狭く `abcdefghijkl\nshort` を入力した場合）:

    ```
    > abcdefgh
          ijkl
        │ short▏
    ```

    cursor `▏` は最終行の末尾だけに出る。
- `Enter` で dispatch を **背景で起動** して popup は即閉じる（`tmux run-shell -b 'tmux-sidebar dispatch ...'`）。worktree 作成 + tmux session 生成 + launcher 起動 + LLM 命名はユーザを待たせない。**作業中の client は新 session に自動移動しない**。成功時の通知は出さず、新 session が sidebar に出現する（reload tick 最大 10 秒、または SIGUSR1 hook で即時）のがそのまま完了サインになる。attach するかは `prefix s` / sidebar からユーザが任意のタイミングで決める。失敗時のみ `tmux display-message` で `tmux-sidebar dispatch: <err>` が status line に出る
- `:<branch>` プレフィックスで先頭行を始めると **branch 接続モード**になる: 指定 branch が local にあればそれを、remote のみなら fetch してから、どこにも無ければ `origin/<default>` から新規作成して worktree を作る。prompt は launcher に渡されず idle で起動する
- `Esc` で Step 1 に戻る

### 完了時

popup を閉じ、dispatch を背景で起動する（前項参照）。新 session が生成されると sidebar の reload tick（最大 10 秒、または SIGUSR1 hook で即時）でリストに現れる。

## Preview

sidebar 下部の preview area は cursor が指す window の agent transcript の initial prompt を表示する（state file に `pane_N_session_id` がある場合のみ）。

## State / Activity badges

各 window 行の右端に `<agent タグ><status バッジ>` を表示する。

### Agent タグ

| タグ | 装飾 | 意味 |
|---|---|---|
| `[c]` | 無着色 | Claude Code（unknown / legacy fallback も含む） |
| `[x]` | cyan | Codex CLI |

### Status バッジ

| バッジ | 状態 | 意味 |
|---|---|---|
| `🔄Ns` / `🔄Nm` | running | 1 分未満は秒、1 分以上は分 |
| `💬` | permission | ユーザ応答待ち（permission 用色） |
| `💬` | ask | ユーザ応答待ち（ask 用色） |
| (非表示) | idle | バッジを描画しない |

## Configuration files

すべて `~/.config/tmux-sidebar/` 配下、1 行 1 entry、`#` でコメント。

| ファイル | 内容 |
|---|---|
| `hidden_sessions` | 表示しない session 名 |
| `pinned_sessions` | pin する session 名（行順 = 表示順） |

設定方法・記述例は [docs/setup.md](setup.md) を参照。

### 競合時の優先

- `hidden` > 表示（hidden 指定された session は pin されていても出さない）
- 表示される pinned session は **`pinned_sessions` の行順** で並ぶ（tmux 列挙順より優先）
- pinned 群と unpinned 群の境界に区切り線。両群とも非空のときだけ描画する（全件 pinned / 全件 unpinned のときは出さない）

## Required external tools

dispatch / popup picker のフローはローカルの CLI を直接呼び出す。以下が PATH に存在し、必要に応じて認証済みであることを前提とする。

| ツール | 用途 | 必須度 |
|---|---|---|
| `tmux` 3.2+ | popup / pane / session 制御全般 | 必須 |
| `git` | worktree / branch 操作 | 必須 |
| `ghq` | repo 一覧 (`ghq list -p`) | 必須 |
| `claude` ([Claude Code CLI](https://github.com/anthropics/claude-code)) | popup picker の Step 2 で `--launcher claude` を選んだ時の launcher、および dispatch 側の LLM branch 命名（`claude -p`） | popup picker を使うなら必須。命名のみ用途なら未認証でもフォールバックは動くが体験が落ちる |
| `codex` ([Codex CLI](https://github.com/openai/codex)) | popup picker の Step 2 で `--launcher codex` を選んだ時の launcher | codex を使うなら必須 |

`claude` / `codex` のどちらか片方しか使わない場合は、もう一方を入れる必要はない。LLM 命名は `claude` だけを使う（codex の `exec` サブコマンドは命名用途には使わない設計）。

## Environment variables

| 変数 | 説明 |
|---|---|
| `TMUX_SIDEBAR_STATE_DIR` | state file directory（既定 `/tmp/agent-pane-state`） |
| `TMUX_SIDEBAR_WIDTH` | sidebar 幅（列数、既定 `40`、最小 `20`） |
| `TMUX_SIDEBAR_NO_ALT_SCREEN` | 設定で alt-screen 無効化（E2E 用） |

## Subcommands

| サブコマンド | 動作 |
|---|---|
| (なし) | sidebar pane mode を起動 |
| `new` | picker TUI を起動。popup として表示するかは呼び出し側（tmux.conf bind-key）が `tmux display-popup -E ...` で決める |
| `dispatch <repo> [prompt]` | git worktree + tmux session を作成して launcher (claude / codex) を起動する CLI（`/dispatch` skill の engine と共通） |
| `toggle` | 現在 window の sidebar を toggle |
| `close` | 現在 window の sidebar を閉じる |
| `focus-or-open` | sidebar があれば focus、なければ作成 |
| `cleanup-if-only-sidebar` | sidebar だけ残った window を削除 |
| `restart` | 既存 sidebar を kill して再生成 |
| `doctor [--yes]` | 設定診断と一部自動修正 |
| `upgrade` | GitHub Releases から最新バイナリ取得 |
| `version` | version 表示 |

## tmux hook

詳細は README の Setup を正とする。要点のみ:

- `after-new-window` / `after-new-session`: sidebar 自動生成
- `after-select-window` / `client-session-changed`: cursor 追従と誤 focus 逃し
- `pane-exited` / `after-kill-pane`: sidebar だけの window を削除
- `client-resized`: 幅再適用
- `window-linked` / `window-unlinked` / `session-created` / `session-closed`: 即時更新通知

hook 未設定でも window/session 変更は最大 10 秒で反映する。

## Coexistence with tmux native bindings

sidebar の操作キーは tmux 標準のキーバインドを上書きしない。
`prefix+s`, `prefix+&`, `prefix+,`, `prefix+$`, `prefix+.` 等は引き続き有効。
sidebar process が動いていない / フォーカス外でも tmux 標準操作は完全に動作する。

**sidebar は dominant path、tmux native は safety net。**

## 非目標

- pane 内部操作（split-window, resize-pane, zoom, copy-mode）の sidebar 経由化
- server 境界（attach, detach, new-server, kill-server）の制御
- tmux plugin manager への依存
- sidebar 幅のドラッグ変更
- tmux.conf 内の数値の自動書き換え
- 完全な undo close（scrollback 完全復元は tmux-resurrect 等別レイヤに委ねる）
- リポジトリ rename
