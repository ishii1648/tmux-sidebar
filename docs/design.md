# tmux-sidebar 設計

この文書は `docs/spec.md` の振る舞いをどう実現するかを記述する。
ユーザ視点の仕様、キー操作、設定ファイル、状態ファイルの外部契約は
`docs/spec.md` を正とする。過去の Fish 実装や ADR 経緯は
`docs/history.md` に分離する。

---

## 全体構成

tmux-sidebar は tmux の左端 pane 上で Bubble Tea TUI として動作する。
pane 自体の生成・削除・幅補正・フォーカス移動は tmux hook と
CLI サブコマンドで扱い、TUI は一覧表示、検索、カーソル移動、
選択 window への移動を担当する。

| 層 | 主な責務 |
|---|---|
| `main.go` | CLI dispatch、Bubble Tea 起動、tmux pane metadata 設定、fsnotify/SIGUSR1 の注入 |
| `internal/ui` | TUI model、表示、キー入力、検索、状態反映、Git/PR/prompt preview |
| `internal/tmux` | tmux コマンド実行、session/window/pane 情報取得、window 切替 |
| `internal/state` | `/tmp/agent-pane-state` 形式の読み取り |
| `internal/config` | `hidden_sessions` と sidebar width の読み込み |
| `internal/doctor` | README に沿った tmux/Claude 設定診断 |

---

## TUI ライフサイクル

通常起動時、`main.go` は以下を行う。

1. `TMUX_PANE` から現在の pane を特定し、`@pane_role=sidebar` を設定する。
2. pane 単位の `window-style=default` を設定し、非フォーカス時の灰色表示を抑止する。
3. `/tmp/tmux-sidebar-<pane>.pid` を作成し、tmux hook から `SIGUSR1` を送れるようにする。
4. `state.NewFSReader(os.Getenv("TMUX_SIDEBAR_STATE_DIR"))` を作成する。
5. Bubble Tea program を alt-screen + focus reporting 付きで起動する。
6. 状態ディレクトリを `fsnotify` で監視し、変更時に `ui.StateChangedMsg` を送る。
7. `SIGUSR1` 受信時に `ui.TmuxChangedMsg` を送る。

`SIGHUP` / `SIGTERM` では pid file を削除して終了する。
`kill-pane` では `defer` が実行されないことがあるため、signal handler 側でも削除する。

---

## tmux 情報取得

session/window/pane の一覧は `tmux list-panes -a -F ...` の 1 回の呼び出しで取得する。
`internal/tmux.ListAll()` は以下をまとめて返す。

- session id/name
- window id/index/name
- pane id/index/number
- `window_active`
- `session_attached`

TUI はこの結果から session 順、window 順、window ごとの pane number を組み立てる。
状態ファイルは pane number をキーにしているため、window -> pane numbers の map を
保持し、状態だけの更新時は tmux を再実行しない。

active window は sidebar 自身の session id で絞り込む。
`window_active=1` は session ごとの値なので、絞り込まないと別 session の active window が
他 session の sidebar に混入する。

---

## 状態更新

状態更新は 1 秒 polling ではない。現在の設計はイベント駆動を基本にし、
必要な周期処理だけを残す。

| 契機 | 処理 |
|---|---|
| 起動直後 | tmux 一覧 + 状態ファイルを読み込む |
| 状態ファイル変更 (`fsnotify`) | 状態ファイルのみ読み直す |
| tmux hook からの `SIGUSR1` | tmux 一覧 + 状態ファイルを読み直す |
| 1 分 tick | running elapsed 表示のため状態ファイルのみ読み直す |
| 10 秒 tick | Git/PR 更新と、SIGUSR1 が届かない場合の active window fallback |

状態ファイル変更時に tmux を叩かないことで、agent の状態更新が頻繁でも tmux 側の負荷を
増やさない。window/session の増減は tmux hook からの `SIGUSR1` を主経路とし、
hook が未設定または失敗した場合でも最大 10 秒で収束する。

---

## 状態ファイル

状態ファイルの外部仕様は `docs/spec.md` に記載する。実装上は
`internal/state.FSReader` が以下を読む。

- `pane_N`: 1 行目 status、2 行目 agent kind
- `pane_N_started`: running 開始 epoch 秒
- `pane_N_path`: agent 起動ディレクトリ
- `pane_N_session_id`: agent session UUID

`pane_N` が存在しない pane は agent なしとして扱う。
`pane_N` の 2 行目が `claude` / `codex` 以外の場合は unknown とし、
表示上は Claude と同じ `[c]` fallback にする。

`/tmp` 配下は world-writable なので、reader は regular file 以外を無視する。
symlink や device file を読まないことで、`/dev/zero` などへの誘導による停止を避ける。

---

## UI model

`internal/ui.Model` は以下の状態を持つ。

