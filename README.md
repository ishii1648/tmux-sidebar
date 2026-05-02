# tmux-sidebar

tmux の **cross-context 軸（session / window）** を司る常駐 control surface。
左端 sidebar pane に全 session/window と agent (Claude Code / Codex CLI) の状態を一覧表示し、
キーボードで switch / close / pin などのライフサイクル操作を発行する。
新規 session 生成は sidebar 起動の popup picker（repo + agent mode 選択）で完結する。

> **Note**: 現時点で出荷済みの機能は「全 session/window 一覧 + 状態バッジ + Enter 移動 + 検索」まで。
> 上記の lifecycle 操作 / popup picker は実装中。詳細は [docs/spec.md](docs/spec.md)（目標仕様）と [docs/TODO.md](docs/TODO.md)（実装計画）を参照。

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

### 出荷済み

- 全セッション・ウィンドウを階層表示（agent がいないウィンドウも含む）
- agent (Claude Code / Codex CLI) の状態バッジ: 行頭に agent タグ (`[c]` / `[x]`)、続けて状態絵文字 (`🔄Nm` / `💬`)
- `j` / `k` / `↑` / `↓` でカーソル移動、`Enter` で対象ウィンドウへジャンプ
- 任意の文字を入力するとインクリメンタル検索フィルタが効く（`Esc` でクリア）
- `after-new-window` フックで新しいウィンドウに自動生成
- 選択ウィンドウの agent transcript から initial prompt を下部にプレビュー
- Git ブランチ / PR 番号の表示

### 実装中（roadmap）

- vim 風 modal 入力（`/` で検索モード、normal モードで単打コマンド）
- window/session の close (`d`/`D`)
- pin の永続化（`p`）
- `N` で popup picker mode 起動 → ghq repo + agent mode (`claude` / `codex` / `dispatch` / `orchestrate`) 選択

詳細は [docs/spec.md](docs/spec.md) と [docs/TODO.md](docs/TODO.md) を参照。

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

| サブコマンド | 説明 | 状態 |
|---|---|---|
| (なし) | TUI サイドバー（pane mode）を起動 | 出荷済み |
| `new [--context=<file>]` | popup picker mode を起動（通常は sidebar の `N` から間接実行） | 実装中 |
| `close` | サイドバーを閉じる | 出荷済み |
| `toggle` | サイドバーの表示/非表示を切り替え | 出荷済み |
| `focus-or-open` | サイドバーがあればフォーカス、なければ作成 | 出荷済み |
| `cleanup-if-only-sidebar` | sidebar のみ残ったウィンドウを削除 | 出荷済み |
| `restart` | 全 tmux ウィンドウのサイドバーペインを kill して再生成（バイナリ更新後に使う） | 出荷済み |
| `doctor [--yes]` | tmux 設定をチェック（`--yes` で自動修正） | 出荷済み |
| `upgrade` | GitHub Releases から最新バイナリをダウンロードしてインストール | 出荷済み |
| `version` | バージョンを表示 | 出荷済み |

## Keyboard shortcuts

### 出荷済み

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

### 実装後の目標仕様

入力モデルは `normal` / `search` の 2 モードに分かれます。

| モード | 動作 |
|---|---|
| `normal` | 単打キーで commands 発行（switch / close / pin など） |
| `search` | `/` で進入、`Esc` で normal へ戻る |

normal モードの主なキー:

| カテゴリ | キー | 動作 |
|---|---|---|
| 移動 | `j`/`k`, `Tab`/`Shift+Tab` | 行移動、フィルタ切替 |
| 切替 | `Enter` | 選択 window へ移動 |
| Lifecycle | `d` / `D` | window/session の close（state に応じた confirm 強度） |
| Lifecycle | `N` | popup picker で新規 session |
| 装飾 | `p` | pin toggle |

詳細は [docs/spec.md](docs/spec.md) を参照。

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

すべて `~/.config/tmux-sidebar/` 配下、1行1エントリ、`#` でコメント。

| ファイル | 内容 | 状態 |
|---|---|---|
| `hidden_sessions` | 表示しないセッション名 | 出荷済み |
| `pinned_sessions` | pin するセッション名（行順 = 表示順） | 実装中 |

### hidden_sessions

```
# 表示対象外にするセッション名（1行1エントリ、# はコメント）
main
```

上記の例では `main` セッションがサイドバーのセッション一覧から非表示になります。ファイルが存在しない場合は全セッションが表示されます。

幅は環境変数 `TMUX_SIDEBAR_WIDTH` で設定します（デフォルト `40`、最小 `20`）。[setup.md §1](docs/setup.md#1-サイドバー自動生成必須) と [§4](docs/setup.md#4-ディスプレイ移動時のサイドバー幅維持推奨) の tmux.conf 側の数値も合わせて変更してください。

## License

MIT
