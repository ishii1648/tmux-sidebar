# issues

GitHub Issues は使わず、このディレクトリ以下に markdown で issue を管理する。バグ報告・機能要望・設計課題はすべてここで完結させる。

## ディレクトリ構成

| パス | 用途 |
|---|---|
| `issues/` | active な issue（対応中・対応待ち） |
| `issues/closed/` | 完了済 issue（完了時に `git mv` で移動） |
| `issues/pending/` | 外部依存・設計判断待ちで凍結中の issue（close しない） |
| `issues/SEQUENCE` | 次に発番する番号。issue 作成時に +1 して同じコミットに含める |

## ファイル命名規則

`{seqnum}-{category}-{short-description}.md` 形式で固定する:

- `seqnum`: `issues/SEQUENCE` の現在値、4 桁ゼロパディング（9999 を超えたら 5 桁）
- `category`: コミットプレフィックスと揃える（`feat` / `fix` / `bug` / `refactor` / `docs` / `chore` / `design`）
- `short-description`: ハイフン区切りの英語の短い概要

例:
- `0001-bug-cursor-invisible-past-xx-more.md`
- `0002-feat-multi-line-prompt-paste.md`
- `0003-design-state-dir-layout.md`

## issue ファイルのフォーマット

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

## 変更箇所

(任意。複数ファイル・複数パッケージにまたがる場合のみ。着手前に書くと AI への実装委譲がしやすい)

| パッケージ / ファイル | 変更内容 |
|---|---|
| `internal/picker/picker.go` | scrollOffset 追加、viewRepo 書き換え |
| `internal/picker/picker_test.go` | viewport テスト追加 |

## 実装チェックリスト

(任意。複数ステップに分かれる場合のみ。着手前に書くと AI への実装委譲がしやすい)

- [ ] computeRepoViewport ヘルパー実装
- [ ] applyFilter で scrollOffset リセット
- [ ] e2e: 16 行超えで cursor 可視

## 解決方法

(close 時に追記。何をどう修正したか。コミットや変更ファイルを引用する)
```

「変更箇所」「実装チェックリスト」は **着手前の計画文書として書くと効く**（実装後に思い出して書くと事後追認になりやすい）。1 行 fix や対応方針が自明な issue では省略してよい。

## ワークフロー

| 状況 | 操作 |
|---|---|
| 新規 issue 作成 | `issues/` 直下にファイル作成 + `issues/SEQUENCE` を +1 → **issue 単独でコミット** |
| 対応順序 | 番号の小さい issue から順に対応する |
| 完了時 | 本文に `Completed:` と `## 解決方法` を追記 + `git mv issues/<f>.md issues/closed/` → **1 issue 1 commit** で確定 |
| pending 化 | 本文に pending 理由を追記 + `git mv issues/<f>.md issues/pending/`。修正は加えない |
| reopen | 本文に reopen 理由（何が解決していなかったか）を追記 + `git mv issues/closed/<f>.md issues/` |
| バグ発見時 | 再現手順・期待結果・実結果を含む issue を `issues/` に作成してからコミット |

## 3 本柱ドキュメントとの役割分担

- issue: **これから直す / 作る** ことの管理単位（前向き）
- spec / design: **現在こうなっている** 仕様・設計のスナップショット
- history: **過去にこう判断した** 経緯（後ろ向き、append-only）

issue を完了させた時に user-visible な振る舞いが変わるなら spec、実装構造が変わるなら design を同じ PR で更新する。方向反転を伴う対応なら history も追記する。
