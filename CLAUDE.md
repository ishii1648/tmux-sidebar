# tmux-sidebar プロジェクトガイドライン

## ドキュメント運用（3 本柱）

この repo では ADR を作らず、以下の 3 文書で設計・振る舞い・経緯を分担している。
変更時は **役割を混ぜずにそれぞれを更新** すること。

| 文書 | 役割 | 性格 | 更新タイミング |
|---|---|---|---|
| `docs/spec.md` | ユーザ視点の振る舞い（what） | 現在地のスナップショット | user-visible な仕様変更があるたび |
| `docs/design.md` | 実装上の設計（how） | 現在地のスナップショット | 実装構造・責務分担が変わるたび |
| `docs/history.md` | 過去の判断と分岐（why） | append-only の歴史 | 方向反転・前提変更を伴う変更時のみ |

### 原則

- spec / design は **常に最新スナップショット**。過去の経緯は書かない（history へ）
- history は **append-only**。既存セクションは原則書き換えない（過去の判断は過去のまま残す）
- 「採用しなかった代替」と「却下理由」は history にしか残せない情報なので、方向反転時は必ず書く
- リリースノート（What の伝達）は goreleaser の自動生成に任せ、CHANGELOG.md は持たない

### history.md に書くべき変更の判定

| 種別 | history への記録 |
|---|---|
| バグ修正、性能改善、依存更新 | 不要 |
| 既存設計の自然な延長（feature 追加） | 不要 |
| **既存方針の反転 / 設計前提の変更** | **必須** |
| 検討したが採用しなかった代替がある | 必須（rationale を残す） |
| 内部リファクタで設計上の大きな再編 | 任意 |

判断に迷ったら「半年後の自分が `git log` ではなく文書で読みたい情報か？」を基準にする。

## issues について

issue は **すべて `issues/` 以下の markdown ファイル** で管理する。命名規則・フォーマット・ワークフローの詳細は [issues/CLAUDE.md](issues/CLAUDE.md) を参照。

**禁止事項**:

- ユーザが「issue を追加して」「issue 作って」等と言ったら、**常に `issues/` ディレクトリへの markdown 追加** と解釈する。「GitHub issue を作って」と明示されない限り例外なし。
- GitHub Issues を作らない。`gh issue create` / `mcp__github__issue_write` などの GitHub Issues 系コマンド・MCP ツールも使わない（`gh pr` / PR 系の MCP は対象外）。
- GitHub MCP ツールが環境で利用可能でも、issue 操作には呼ばない。

`issues/` 以下のファイルは **1 issue 1 commit** で `issues/SEQUENCE` の +1 と同時にコミットする（[issues/CLAUDE.md](issues/CLAUDE.md) のワークフロー表を参照）。

## 実装後の動作確認

実装・修正が完了したら、必ず `/verify-implementation` を実行して動作確認を行ってから完了を報告すること。
