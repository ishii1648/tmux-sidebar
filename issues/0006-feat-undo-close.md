# window / session の undo close（誤削除リカバリ）

Created: 2026-05-08
Reopened: 2026-05-20
Model: Opus 4.7

## 概要

`d` / `D` で kill した window / session を、サイドバーから interactive に復元できるようにする。復元粒度は「session/window の **構造 + 名前 + cwd + 起動コマンド + layout**」までとし、scrollback 完全復元は scope 外（[docs/history.md](../../docs/history.md) §105 通り tmux-resurrect 等の別レイヤ責務）。

現状は kill 直前に `tmux capture-pane -p` の出力を `~/.local/share/tmux-sidebar/graveyard/<timestamp>_<label>.txt` へ書き出し、`saved: <path>` をメッセージ行に出すだけ（`internal/ui/model.go:984-1002` の `captureToGraveyard()`、メッセージは同 `:728`）。

- graveyard は **write-only**。restore 経路は未実装
- 退避内容は plain text の scrollback のみで、meta（session/window 名 / cwd / 起動コマンド / layout）は持たない
- `docs/spec.md` / `docs/design.md` には graveyard 自体が未記載（spec.md L294 で「完全な undo close は non-goal」とだけ書かれている）

ここに **meta 退避 + 構造復元 UI** を載せ、誤削除を「閉じてもサイドバーから戻せる」レベルに引き上げる。

## 根拠

誤操作で重要な agent context を失うリスクへの保険。capture-pane の退避は静的 snapshot にすぎず、「誤って閉じた window/session を再度同じコンテキストで開き直す」までは戻せない。`d` / `D` の confirm 強度は高めているが、復元できない以上は「失っても痛くない」レベルの安心感までは届いていない。

ユーザ要望（2026-05-20）は scrollback まで含む完全復元ではなく、誤って消した window/session を「構造として」戻したい、というもの。§105 で却下された「**完全な** undo close（scrollback 完全復元）」と、§105 で採用された「kill 直前 confirm + 退避 path 通知だけ」の中間に位置する軽量 undo を scope として切り直す。

## 対応方針

未確定。以下の方向で検討:

| 案 | 内容 | メリット | デメリット |
|---|---|---|---|
| A. graveyard 拡張（採用候補） | kill 直前に capture と並んで `<timestamp>_<label>.json` を退避し、session/window 名・cwd・起動コマンド・layout を JSON で保持。サイドバーから一覧 → 選択 → tmux primitive で再構築 | 追加依存なし、sidebar 内で完結、§105 の境界線（scrollback 完全復元は外）を守れる | 起動コマンドの追跡には pane の `pane_start_command` か state file 連携が必要。layout は `display-message -p '#{window_layout}'` で取れるが session レベル復元は window 群の集合として組み直しが要る |
| B. tmux-resurrect 連携 | 既存 plugin の save ファイルを参照して復元 UI を sidebar から提供 | 復元粒度が広く既存資産を活用 | 依存追加。§105 で「別レイヤ責務」と切り分けた境界線が曖昧になる。sidebar が plugin output を二次的に解釈する構造になり責務が肥大化 |

A を本命、B は scope 外の参考として残す。

## 変更箇所

| パッケージ / ファイル | 変更内容 (A 案ベース) |
|---|---|
| `internal/ui/model.go` | `captureToGraveyard()` を拡張して meta JSON を併存退避。kill 直前の情報収集（cwd / command / layout / window 一覧）を追加 |
| `internal/ui/model.go` (新規 mode) | graveyard 一覧表示 + restore 起動の view mode を追加。`u` あたりのキーでサイドバーから入る想定 |
| `internal/tmux/` または相当箇所 | window/session の再構築コマンド組み立て（`new-session`, `new-window`, `select-layout` の合成） |
| `internal/ui/model_test.go` | meta 退避と restore のテストを追加（既存 `TestClose_YInvokesKillWindow` 系の延長） |
| `docs/spec.md` | graveyard と undo close の user-visible 動作を追記（現状 L294 は「non-goal」と書いてあるので、軽量 undo の追加と完全復元の non-goal 維持を整理） |
| `docs/design.md` | graveyard schema、restore フロー、責務境界（scrollback は外）を追記 |
| `docs/history.md` | §105 の「採用しなかった代替」最終行に対する方向反転を append。「完全復元は維持で却下、軽量 undo は採用」と差分を残す |

## 実装チェックリスト

- [ ] graveyard meta JSON の schema 設計（フィールド、命名、TTL、容量上限）
- [ ] kill 直前の情報収集追加（cwd / 起動コマンド / layout / window 群）
- [ ] meta JSON の退避処理を `captureToGraveyard()` 周辺に組み込み
- [ ] サイドバーの graveyard 一覧 view mode（entry 並び、選択、削除）
- [ ] restore 動作（tmux primitive の組み立てと実行）
- [ ] テスト追加（meta 退避 / restore / TTL）
- [ ] spec.md 更新（軽量 undo を user-visible 仕様として記述、完全復元は non-goal 維持）
- [ ] design.md 更新（graveyard schema と restore フロー）
- [ ] history.md 追記（§105 最終行の方向反転 / 採用と却下の境界線の更新）

## 経緯

### 2026-05-08 作成時 scope（撤回）

「scrollback 含めて完全復元」を目標に置き、agent transcript の進行中状態 / window 環境変数 / cwd / 起動コマンドまで全て戻すことを掲げた。

### 2026-05-08 pending 化

tmux primitive に scrollback 完全復元機能がなく、tmux-resurrect / tmux-continuum との連携設計（依存追加の是非、復元粒度、sidebar 内部 UI と plugin output の境界）が未整理だったため凍結。詳細は [docs/history.md](../../docs/history.md) §105 の "採用しなかった代替" 表を参照。

### 2026-05-20 reopen（スコープ縮小）

ユーザ要望は scrollback 完全復元までは含まず、誤削除リカバリ用途。pending 理由（tmux primitive 由来の完全復元不能制約）は縮小後の scope では制約にならないため active に戻す。完全復元は §105 通り「別レイヤ責務」として scope 外を維持する。
