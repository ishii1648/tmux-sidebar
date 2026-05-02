# tmux-sidebar 仕様

この文書はユーザ視点の振る舞いを記述する。実装方法や設計判断は
`docs/design.md`、過去の経緯は `docs/history.md` に記載する。

---

## 概要

tmux-sidebar は tmux の **cross-context 軸（session / window）** を司る常駐 control surface である。
左端 sidebar pane に全 session/window を一覧表示し、cursor 選択 + 単打鍵で
switch / close / pin などのライフサイクル操作を発行する。
新規 session 生成は sidebar から起動される popup picker で行い、
ghq repo 選択 + agent mode 選択をワンフローで完結する。

pane 内部の操作（split-window, resize, zoom, copy-mode 等）と
server 境界（attach, detach, new-server）は対象外であり、tmux native の責務に残す。

## レンダリング先

| 面 | 用途 | サイズ |
|---|---|---|
| **pane mode** | 常駐 navigation + lifecycle 操作 | 既定 40 cols × 端末高 |
| **popup picker mode** | 新規 session wizard、その他の多段選択 | tmux popup（既定 80 × 24） |

両者は同一バイナリ。pane mode が popup picker を起動し、終了後に状態を取り込む。

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
| `N` | popup picker mode で新規 session 作成 | 後述 |

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

**pin は削除保護を兼ねる**: pinned session に対する `D`（session kill）はブロックされ、`pinned_sessions` から該当行を削除して unpin することを促すメッセージが footer に出る。`d`（window kill）は session 単位ではないため、pin の影響を受けない。

ファイル変更は sidebar 内部の reload tick（最大 10 秒間隔）で自動的に反映される。即時反映したい場合は tmux hook 経由で `SIGUSR1` を送るか、`tmux-sidebar restart` で再起動する。

### その他

| キー | 動作 |
|---|---|
| `Ctrl+C` | sidebar process 終了 |

## Popup picker mode

`N` 押下で sidebar process が tmux popup を起動し、popup 内で同一バイナリの
picker mode が走る。

### Step 1: repo 選択

ghq 配下の repo を fuzzy filter で選ぶ。
すでに session として開いている repo は dim 表示し、`Enter` を押すと
**新規作成せずその session に switch する**（重複作成防止）。

### Step 2: mode 選択

| Mode | 内容 |
|---|---|
| `claude` | session 内に Claude Code を起動 |
| `codex` | session 内に Codex CLI を起動 |
| `dispatch` | dispatch skill 経由で agent を起動（dotfiles 側 protocol） |
| `orchestrate` | orchestrate workflow を起動 |

### Step 3: mode 別追加設定

mode が要求する場合のみ表示（worktree branch 名、orchestrate chain 種別など）。
詳細は tmw / 各 skill の指定に従う。

### 完了時

popup を閉じ、tmw / agent 起動コマンドを実行する。
sidebar pane は自発的に reload し、新 session にカーソル移動する。

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
| `new` | popup picker mode を起動（通常は sidebar から `N` で間接起動） |
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
