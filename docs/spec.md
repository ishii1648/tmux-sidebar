# tmux-sidebar 仕様

この文書はユーザ視点の振る舞いを記述する。実装方法や設計判断は
`docs/design.md`、過去の経緯は `docs/history.md` に記載する。

---

## 概要

tmux-sidebar は tmux の左端 pane に全 session/window の一覧と
agent (Claude Code / Codex CLI) の状態を表示し、キーボードで選択した
window へ移動できる TUI ツールである。

sidebar は tmux hook により新しい window/session に自動生成できる。
手動で開閉・focus するための CLI サブコマンドも提供する。

---

## 表示

sidebar は session を見出し、window を子行として階層表示する。
agent がいない window も表示対象である。

```
● Sessions
> type to filter...
▾ work
  1: main
▶ 2: feature                 [c]🔄3m
▾ infra
  1: deploy                  [x]💬
```

現在の tmux window は背景色で highlight される。
sidebar pane が focus されている場合は title が `● Sessions`、
focus されていない場合は `○ Sessions` になる。

表示から除外したい session は `~/.config/tmux-sidebar/hidden_sessions` に
1 行 1 session name で指定する。

---

## Agent 状態バッジ

agent 状態がある window には、右端に agent tag と status badge を表示する。

### Agent tag

| Tag | 意味 |
|---|---|
| `[c]` | Claude Code。unknown / legacy state も fallback として `[c]` |
| `[x]` | Codex CLI |

### Status badge

| Status | 表示 |
|---|---|
| `running` | `🔄Ns` または `🔄Nm` |
| `idle` | status badge は表示しない |
| `permission` | `💬` |
| `ask` | `💬` |

1 分未満の running は秒表示、1 分以上は分表示にする。

---

## キーボード操作

sidebar pane に focus がある時だけ、以下のキーを処理する。

| キー | 動作 |
|---|---|
| `j` / `↓` | 次の window 行へ移動。検索クエリが空の時のみ |
| `k` / `↑` | 前の window 行へ移動。検索クエリが空の時のみ |
| `Enter` | 選択 window へ移動 |
| 任意の文字 | session name / window name のインクリメンタル検索 |
| `Backspace` | 検索クエリを 1 文字削除 |
| `Esc` | 検索クエリをクリア |
| `Ctrl+C` | sidebar process を終了 |

`q` は特別扱いしない。検索クエリとして入力される。
`Esc` は sidebar pane から focus を外す操作ではない。

---

## Window 移動

`Enter` を押すと、選択中 window の session name と window index を使って
tmux client を対象 window に移動する。

session header 行は選択対象ではない。
cursor は window 行だけを移動する。

---

## 検索

文字入力すると検索クエリになり、session name または window name に
case-insensitive substring match する window だけを表示する。

検索結果がある session header だけを表示する。
`Esc` で検索クエリを空に戻す。

---

## Prompt preview

選択中 window の agent state に `pane_N_session_id` があり、対応する Claude transcript が
見つかる場合、sidebar 下部に initial prompt preview を表示する。

prompt がない、transcript が見つからない、session id がない場合は preview を空にする。

---

## Git/PR 表示

window の作業 directory が git repository の場合、branch / ahead / PR 情報を表示する。
`gh` が利用でき、対象 branch に PR がある場合は PR 番号と state を表示する。

Git/PR 表示は補助情報であり、取得に失敗しても sidebar の一覧表示や移動は継続する。

---

## Sidebar の開閉と focus

| サブコマンド | 動作 |
|---|---|
| `tmux-sidebar` | TUI sidebar を起動 |
| `tmux-sidebar toggle` | 現在 window の sidebar を閉じる。なければ開く |
| `tmux-sidebar close` | 現在 window の sidebar を閉じる |
| `tmux-sidebar focus-or-open` | sidebar があれば focus、なければ開いて focus |
| `tmux-sidebar cleanup-if-only-sidebar` | sidebar だけが残った window を削除 |
| `tmux-sidebar restart` | 既存 sidebar を再起動 |
| `tmux-sidebar doctor [--yes]` | 設定を診断し、`--yes` 付きなら一部を自動修正 |
| `tmux-sidebar upgrade` | GitHub Releases から最新バイナリを導入 |
| `tmux-sidebar version` | version を表示 |

sidebar pane は tmux pane option `@pane_role=sidebar` で識別する。

---

## tmux hook

README の Setup を正とする。代表的な hook は以下。

- `after-new-window`: 新しい window に sidebar を自動生成する。
- `after-new-session`: new-session の初期 window に sidebar を自動生成する。
- `after-select-window`: sidebar に window 切替を通知し、誤 focus を右隣 pane に逃がす。
- `client-session-changed`: session 切替時も同様に通知・focus guard する。
- `pane-exited` / `after-kill-pane`: sidebar だけ残った window を削除する。
- `client-resized`: sidebar 幅を絶対セル数で再適用する。
- `window-linked` / `window-unlinked` / `session-created` / `session-closed`: sidebar に即時更新を通知する。

即時更新 hook が未設定でも、window/session 変更は最大 10 秒で sidebar に反映される。

---

## 状態ファイル

デフォルトの状態 directory は `/tmp/agent-pane-state`。
`TMUX_SIDEBAR_STATE_DIR` で変更できる。

| ファイル | 内容 |
|---|---|
| `pane_N` | 1 行目: status (`running` / `idle` / `permission` / `ask`)、2 行目: agent kind (`claude` / `codex`) |
| `pane_N_started` | `running` 開始時刻の unix epoch 秒 |
| `pane_N_path` | agent session の起動 directory |
| `pane_N_session_id` | agent session UUID |

`pane_N` の 2 行目がない、または未対応値の場合は unknown として扱う。
unknown agent は表示上 `[c]` に fallback する。

---

## 設定

### Environment variables

| 変数 | 説明 |
|---|---|
| `TMUX_SIDEBAR_STATE_DIR` | 状態ファイル directory。未設定時は `/tmp/agent-pane-state` |
| `TMUX_SIDEBAR_WIDTH` | sidebar 幅。未設定時は config file、さらに未設定なら `40` |
| `TMUX_SIDEBAR_NO_ALT_SCREEN` | 設定すると alt-screen を無効化する。主に E2E テスト用 |

### `~/.config/tmux-sidebar/hidden_sessions`

sidebar に表示しない session name を 1 行 1 entry で指定する。
空行と `#` 始まりの行は無視する。

### `~/.config/tmux-sidebar/width`

sidebar の既定幅を列数で指定する。
`TMUX_SIDEBAR_WIDTH` が指定されている場合はそちらを優先する。

最小値は `20`。未設定、不正値、最小値未満の場合は `40` に fallback する。

---

## 非目標

- tmux plugin manager に依存しない。
- sidebar 幅のドラッグ変更は提供しない。
- tmux.conf 内の `split-window -l` / `resize-pane -x` の数値は自動で書き換えない。
- `Esc` や `q` による tmux pane focus 離脱は提供しない。
