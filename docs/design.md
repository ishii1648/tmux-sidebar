# tmux-sidebar 設計

この文書は `docs/spec.md` の振る舞いをどう実現するかを記述する。
ユーザ視点の仕様、キー操作、設定ファイル、状態ファイルの外部契約は
`docs/spec.md` を正とする。過去の判断と方針反転は `docs/history.md` に分離する。

---

## 全体構成

tmux-sidebar は同一バイナリで **2 つのレンダリング先** を持つ。

```
tmux-sidebar binary
  ├── pane mode    (常駐、tmux pane 内、40 cols)        ← cross-context navigation + lifecycle
  └── picker mode  (transient、tmux popup、80×24)       ← 新規 session wizard
```

両者は bubbletea を使った Go TUI で、UI コンポーネント・色・キーバインドの一部を共有する。
pane mode が picker を起動し、終了後に状態を取り込む（後述の IPC）。

| 層 | 主な責務 |
|---|---|
| `main.go` | CLI dispatch（pane / picker / その他 subcommand）、Bubble Tea 起動、tmux pane metadata 設定、fsnotify/SIGUSR1 の注入 |
| `internal/ui` | TUI model、modal 入力、表示、commands 発行、Git/PR/prompt preview |
| `internal/picker` | popup picker mode の TUI（repo 選択 → mode 選択 → 設定） |
| `internal/tmux` | tmux コマンド実行、session/window/pane 情報取得、mutate 操作（kill/swap/move/new）|
| `internal/state` | `/tmp/agent-pane-state` 形式の読み取り、unread/履歴の管理 |
| `internal/config` | hidden/pinned の読み込みと書き込み、幅の解決 |
| `internal/repo` | ghq 配下 repo 列挙、fuzzy filter |
| `internal/doctor` | tmux/Claude/Codex 設定診断 |

---

## pane mode のライフサイクル

通常起動時、`main.go` は以下を行う。

1. `TMUX_PANE` から現在の pane を特定し、`@pane_role=sidebar` を設定
2. pane 単位の `window-style=default` を設定し、非フォーカス時の灰色表示を抑止
3. `/tmp/tmux-sidebar-<pane>.pid` を作成し、tmux hook から `SIGUSR1` を送れるようにする
4. `state.NewFSReader(...)` を作成
5. Bubble Tea program を alt-screen + focus reporting 付きで起動
6. 状態 directory を `fsnotify` で監視し、変更時に `ui.StateChangedMsg` を送る
7. `SIGUSR1` 受信時に `ui.TmuxChangedMsg` を送る

`SIGHUP` / `SIGTERM` では pid file を削除して終了する。
`kill-pane` では `defer` が実行されないことがあるため、signal handler 側でも削除する。

---

## modal 入力モデル

入力モードを `normal` と `search` の 2 つに分け、Bubble Tea の `Update` で分岐する。

| モード | 受け付けるキー |
|---|---|
| `normal` | 単打コマンド（`d`, `R`, `p`, `n`, `N` など）、移動キー（`j`/`k`, `gg`/`G`）、`/` で search へ遷移 |
| `search` | 任意文字（クエリに追記）、`Backspace`、`Esc` で normal へ戻る、`Enter` で結果先頭にカーソル |

Model に `inputMode` フィールドを追加し、`handleKey` がモード別の handler を呼ぶ。
search 中も `Ctrl+C` だけは normal mode と同じく terminate を発火する。

move-mark / multi-select / confirm dialog は normal mode の **sub-state** として表現する（モード追加ではなく内部フラグ）。これにより key dispatch ロジックを単純に保つ。

---

## tmux 情報取得

session/window/pane の一覧は `tmux list-panes -a -F ...` の 1 回の呼び出しで取得する。
`internal/tmux.ListAll()` は session/window/pane の id・index・name、`window_active`、
`session_attached` をまとめて返す。

active window は sidebar 自身の session id で絞り込む（cross-session 漏れを防ぐため）。

state ファイルは pane number をキーにしているため、window → pane numbers の map を
保持し、状態だけの更新時は tmux を再実行しない。

---

## 状態更新

| 契機 | 処理 |
|---|---|
| 起動直後 | tmux 一覧 + 状態ファイル読込 |
| 状態ファイル変更 (`fsnotify`) | 状態ファイルのみ読み直す |
| tmux hook からの `SIGUSR1` | tmux 一覧 + 状態ファイル読み直す |
| 1 分 tick | running elapsed 表示更新 |
| 10 秒 tick | Git/PR 更新、active window fallback、capture-pane preview 再取得 |
| sidebar 自発の mutate 後 | 即 reload（楽観更新 + 後追い確認） |

