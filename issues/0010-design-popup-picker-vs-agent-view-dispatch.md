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

## 対応方針

| 案 | 内容 | メリット | デメリット |
|---|---|---|---|
| A. 現状維持 | popup picker は両 launcher を引き続きサポート | 既存ユーザ体験を変えない | 機能重複が増える、ユーザに「どっち使うべき?」を強いる |
| B. Codex 専用化 | popup picker から Claude launcher 選択肢を消し、Claude は `claude agents` に誘導 | UI 分担が明確、コードがシンプル | Claude ユーザは ghq 経由の repo 選択を失う、`:<branch>` checkout モードも失う |
| C. Wrapper 化 | Claude 選択時は内部で `claude --bg "@<repo> <prompt>"` 相当を発火 | ghq 列挙 + supervisor 利点の両取り | `@<repo>` 解決が `ghq list -p` 配下のパスと衝突しないか要検証 |
| D. 差別化を明文化 | spec.md に「popup picker と claude agents の使い分け表」を追記し、両者を並走 | 仕様の交通整理ができる | コードは変わらない (case A と同等) |

#0008 (Claude launcher を `claude --bg` に寄せる) と方針整合が必要。0008 で「Claude 経路は廃止」を選ぶなら本 issue も自動的に B / C 寄りに倒れる。

## 変更箇所

B / C を採る場合:

- `internal/picker/picker.go` — Claude launcher 分岐を削除 or `claude --bg` 呼び出しへ
- `internal/picker/picker_test.go` — Tab toggle のテストを更新
- `internal/dispatch/dispatch.go` — Claude 経路 (#0008 と連動)
- `docs/spec.md` の「Popup picker mode」節 — launcher 選択ステップの記述
- `README.md` の Features 一覧から claude 経路の説明を調整
- `docs/history.md` — 方針反転として append

## 実装チェックリスト

- [ ] #0008 (Claude launcher の去就) と整合する方針を決定
- [ ] `claude agents` の `@<repo>` 解決ルールが ghq の repo パスを引けるか実機確認
- [ ] picker に残す独自機能 (`:<branch>` checkout, dim 重複検出, codex) のスペックを再確認
- [ ] spec.md の Popup picker mode 節を更新
- [ ] 案 A or D 採用なら spec に「使い分け早見表」を追加