- 全 item (`[]ListItem`)
- window id -> pane numbers
- cursor index と cursor window id
- active window id
- search query
- Git/PR cache
- prompt preview cache
- focus state
- scroll offset と terminal size

cursor は index だけでなく window id でも保持する。
tmux window の追加・削除や検索で item index が変わっても、同じ window に戻せるようにするため。
該当 window が消えた場合は active window、sidebar 自身の window、最初の window の順で fallback する。

session header は選択対象ではない。`j` / `k` / arrow 移動では window 行だけを移動する。

---

## 入力と focus

Bubble Tea の focus event を使い、sidebar pane が focus されている時だけキー入力を処理する。
focus がない時は `Ctrl+C` 以外のキーを無視する。

`Esc` は検索クエリのクリアであり、pane focus を外す操作ではない。
pane focus の移動は tmux key binding 側で行う。通常運用では `focus-or-open` のような
専用 key binding で sidebar に入る。

`Enter` は選択中 window の session name と window index から
`tmux switch-client -t <session>:<index>` を実行する。

---

## sidebar pane の生成と識別

sidebar pane は `split-window -hfb` で左端に作る。hook では `-d` を付け、
作成時にフォーカスを奪わない。

pane の識別には tmux pane option `@pane_role=sidebar` を使う。
通常起動時にも `main.go` が自身の pane にこの option を設定し、
hook 側で設定漏れがあっても後続処理で sidebar と認識できるようにする。

`split-window -P -F "#{pane_id}"` の stdout を `run-shell` 内で捕捉する方式は
環境によって扱いづらいため、README の hook 例では `-hfb` で左端に作られる性質を使い、
`#{window_id}.{left}` に `@pane_role` を設定する。

---

## CLI サブコマンド

| サブコマンド | 設計上の役割 |
|---|---|
| `toggle` | 現在 window の sidebar pane を kill、なければ `split-window -hfb` で作成 |
| `focus-or-open` | sidebar があれば focus、なければ作成して focus |
| `close` | 現在 window の sidebar を閉じる |
| `cleanup-if-only-sidebar` | sidebar だけが残った window を削除し、pane 削除後の幅も補正 |
| `restart` | 既存 sidebar を kill して同じ window に再作成 |
| `doctor` | tmux.conf / Claude settings.json の設定診断と一部自動修正 |
| `upgrade` | GitHub Releases から最新バイナリを取得 |

`cleanup-if-only-sidebar` は全 window を scan する。
`pane-exited` / `after-kill-pane` hook は削除された pane の window context で実行されるとは限らないため、
`#{window_id}` に依存しない実装にしている。

---

## 幅管理

sidebar 幅は絶対セル数として扱う。
`split-window -l` と `resize-pane -x` は同じ幅を使う必要がある。

幅の読み込み順は以下。

1. `TMUX_SIDEBAR_WIDTH`
2. `~/.config/tmux-sidebar/width`
3. `config.DefaultSidebarWidth` (`40`)

`config.MinSidebarWidth` (`20`) 未満、parse 不能、未設定の場合は default に戻す。

tmux は client resize 時に pane 幅を比例 scale するため、README では
`client-resized` hook で sidebar pane に `resize-pane -x` を再適用する設定を案内する。
CLI サブコマンドとしての `enforce-width` は持たない。

doctor は `after-new-window` / `after-new-session` の `split-window -l`、
`client-resized` の `resize-pane -x`、runtime config の幅を比較し、
不一致を warning として報告する。

---

## Git/PR 情報と prompt preview

Git/PR 情報は表示中 window ごとに取得する。
agent state に `pane_N_path` があればその directory を使い、なければ tmux の
最初の non-sidebar pane の current path を使う。

PR 情報は `gh pr view --json number,state,isDraft` で取得する。
`gh` がない、対象が git repository ではない、PR がない場合は何も表示しない。
PR API 呼び出しは重いので、同じ branch かつ TTL 内なら cache を再利用する。

prompt preview は `pane_N_session_id` から Claude transcript を探し、
initial prompt を抽出して右下の preview area に表示する。
取得結果は session id ごとに cache する。

---

## dotfiles との分担

| 管理場所 | 内容 |
|---|---|
| この repository | Go 実装、リリースバイナリ、README/spec/design |
| dotfiles `aqua.yaml` | バイナリバージョン管理 |
| dotfiles `tmux.conf` | sidebar 生成、focus guard、SIGUSR1、幅補正 hook |
| Claude/Codex hook | `/tmp/agent-pane-state` への状態ファイル書き出し |

---

## 既知の制約

- tmux hook が未設定でも基本表示は動くが、window/session 変更の反映は最大 10 秒遅れる。
- sidebar への誤 focus 防止は tmux.conf hook に依存する。
- `client-resized` hook の幅値と runtime config の幅値は自動同期されない。
- `gh pr view` は環境の GitHub 認証状態に依存する。
- prompt preview は Claude transcript index と `pane_N_session_id` がある場合のみ表示される。