mutate を sidebar が発行する場合、コマンド成功後すぐに `loadData()` を呼んで view を更新する。
tmux hook 経由の SIGUSR1 もすぐ届くため二重更新になり得るが、ローカル mutate 反映の即時性を優先する。

---

## mutate 操作の翻訳

各 command は tmux primitive へ素直に翻訳する。

| sidebar command | tmux primitive |
|---|---|
| `Enter`（switch） | `switch-client -t <session>` + `select-window -t <session>:<index>` |
| `d`（window close） | `kill-window -t <session>:<index>` |
| `D`（session close） | `kill-session -t <session>` |
| `n`（新規 window） | `new-window -t <session> -c <session current path>` |
| `Shift+J`/`Shift+K`（swap） | `swap-window -t <session>:<idx> -s <session>:<other-idx>` |
| `m`（move） | `move-window -s <src>:<idx> -t <dst>:` |

destructive 操作（close 系）は state file の status を読んで confirm 強度を分岐する:

```
status = idle              → "kill window 'X'? [y/N]"
status = running           → "running for Nm — really kill? [y/N]"
status = permission / ask  → "agent is waiting for input — really kill? [y/N]"
                              + 直近 prompt を preview area に表示
```

multi-select 中の bulk close は選択された window 群について個別に判定し、
**全件 idle の場合のみ** 一括 confirm（"kill 5 windows?"）。1 件でも running があれば個別 confirm に降格する。

---

## 並べ替え・移動

### 同 session 内 swap (`Shift+J/K`)

`swap-window -t target:idx -s target:other-idx` を発行。カーソルは追従する（`cursorWinID` は維持）。

### 別 session への move (`m`)

2 段階モード:
1. `m` 押下で `moveMark = cursorWinID` を保存（行に視覚マーカー表示）
2. カーソルを target session の 任意 window 行へ移動
3. もう一度 `m` で `move-window -s <src> -t <dst session>:` 発行
4. `Esc` で取消

target が session header の場合は session 末尾に挿入。

---

## pin / hidden の合成

`internal/config` に以下の slice を持つ。

```go
type Config struct {
    HiddenSessions []string
    PinnedSessions []string  // 行順 = 表示順
    Width          int
}
```

loadData で:

1. tmux から取得した全 session を `hidden` で除外
2. 残りを `pinned` と `unpinned` に分割
3. `pinned` は `PinnedSessions` の順で並べ、unpinned は tmux 列挙順のまま
4. pinned/unpinned の間に区切り線を挿入

pin toggle 時は `pinned_sessions` を書き戻し、loadData を再発火する。

---

## activity history (unread badge)

state file 形式を拡張する:

| ファイル | 内容 |
|---|---|
| `pane_N_event_log` | 改行区切りで `<unix_epoch>:<event>` を append（`event` は `permission` / `ask`）|

sidebar は switch-client 後に当該 pane の `pane_N_event_log` を **truncate** することで unread を 0 にする。
表示時は last-attached time（tmux で取得）以降に発生した event 数を unread として `!N` バッジに表示する。

ファイル append は agent hook の責務。tmux-sidebar 側は読み取りと truncate のみ。

---

## capture-pane preview

cursor 行が指す window の代表 pane（state file が指す pane、なければ first non-sidebar pane）について、
`tmux capture-pane -p -t <pane>` の末尾 N 行を取得し preview area に表示する。

- 取得は **10 秒 tick** に乗せる（git polling と同 cadence）
- prompt preview がある場合はそちらを優先、なければ capture preview を fallback
- `previewMode` フラグで明示切替可能（将来）

---

## popup picker mode

### 起動

pane mode から `N` 押下で:

```
tmux display-popup -E -w 80 -h 24 -E 'tmux-sidebar new --context=<file>'
```

`--context=<file>` は temp file path で、現在の session 一覧 / pinned / sidebar の
sessionID を JSON で渡す。picker mode はこれを読んで重複検出 / dim 表示に使う。

### picker の状態機械

```
[repo 選択] --(Enter)--> [mode 選択] --(Enter)--> [mode 別設定 (任意)] --(Enter)--> [実行]
       ↑                       ↑                          ↑
       Esc で前 step、最初の step で Esc → 取消（exit）
```

### 実行

picker mode は最終的に tmw / agent 起動コマンドを `os/exec` で実行し、
成功時は exit code 0、失敗時は stderr に reason を出して非ゼロ終了する。

pane mode 側は popup の終了を `display-popup -E` の return で受ける（`-E` は popup の child の
終了を待ち、exit code を伝播する）。終了後 pane mode は `loadData()` を発火し、
新 session を検出してカーソルを移動させる。

