# Popup picker と `claude agents` のディスパッチ入力の役割整理

Created: 2026-05-13
Model: Opus 4.7

## 概要

`tmux-sidebar new` (popup picker, `internal/picker`) は **repo 選択 → prompt 入力 → dispatch 起動** の 2-step wizard で、Claude / Codex のいずれかを launcher に選ぶ。Claude Code v2.1.139+ のエージェントビューは画面下部の入力欄で同等のディスパッチを行え、`@<repo>` でディレクトリ指定、`/<skill>` でスキル起動、`@<agent-name>` で subagent 指定、画像貼り付けにも対応する。Claude を選んだときの popup picker と機能が広範に重複しているため、popup picker の存在意義を整理し、残すべき独自価値を spec に明文化する。

## 根拠

- 公式 (https://code.claude.com/docs/ja/agent-view) の dispatch 機能:
  - 「エージェントビューの下部の入力にプロンプトを入力して `Enter` を押すと、新しいバックグラウンドセッションが開始されます。」
  - `<agent-name> <prompt>` / `@<agent-name>` / `@<repo>` / `/<skill>` / `#<number>` のプレフィックス記法
  - 「プロンプトに画像を貼り付けて、タスクにスクリーンショットまたは図を含めます。」
  - 「複数のリポジトリを保持する親ディレクトリで `claude agents` を開き、プロンプトで `@<repo>` を使用して 1 つを言及してセッションをそこで実行します。」
- 本リポジトリの popup picker の機能 (`docs/spec.md` Popup picker mode 節, `internal/picker/picker.go`):
  - ghq 配下 repo の **fuzzy filter** + dim 表示で「既存 session があればそこに switch」
  - `Tab` で launcher (claude / codex) 切替
  - prompt 入力 — bracketed paste / 改行 / soft-wrap / `:<branch>` checkout モード
  - 背景 dispatch (`tmux run-shell -b`) で popup を即閉じる (`<300ms`)
  - branch 命名は `claude -p` → slugify フォールバック (#0007 と連動)
  - tmux session 作成 + Codex 経路サポート
- 重複する部分: **Claude launcher を選んだときの prompt 入力 → 背景 dispatch** のフローはエージェントビュー側でほぼ同じ体験。
- popup picker にしか無い価値:
  - **ghq 列挙** (claude agents は親ディレクトリの兄弟だけ)
  - **Codex CLI launcher**
  - **重複セッション検出 → switch-client** (claude agents は claude セッションのみ)
  - **`:<branch>` checkout モード** (既存 branch を idle で開く)
  - **tmux session 名で重複ハンドリング** (`SessionExplicit` 等)
- **Agent View の dispatch スコープ制約** (公式 docs「特定のディレクトリにディスパッチする」節):
  - dispatch は「開いたディレクトリ + その直接の子 (`@<repo>` の basename match)」に縛られる
  - ghq の `<host>/<owner>/<repo>` 3 階層を flat に横断する手段が無い
  - 結果として「Claude を ghq 配下の任意 repo にディスパッチしたい」ニーズは popup picker でしか満たせない
  - list 自体は machine-wide (`~/.claude/jobs/` 経由) で見えるが、dispatcher と viewer は分離されている
- launcher 対称性 (Claude/Codex を同一 UX で扱う) は `tmux-sidebar new` の核心価値。Agent View 側は Claude 専用なので、popup を Codex 専用化すると非対称が生まれる。

## 対応方針

**採用: 案 D — 差別化を spec.md に明文化** (2026-05-13 確定)。

Agent View がリサーチプレビュー段階であること、ghq の 3 階層 namespace を Agent View が解けないこと、launcher 対称性 (Codex 含む) と tmux native 統合の独自価値、`disableAgentView` の組織制約への耐性、これらを総合して **コードは変えず、`docs/spec.md` で「`tmux-sidebar new` = repo 横断 dispatcher、`claude agents` = repo 内 dispatcher」の住み分けを宣言する** に倒す。#0007 / #0008 / #0009 を pending にしたのと整合する「経過観察 + 最低限の衝突回避」方針の一部。

棄却した案:

| 案 | 棄却理由 |
|---|---|
| A. 現状維持 (spec 沈黙) | ユーザが「どっち使うべき?」で迷う状態を放置してしまう。最低限の衝突回避としての明文化は必要 |
| B. Codex 専用化 | ghq 横断 dispatch の手段が消える、launcher 対称性が崩れる、リサーチプレビューに依存する判断になる |
| C. `claude --bg` の wrapper 化 | `@<repo>` が ghq の `<host>/<owner>/<repo>` を解けない見込みで wrapper として破綻、リサーチプレビューに依存する |

将来 Agent View が GA になり `@<host>/<owner>/<repo>` を解くようになったら本 issue を reopen して再評価する。

## 変更箇所

案 D 採用に伴うコード変更はなし。ドキュメントのみ:

- `docs/spec.md` — 「Agent View との分担」節を新設、`tmux-sidebar new` (= repo 横断 dispatcher) と `claude agents` (= repo 内 dispatcher) の使い分け早見表を追加
- `README.md` — Features 一覧の Popup picker 説明に「Agent View では出来ない ghq 横断 dispatch を担う」一文を添える (任意、spec への導線として)
- `docs/history.md` — Agent View 登場時に「ghq 中心構造の再評価を保留し、リサーチプレビュー期間中は経過観察 + 衝突回避に徹する」と append (3 つの pending issue #0007〜#0009 と合わせて、方針を一括記録)

## 実装チェックリスト

- [ ] `docs/spec.md` に「Agent View との分担」節を追加 (使い分け早見表、住み分け宣言)
- [ ] `docs/history.md` に経過観察方針を append (#0007〜#0009 の pending 化と並べて、Agent View 全体への姿勢として記録)
- [ ] `README.md` の Features 説明を必要に応じて補強
- [ ] 再評価トリガを spec / history に明記 (Agent View GA、`@<host>/<owner>/<repo>` の階層解決対応、`disableAgentView` の市場浸透度)
