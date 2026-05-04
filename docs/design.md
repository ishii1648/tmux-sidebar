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
pane mode と picker mode は独立したエントリポイント（pane mode は引数なし起動、picker mode は `tmux-sidebar new`）で、tmux 経由（`list-sessions` 等）でのみ状態を共有する。

| 層 | 主な責務 |
|---|---|
| `main.go` | CLI dispatch（pane / picker / その他 subcommand）、Bubble Tea 起動、tmux pane metadata 設定、fsnotify/SIGUSR1 の注入 |
| `internal/ui` | pane mode の TUI model、modal 入力、表示、commands 発行、Git/PR/prompt preview |
| `internal/picker` | popup picker mode の TUI（repo 選択 → prompt 入力 → dispatch 起動） |
| `internal/tmux` | tmux コマンド実行、session/window/pane 情報取得、mutate 操作（kill）|
| `internal/state` | `/tmp/agent-pane-state` 形式の読み取り |
| `internal/hook` | `/tmp/agent-pane-state` への書き出し（`tmux-sidebar hook` subcommand の本体）。Claude Code hook の stdin JSON を best-effort パースし、`session_id` / `cwd` を抽出する |
| `internal/config` | hidden/pinned の読み込みと書き込み、幅の解決 |
| `internal/repo` | ghq 配下 repo 列挙、fuzzy filter |
| `internal/dispatch` | git worktree 作成 + tmux session 生成 + launcher 起動の deterministic engine（dispatch.sh の Go 移植） |
| `internal/doctor` | tmux/Claude/Codex 設定診断。両 agent の settings ファイル（`~/.claude/settings.json` / `~/.codex/hooks.json`）を `agentTargets` を介して並列にチェックし、不足/legacy/inline-shell/kind 不一致を検出して `--yes` で `tmux-sidebar hook` サブコマンド形式に upgrade する |

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
| `normal` | 単打コマンド（`d`, `D`, `p`, `N` など）、移動キー（`j`/`k`）、`/` で search へ遷移 |
| `search` | 任意文字（クエリに追記）、`Backspace`、`Esc` で normal へ戻る、`Enter` で結果先頭にカーソル |

Model に `inputMode` フィールドを追加し、`handleKey` がモード別の handler を呼ぶ。
search 中も `Ctrl+C` だけは normal mode と同じく terminate を発火する。

confirm dialog は normal mode の **sub-state** として表現する（モード追加ではなく内部フラグ）。これにより key dispatch ロジックを単純に保つ。

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
| 10 秒 tick | Git/PR 更新、active window fallback |
| 1 秒 tick (条件付き) | freshSessionWindow 内の session が存在する間だけ走り、表示の色付けを時間経過で剥がす。残らなくなったら自動停止 |
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

destructive 操作（close 系）は state file の status を読んで confirm 強度を分岐する:

```
status = idle              → "kill window 'X'? [y/N]"
status = running           → "running for Nm — really kill? [y/N]"
status = permission / ask  → "agent is waiting for input — really kill? [y/N]"
                              + 直近 prompt を preview area に表示
```

`D` は対象 session が pinned のとき confirm を出さずブロックする（`requestKillSession` で `cfg.IsPinnedSession` をガード）。`d` も「pinned session の最後の window」のときはブロックする（`requestKillWindow` で `IsPinnedSession && sessionWindowCount(name) <= 1`）。これは tmux の `kill-window` が最後の window を消すと session も消す挙動を持つため、ガードしないと `d` 経由で `D` のブロックがバイパスされてしまうのを防ぐ。message line に「`pinned_sessions` から該当行を削除してから kill」を案内する。pin = 削除保護というユーザのメンタルモデルを実装に反映し、結果として pinned_sessions ファイルに「kill 済み session の残骸」が残ることを構造的に防ぐ。

---

## pin / hidden の合成

pin と hidden は **設定ファイルでのみ管理** する（in-app の toggle キーは提供しない）。
ファイル編集の反映は `loadData()` の起点で `config.Load(...)` を毎回呼び直すことで行う：

- `loadData()` は disk から最新の `hidden_sessions` / `pinned_sessions` を読み、
  結果を `dataMsg.cfg` に積んで Update に渡す
