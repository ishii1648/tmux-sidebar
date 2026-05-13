# Claude 状態取得を `~/.claude/jobs/<id>/state.json` 経由に寄せる検討

Created: 2026-05-13
Model: Opus 4.7

## 概要

現状、Claude セッションの状態 (running / idle / permission / ask) は **tmux-sidebar が登録する hook** が `/tmp/agent-pane-state/pane_N` を書き出すことで sidebar に伝わる (`internal/hook`, `internal/state`)。Claude Code v2.1.139+ の supervisor model では Claude 自身が `~/.claude/jobs/<id>/state.json` にセッション状態を書き出している。**Claude 部分だけ** は公式の state.json を読みに行く方式に寄せられないかを検討する。Codex は引き続き hook ベースを維持する前提。

## 根拠

- 公式 (https://code.claude.com/docs/ja/agent-view) より:
  - 「セッション状態は Claude Code 設定ディレクトリの下に保存されます。`CLAUDE_CONFIG_DIR` を設定した場合、スーパーバイザーは `~/.claude` の代わりにそのディレクトリを使用し、独自のセッションを持つ別のインスタンスとして実行されます。」
  - 状態ファイル一覧:
    | パス | 内容 |
    |---|---|
    | `~/.claude/daemon.log` | スーパーバイザーログ |
    | `~/.claude/daemon/roster.json` | 実行中のバックグラウンドセッションのリスト |
    | `~/.claude/jobs/<id>/state.json` | エージェントビューに表示されるセッションごとの状態 |
  - 状態の語彙: 「作業中 / 入力が必要 / アイドル / 完了 / 失敗 / 停止」(現状 sidebar は running / idle / permission / ask の 4 種)
- 本リポジトリ側:
  - `internal/state/state.go:11-19` — `/tmp/agent-pane-state/pane_N` を Reader が読む。コメント「ADR-063 hooks (Claude Code / Codex CLI 両対応)」。
  - `internal/hook/hook.go:25-50` — `tmux-sidebar hook <status>` で Claude / Codex の hook から呼ばれて書き出す。Claude Code の hook JSON (`session_id` / `cwd`) を best-effort パース。
  - `internal/doctor/doctor.go:137` — `~/.claude/settings.json` のあるべき hook 設定を診断・自動補正している。
- 公式 state があるなら、Claude については (a) hook をユーザに設定させる必要がなくなる (b) `tmux-sidebar hook` 経由でしか取れない情報を別途取れる可能性 (例: completion, failure) (c) `CLAUDE_CONFIG_DIR` を変えるユーザにも自動で追従できる。
- 一方、`pane_N` は **tmux pane 単位** で索引するのに対し、`jobs/<id>` は **claude session 単位**。tmux pane と claude session の対応付けは `pane_N_session_id` から逆引きできる (sidebar 側に既に session ID は来ている)。
- `state.json` のスキーマは public API として保証されていない可能性がある (リサーチプレビューなので変更されうる) — 結合度の判断が必要。

## 対応方針

| 案 | 内容 | メリット | デメリット |
|---|---|---|---|
| A. 現状維持 | hook ベースのまま | 公式仕様変更の影響を受けない | hook 未設定ユーザは状態が出ない、claude 内蔵情報と二重 |
| B. 公式 state を併読 | hook を primary、`jobs/<id>/state.json` を fallback / 補強 | 段階移行、リッチな状態 (completed / failed) を表示できる | 二経路の整合が必要 |
| C. 公式 state を primary | `~/.claude/jobs/<id>/state.json` を主、hook は Codex 専用に縮退 | doctor が Claude 側 hook を促す必要がなくなる、`CLAUDE_CONFIG_DIR` 自然対応 | スキーマ変更で壊れる可能性、リサーチプレビュー依存 |
| D. roster + supervisor 連携 | `~/.claude/daemon/roster.json` も読み、tmux に紐付かない bg セッションも sidebar に表示 | #0008 (claude --bg 経路) と一貫、sidebar の "cross-context" 概念を背景セッションに拡張 | spec.md に「pane に紐付かないセッション」概念の追加が必要 |

## 変更箇所

判断によるが C / D を採る場合:

- `internal/state/` — Claude セッションを `jobs/<id>/state.json` から読む `JobsReader` 等を追加
- `internal/hook/hook.go` — Claude kind の処理を `--kind codex` 限定に縮退するか、warning を出す
- `internal/doctor/doctor.go` — `~/.claude/settings.json` の hook 必須化を緩和、`jobs/` 経路の前提に書き換え
- `internal/ui` — completed / failed / stopped の新ステータスバッジを追加 (spec.md の「Status バッジ」表に追記が必要)
- `docs/spec.md` — Status バッジ表、Required external tools、Environment variables (`CLAUDE_CONFIG_DIR` の扱い)
- `docs/design.md` — 状態取得経路の図
- `docs/history.md` — hook ベース → 公式 state ベースは方針反転なので append

## 実装チェックリスト

- [ ] `~/.claude/jobs/<id>/state.json` の実際のスキーマを v2.1.139+ で確認 (status / activity / errored / kind 等のフィールド)
- [ ] `CLAUDE_CONFIG_DIR` 環境変数を尊重するか決める (現状の `/tmp/agent-pane-state` は `TMUX_SIDEBAR_STATE_DIR` で上書き可能、別軸)
- [ ] スキーマが non-stable と判断したら案 B (併読 + fallback) に倒す
- [ ] Codex 経路に副作用がないことを確認
- [ ] `tmux-sidebar hook` をすぐ廃止しない (互換期間が必要)
- [ ] `docs/spec.md` / `docs/design.md` を更新
- [ ] 方針反転を伴うので `docs/history.md` も更新

## pending 理由

2026-05-13: Agent View はリサーチプレビュー段階で、`~/.claude/jobs/<id>/state.json` のスキーマも `~/.claude/daemon/roster.json` の構造も public な安定 API として保証されていない。現状の hook ベースは Claude / Codex 両 launcher を同一フォーマット (`pane_N`) で扱える launcher 対称性を持ち、リソース衝突もない (#0009 検討で確認: 別ファイル / 別パスなので並存可能)。観察ポイント:

- `~/.claude/jobs/<id>/state.json` のスキーマが changelog で public stable と明記されるか
- `daemon/roster.json` が外部読み取り用途として公開されるか
- 「Claude セッションのうち bg 経由で起動されたもの」を sidebar に表示する需要が出るか (現状は tmux pane 内で `claude` を起動するモデルが主なのでニーズが顕在化していない)
- `CLAUDE_CONFIG_DIR` を変更するユーザからの追従要望

再評価のトリガは「Agent View の GA」+「state.json スキーマの安定アナウンス」。それまでは hook ベースを維持し、Codex 経路と対称に保つ。