### 重複検出

picker の repo 一覧で、すでに同名 session が存在する repo は dim 表示する。
`Enter` 押下時、新規作成ではなく `tmw` をスキップして既存 session への switch を発行する。

---

## tmw / dispatch / orchestrate との分担

| 責務 | 担当 |
|---|---|
| ghq repo 列挙 | `internal/repo`（ghq の出力を直接呼ぶ） |
| repo 表示・filter UI | tmux-sidebar picker mode |
| worktree 判定 / session 作成 | tmw（`tmw <repo>` をそのまま呼ぶ） |
| agent 起動（claude/codex） | tmw / dotfiles 側 hook |
| dispatch / orchestrate 実行 | dispatch / orchestrate skill |

picker mode は **UI 層に専念** し、決定後は tmw / skill にコマンドを委譲する。

popup tmw は廃止せず、tmux 側 keybind で残す（sidebar 未起動時 / 互換用）。

---

## 状態ファイル

外部仕様は `docs/spec.md` を正とする。実装上は `internal/state.FSReader` が以下を読む。

- `pane_N`: 1 行目 status、2 行目 agent kind
- `pane_N_started`: running 開始 epoch 秒
- `pane_N_path`: agent 起動 directory
- `pane_N_session_id`: agent session UUID
- `pane_N_event_log`: 改行区切り `<epoch>:<event>` の append-only ログ（unread 用）

`/tmp` 配下は world-writable なので、reader は regular file 以外を無視する。

---

## sidebar pane の生成と識別

sidebar pane は `split-window -hfb` で左端に作る。hook では `-d` を付けてフォーカスを奪わない。
pane の識別には tmux pane option `@pane_role=sidebar` を使う。

通常起動時にも `main.go` が自身の pane にこの option を設定し、hook 側で漏れがあっても
後続処理で sidebar と認識できるようにする。

---

## CLI サブコマンド

| サブコマンド | 設計上の役割 |
|---|---|
| (なし) | pane mode 起動 |
| `new [--context=<file>]` | popup picker mode 起動（pane mode から間接実行） |
| `toggle` | 現在 window の sidebar pane を kill、なければ作成 |
| `focus-or-open` | sidebar があれば focus、なければ作成して focus |
| `close` | 現在 window の sidebar を閉じる |
| `cleanup-if-only-sidebar` | sidebar だけ残った window を削除し、pane 削除後の幅補正 |
| `restart` | 既存 sidebar を kill して同じ window に再作成 |
| `doctor [--yes]` | 設定診断と一部自動修正 |
| `upgrade` | GitHub Releases から最新バイナリを取得 |

---

## 幅管理

sidebar 幅は絶対セル数として扱う。`split-window -l` と `resize-pane -x` は同じ幅を使う。

幅の読み込み順:

1. `TMUX_SIDEBAR_WIDTH`
2. `config.DefaultSidebarWidth`（`40`）

`config.MinSidebarWidth`（`20`）未満、parse 不能、未設定の場合は default に戻す。

doctor は tmux.conf の `split-window -l` / `resize-pane -x` / runtime config の幅を比較し、
不一致を warning として報告する。

---

## tmux native bindings との共存

sidebar は tmux 標準のキーバインドを上書きしない。`prefix+s`, `prefix+&`, `prefix+,` などは
ユーザの設定どおり動作する。sidebar process が動いていない / focus 外でも tmux 操作は完全に動く。

---

## dotfiles との分担

| 管理場所 | 内容 |
|---|---|
| この repository | Go 実装、リリースバイナリ、README/spec/design |
| dotfiles `aqua.yaml` | バイナリバージョン管理 |
| dotfiles `tmux.conf` | sidebar 生成、focus guard、SIGUSR1、幅補正 hook、popup picker keybind |
| Claude/Codex hook | `/tmp/agent-pane-state` への状態書き出し（`pane_N_event_log` 含む） |
| tmw | session 生成 + agent 起動 (engine。picker mode から呼ぶ) |
| dispatch / orchestrate skill | mode 実行（picker mode から委譲） |

---

## 既知の制約

- tmux hook が未設定でも基本表示は動くが、window/session 変更の反映は最大 10 秒遅れる
- `client-resized` hook の幅値と runtime config の幅値は自動同期されない
- `gh pr view` は環境の GitHub 認証状態に依存する
- prompt preview は agent transcript index と `pane_N_session_id` がある場合のみ
- 完全な undo close は提供しない（kill 直前 confirm + scrollback 退避 path 通知のみ）
- popup picker は tmux popup の機能に依存する（tmux 3.2 以上）