- Update の `dataMsg` ハンドラが `m.cfg = msg.cfg` で in-memory state を refresh する
- 既存の reload 契機（`SIGUSR1` / `gitTickMsg` の 10 秒 tick / 自発 mutate 後）に乗って自動反映

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

## transcript lookup

agent transcript（initial prompt 取得用）の解決は **二段構え**。

1. **session index** (`~/.claude/session-index.jsonl` / `~/.codex/sessions/...`)
   を `pane_N_session_id` で引く
2. index が空 / ENOENT の場合は agent ごとの transcript root を `filepath.WalkDir` し、
   basename で session ID を拾う

| agent | index | walk root | basename match |
|---|---|---|---|
| claude | `~/.claude/session-index.jsonl` | `~/.claude/projects/` | `<sid>.jsonl` 完全一致 |
| codex | （index なし） | `~/.codex/sessions/` | `Contains(<sid>)`（rollout-...-<sid>.jsonl 形式） |

claude 側の walk fallback は **basename 完全一致**（`Contains` ではない）で
`projects/*/<parent>/subagents/agent-*.jsonl` のような subagent transcript の
誤拾いを防ぐ。`WalkDir` のエラーは握りつぶす（symlink loop 対策は標準ライブラリ任せ）。

walk root のデフォルトは `session.DefaultClaudeProjectsDir` /
`session.DefaultCodexSessionsDir` という package 変数で、テストから差し替える。

---

## popup picker mode

### 起動

`tmux-sidebar new` subcommand 自体は popup framing を **持たない**。呼び出し側が `tmux display-popup -E` でラップするかどうかを決める。

tmux.conf 側の bind-key で起動:

```
bind-key N display-popup -E -w 80 -h 24 'tmux-sidebar new'
```

popup の幅・高さは call site で決める（subcommand のハードコードではない）。`tmux-sidebar new` を popup で囲まずに直接実行すると、その場のターミナルで picker TUI が走る（デバッグ・E2E・非 tmux 環境用）。

pane mode 側から `N` 等で popup を起こす経路は持たない（[history.md 参照](./history.md)）。pane mode と picker mode はどちらも同一バイナリだが、別エントリポイント (`tmux-sidebar` / `tmux-sidebar new`) として独立しており、状態は tmux 自身を介してしか共有しない。

### 重複検出

picker は `runNew` 起動時に `tmux.NewExecClient().ListSessions()` を呼んで「現在開いている session 名のリスト」を取得し、`picker.New(repos, openSessionNames, runner)` の引数として渡す。すでに同名 session が存在する repo は dim 表示し、`Enter` 押下時に新規作成ではなく既存 session への `switch-client` を発行する（`Runner.SwitchClient`）。tmux が起動していないなど ListSessions が失敗した場合は空リストで継続し、重複検出だけが skip される。

### picker の状態機械

```
[repo 選択] --(Enter)--> [prompt 入力] --(Enter)--> [dispatch 起動]
       ↑                       ↑
       Esc で取消（exit）       Esc で repo 選択へ戻る
```

### 実行

