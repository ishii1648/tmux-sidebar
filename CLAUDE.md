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

GitHub Issues は使わず、`issues/` 以下に markdown 形式で issue を管理する。バグ報告・機能要望・設計課題はすべてここで完結させる。

### ディレクトリ構成

| パス | 用途 |
|---|---|
| `issues/` | active な issue（対応中・対応待ち） |
| `issues/closed/` | 完了済 issue（完了時に `git mv` で移動） |
| `issues/pending/` | 外部依存・設計判断待ちで凍結中の issue（close しない） |
| `issues/SEQUENCE` | 次に発番する番号。issue 作成時に +1 して同じコミットに含める |

### ファイル命名規則

`{seqnum}-{category}-{short-description}.md` 形式で固定する:

- `seqnum`: `issues/SEQUENCE` の現在値、4 桁ゼロパディング（9999 を超えたら 5 桁）
- `category`: コミットプレフィックスと揃える（`feat` / `fix` / `bug` / `refactor` / `docs` / `chore` / `design`）
- `short-description`: ハイフン区切りの英語の短い概要

例:
- `0001-bug-cursor-invisible-past-xx-more.md`
- `0002-feat-multi-line-prompt-paste.md`
- `0003-design-state-dir-layout.md`

### issue ファイルのフォーマット

タイトル直下に必須メタ、本文は決まったセクション構成にする:

```markdown
# <タイトル>

Created: YYYY-MM-DD
Completed: YYYY-MM-DD  ← close 時のみ追記
Model: <model-name> <version>  ← 例: Opus 4.7 / GPT-5.4

## 概要

(簡潔な説明)

## 根拠

(なぜ対応が必要か。コードの該当箇所、ユーザ要望、参照仕様などを明示する)

## 対応方針

(任意。設計上のアプローチ。大きな選択肢がある場合は採用案と却下案を併記)

## 解決方法

(close 時に追記。何をどう修正したか。コミットや変更ファイルを引用する)
```

### ワークフロー

| 状況 | 操作 |
|---|---|
| 新規 issue 作成 | `issues/` 直下にファイル作成 + `issues/SEQUENCE` を +1 → **issue 単独でコミット** |
| 対応順序 | 番号の小さい issue から順に対応する |
| 完了時 | 本文に `Completed:` と `## 解決方法` を追記 + `git mv issues/<f>.md issues/closed/` → **1 issue 1 commit** で確定 |
| pending 化 | 本文に pending 理由を追記 + `git mv issues/<f>.md issues/pending/`。修正は加えない |
| reopen | 本文に reopen 理由（何が解決していなかったか）を追記 + `git mv issues/closed/<f>.md issues/` |
| バグ発見時 | 再現手順・期待結果・実結果を含む issue を `issues/` に作成してからコミット |

### 3 本柱ドキュメントとの役割分担

- issue: **これから直す / 作る** ことの管理単位（前向き）
- spec / design: **現在こうなっている** 仕様・設計のスナップショット
- history: **過去にこう判断した** 経緯（後ろ向き、append-only）

issue を完了させた時に user-visible な振る舞いが変わるなら spec、実装構造が変わるなら design を同じ PR で更新する。方向反転を伴う対応なら history も追記する。

## 実装後の動作確認

実装・修正が完了したら、必ず `/verify-implementation` を実行して動作確認を行ってから完了を報告すること。
