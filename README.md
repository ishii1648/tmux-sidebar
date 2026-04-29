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

`tmux.conf` および agent hook の設定手順は [docs/setup.md](docs/setup.md) を参照してください。最低限 §1 と §2 を設定すればサイドバーは動作します。

- [§1. サイドバー自動生成（必須）](docs/setup.md#1-サイドバー自動生成必須)
- [§2. sidebar への誤フォーカス防止 + カーソル追従通知（必須）](docs/setup.md#2-sidebar-への誤フォーカス防止--カーソル追従通知必須)
- [§3. サイドバーのみ残ったウィンドウの自動削除（推奨）](docs/setup.md#3-サイドバーのみ残ったウィンドウの自動削除推奨)
- [§4. ディスプレイ移動時のサイドバー幅維持（推奨）](docs/setup.md#4-ディスプレイ移動時のサイドバー幅維持推奨)
- [§5. SIGUSR1 による即時更新通知（推奨）](docs/setup.md#5-sigusr1-による即時更新通知推奨)
- [§6. toggle キーバインド（任意）](docs/setup.md#6-toggle-キーバインド任意)
- [§7. サイドバーへのフォーカスキーバインド（任意）](docs/setup.md#7-サイドバーへのフォーカスキーバインド任意)
- [§8. Agent (Claude Code / Codex CLI) の状態ファイル（任意）](docs/setup.md#8-agent-claude-code--codex-cli-の状態ファイル任意)

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

最小値は `20`。範囲外や不正値はデフォルト `40` にフォールバックします。[setup.md §1](docs/setup.md#1-サイドバー自動生成必須) と [§4](docs/setup.md#4-ディスプレイ移動時のサイドバー幅維持推奨) の tmux.conf 側の数値も合わせて変更してください。

## License

MIT