picker mode は dispatch を背景プロセス (`tmux run-shell -b 'tmux-sidebar dispatch ...'`) として fire-and-forget で起動し、popup を即閉じる（[picker mode の実行ステップ](#picker-mode-の実行ステップ) 節参照）。dispatch 完了の通知は出さず、新 session が sidebar の reload tick (≤10s、SIGUSR1 で即時) に乗って出現することそのものが完了サインになる。失敗時のみ dispatch 側の `runDispatch` エラーハンドラが `tmux display-message` で通知する。

---

## picker mode の実行ステップ

picker mode は 2-step で固定する（dispatch_launcher.fish と同じ構成）。

| Step | UI | 操作 |
|---|---|---|
| 1: repo 選択 | ghq repo list、ヘッダーに current launcher 表示。normal / search の 2 モード（`pickerMode`）を持ち、search 時のみクエリ行を出す | normal: `j`/`k`/矢印で移動、`/` で search 進入、`Tab` で launcher toggle、`Enter` で次へ（既存 session ありなら switch） / search: 任意文字でフィルタ、`Esc` で query クリア + normal 復帰 |
| 2: prompt 入力 | `claude / codex  <repo>` ヘッダー + `> ` 入力欄 | `Tab` で launcher 再 toggle、`Enter` で dispatch 実行、`Esc` で Step 1 に戻る |

Step 1 を modal 化したのは、「j/k で一覧を眺める」navigation 主体の使い方を default にするため（fzf 風の常時 auto-search だと普通の文字キーが入力扱いになり、ナビゲーションキーと衝突する）。`pickerMode` enum は `internal/picker` に閉じる：sidebar の `inputMode` と意味的に対応するが、両者は独立進化させたい責務分離。Step 2 (`stepPrompt`) は text 入力専用なので mode を持たない（Step 1 での mode は `Esc` で戻ってきても維持する）。

| 責務 | 担当 |
|---|---|
| ghq repo 列挙 | `internal/repo`（ghq の出力を直接呼ぶ） |
| repo 表示・filter UI | `internal/picker`（picker mode TUI） |
| 既存 session 検出 + switch | `picker.Runner.SwitchClient` |
| dispatch 実行（worktree + session + launcher + prompt） | `picker.Runner.SpawnDispatch` → `tmux run-shell -b 'tmux-sidebar dispatch ...'` を fire-and-forget で起動 → `internal/dispatch.Launch`（別プロセス）|

picker は dispatch 完了を **待たない**。`SpawnDispatch` が tmux server 内に dispatch process を投げた瞬間に popup を閉じる。worktree 作成や git fetch、tmux session 生成といった重い処理（数秒）はユーザを待たせず、完了時の Switch で client が新 session に切り替わる。dispatch 中のエラーは spawn された `tmux-sidebar dispatch` 側で `tmux display-message` を呼んで通知する（main.go の runDispatch エラーハンドラが担当、stderr が `tmux run-shell -b` で破棄される対策）。

mode 選択 step（claude/codex/dispatch を radio で選ぶ）は持たない。「素の claude / codex 起動」mode は廃止し、picker からは常に dispatch（worktree + prompt）経由で session を作る。素起動が欲しい場合は CLI 直叩き（`tmux-sidebar dispatch <repo> --no-prompt --launcher claude`）か tmux native の `prefix+c` を使う。

dotfiles 側の `prefix+S` (dispatch_launcher.fish) と popup tmw キーバインドは互換のため残す（sidebar 未起動時 / fish ユーザの慣れたフロー用）。

## dispatch engine の責務分担

`internal/dispatch` は dotfiles の `dispatch.sh launch` を Go で再実装した deterministic engine。dispatch には 2 つの利用経路があり、両方が同じ engine を呼ぶことで挙動が一致する:

| 経路 | UI 層 | engine |
|---|---|---|
| `/dispatch` slash command（Claude session 内） | LLM が引数解釈・branch 名生成・in-session 判断 | `dispatch.sh` → 将来的に `tmux-sidebar dispatch` |
| `prefix+S` (dispatch_launcher.fish) | fish + fzf による repo 選択 + prompt 入力 | 同上 |
| `tmux-sidebar new` (tmux.conf bind-key 経由の picker mode) | bubbletea による repo 選択 + prompt 入力 | `internal/dispatch.Launch` を直接呼ぶ（`tmux run-shell -b` で背景起動） |

責務の境界:
- LLM / UI が決めるもの: repo, prompt, in-session か新規 session か, branch 名の **明示指定**（`:<branch>` の checkout 指定）
- engine が決めるもの: branch 名の **暗黙生成**（`Branch == ""` のとき `DeriveBranch` が `claude -p` → slugify フォールバックで決定）、ghq 短縮名解決, worktree 命名規則 (`<main>@<branch-dirname>`), 既存 branch checkout / 新規 branch 作成, `.claude/settings.local.json` コピー, tmux session 名衝突回避, codex の attached client 待ち, prompt-file injection

branch 名生成を engine 側に置く理由は popup の fire-and-forget を維持するため。`claude -p` のレイテンシ（~1-5s）を popup process に乗せると Phase 4 で得た「Enter から < 300ms で popup が閉じる」体験が壊れるので、`tmux run-shell -b` で spawn された dispatch process が background で命名する。`Namer` は `dispatch.Launch(opts, namer)` の引数として渡され、`Options` には serializable な値しか入れない方針を保つ（`ToArgs()` の対象から外す）。

LLM 出力は `^(feat|fix|chore)/[a-z0-9][a-z0-9-]{1,24}$` で shape 検証する（ハルシネーション・前置き混入対策）。不合格時は `BranchFromPrompt` の決定論的 slugify にフォールバックする無音の二段構え。`claude` CLI 不在 / 認証切れ / timeout でも同じく slugify に落ちるので、launcher を codex 単独で使うユーザでも壊れない。

`tmux-sidebar dispatch <repo> [prompt] [flags]` は dispatch.sh とフラグ互換 (`--launcher`, `--session`, `--window`, `--branch`, `--no-worktree`, `--no-prompt`, `--prompt-file`, `--in-session`)。構造化出力 (`STATUS:` / `SESSION:` / `WINDOW:` / `PANE_ID:` / `REPO:` / `WORK_DIR:` / `BRANCH:`) も同形式。

追加フラグ `--switch` (デフォルト false) を持つ。これは `dispatch.Options.Switch` を立てる薄いフラグで、`createTmuxTarget` 直後に `tmux switch-client -t <session>` を発火する。dispatch.sh は `tmux run-shell -b` 越しに background 実行されてユーザが手動で attach する設計なので switch を持たない。tmux-sidebar の picker も **Switch を立てない**: ユーザが今やっている作業から強制的に新 session に飛ばされるのは押し付けが強すぎる。成功時の通知も意図的に出さず、新 session が sidebar に出現することそのものを完了サインにする（display-message での「launched」表示は数秒で消えるうえ sidebar が真のソースなので情報が二重になり、status line のノイズだけ増えるため）。失敗時のみ main.go の runDispatch エラーハンドラから display-message で通知する。codex の `waitForAttachedClient` は手動 attach までの最大 5 分間、dispatch サブプロセス内で polling して待つ（dispatch.sh CLI と同じ挙動）; ユーザが時間内に attach しなければ codex は OSC 11 背景色問題を抱えたまま起動するが、致命的ではない。`--switch` フラグは CLI 経由の自動化スクリプト等で「明示的に飛びたい」用途のために残してある。

---

## 状態ファイル

外部仕様は `docs/spec.md` を正とする。実装上は `internal/state.FSReader` が以下を読む。

- `pane_N`: 1 行目 status、2 行目 agent kind
- `pane_N_started`: running 開始 epoch 秒
- `pane_N_path`: agent 起動 directory
- `pane_N_session_id`: agent session UUID

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
| `new` | popup picker mode 起動（tmux.conf bind-key + `display-popup -E` でラップして呼び出す） |
| `toggle` | 現在 window の sidebar pane を kill、なければ作成 |
| `focus-or-open` | sidebar があれば focus、なければ作成して focus |
| `close` | 現在 window の sidebar を閉じる |
| `cleanup-if-only-sidebar` | sidebar だけ残った window を削除し、pane 削除後の幅補正 |
| `restart` | 既存 sidebar を kill して同じ window に再作成 |
| `doctor [--yes]` | 設定診断と一部自動修正 |
| `upgrade` | GitHub Releases から最新バイナリを取得 |
| `hook <status> [--kind ...]` | agent (Claude Code / Codex) hook から呼ばれて状態ファイルを書き出す。`internal/hook` への薄い wrapper |

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
| Claude/Codex hook 設定 | `tmux-sidebar hook <status>` を呼ぶ宣言（実際の書き出しは tmux-sidebar 側で行う） |
| tmw | session 生成 + agent 起動 (engine。picker mode から呼ぶ) |
| dispatch / orchestrate skill | mode 実行（picker mode から委譲） |

---

## 既知の制約

- tmux hook が未設定でも基本表示は動くが、window/session 変更の反映は最大 10 秒遅れる
- `client-resized` hook の幅値と runtime config の幅値は自動同期されない
- `gh pr view` は環境の GitHub 認証状態に依存する
- prompt preview は `pane_N_session_id` と transcript 実体（index 経由 or projects walk fallback で解決）が両方揃った場合のみ
- 完全な undo close は提供しない（kill 直前 confirm + scrollback 退避 path 通知のみ）
- popup picker は tmux popup の機能に依存する（tmux 3.2 以上）
