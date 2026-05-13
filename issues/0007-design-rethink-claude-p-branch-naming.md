# `claude -p` を使った branch 命名の位置付け見直し

Created: 2026-05-13
Model: Opus 4.7

## 概要

`internal/dispatch/branch.go` の `ClaudeNamer` は `claude -p --system-prompt <branch-naming-prompt> <user>` を fork して branch 名を生成している。Claude Code v2.1.139+ で導入された **エージェントビュー** がセッションを Haiku クラスモデルで自動命名するようになり、Claude 側に同種の「短い summary 生成」インフラが組み込まれた。本 issue では `ClaudeNamer` 経路をそのまま維持するか、廃止して決定論的 slug (`BranchFromPrompt`) のみに絞るか、あるいは別経路に置き換えるかを判断する。

## 根拠

- `internal/dispatch/branch.go:147-200` で `claude -p` をサブプロセス起動し、5 秒タイムアウト → fallback slugify という二段構えになっている。
- ドキュメント (https://code.claude.com/docs/ja/agent-view) は次の点を述べる:
  - 「セッションはプロンプトから自動的に名前が付けられます。後で `Ctrl+R` で名前を変更できます。」(セッション命名は Haiku ベースで claude 側が責任を持つ)
  - 「各更新は通常のプロバイダーを通じた 1 つの短い Haiku クラスリクエストであり、セッション自体と同じデータ使用条件の下で請求および処理されます。」(Haiku が公式の short summary 生成エンジン)
- `ClaudeNamer` の動機 (`docs/design.md` の dispatch engine 節) は「popup の fire-and-forget を維持するため、`claude -p` のレイテンシ (~1-5s) を background dispatch process に追い出す」というもの。これは `claude` を sonnet モデルで叩く前提だが、命名のためなら本来 Haiku 相当で十分。
- branch 名と session 名は別概念だが、ユーザにとっては「Haiku が tmux session で勝手に短い名前を付けてくれる」感覚として等価で、二箇所で別ロジックが動いていることは将来的に「branch 名と claude agents 上の session 名がズレる」原因になりうる。
- 副次的に、`claude -p` の呼び出しはユーザのサブスクリプションクォータを消費し、認証切れ/未認証ユーザでは黙ってフォールバックする (`branch.go:174-180`)。Haiku 相当の小さなリクエストに切り替えられる経路があるならそちらが軽い。

## 対応方針

以下のいずれかから選ぶ。決定前に `claude -p` のモデル指定や Haiku 直叩きの可否、エージェントビュー側で命名済みの値を外部から取れる API があるかを確認する。

| 案 | 内容 | メリット | デメリット |
|---|---|---|---|
| A. 現状維持 | `ClaudeNamer` を残す | 既存テスト / dispatch.sh との互換維持 | 命名ロジックが claude 内蔵と二重になる |
| B. 廃止 | `ClaudeNamer` を消し `BranchFromPrompt` の slugify のみに | コードが小さくなる、CLI 依存削減、`claude -p` のクォータ消費なし | LLM 命名のリッチさを失う (`feat/add-tenant-module` のような型推定がなくなる) |
| C. Haiku 直叩き | `claude -p --model claude-haiku-4-5` 等で軽量モデルに切替 | レイテンシ短縮、コスト削減、現状の二段構えはそのまま | claude CLI のモデルフラグ仕様に依存 |
| D. Agent View に統合 | branch 命名を picker / dispatch から外し、`claude --bg` 経由で claude agents の auto-naming を使う | 命名責務を Claude 側に寄せられる | branch 名と session 名の対応は別途必要、要 spec 整理 |

popup の fire-and-forget 体験 (Enter から <300ms で popup が閉じる、`docs/history.md` Phase 4) を壊さないことが制約。

## 変更箇所

判断によって範囲が変わる。B/C の場合:

- `internal/dispatch/branch.go` の `ClaudeNamer` / `claudeBranchSystemPrompt` 削除 or モデル指定追加
- `internal/dispatch/branch_test.go` のテスト更新
- `internal/dispatch/dispatch.go` で `ClaudeNamer{}` を渡している箇所
- `docs/design.md` の dispatch engine 節
- 方向反転を伴うので `docs/history.md` も追記対象

## 実装チェックリスト

- [ ] `claude -p` の `--model` フラグ仕様と Haiku 系モデル ID の現行値を確認
- [ ] エージェントビューが生成する session 名を外部 (state.json / API) から取得できるか確認
- [ ] 4 案から方針を決定
- [ ] 必要なら `ClaudeNamer` を差し替え or 削除
- [ ] `docs/design.md` / `docs/history.md` を更新
