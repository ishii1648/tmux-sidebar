---
name: verify-implementation
description: 実装後の動作確認を行う。ビルド確認・current tmux window でのサイドバー表示・目視確認を順に実施し、結果を報告する。
argument-hint: ""
version: 0.3.0
---

# verify-implementation

実装・修正が完了したら、以下のステップで動作確認を行い、結果を報告する。

## Step 1: ビルド確認

```fish
go build ./...
```

エラーがあれば修正してから次のステップへ進む。

## Step 2: テスト実行

```fish
go test ./...
```

すべての package が `ok` であることを確認する。失敗があれば修正してから次へ。

## Step 3: サイドバーを current tmux window に表示

`/open-sidebar` スキルを実行する。完了後 `$sidebar_id` にペイン ID が入る。`/open-sidebar` は `-l 40` で開くため幅は 40 になる。

## Step 4: サイドバーの表示内容を自律的に検証

`tmux capture-pane` でペインの内容を取得し、各チェック項目を自律的に確認する。

```fish
# ペインの位置・サイズを確認
tmux list-panes -F "#{pane_id} #{pane_left} #{pane_width} #{@pane_role}"

# ペインの表示内容を取得（エスケープシーケンス込み）
tmux capture-pane -ep -t $sidebar_id
```

取得した出力を以下のチェックリストに照らして確認する:

### レイアウト

- `pane_left` が `0` → サイドバーが左端に配置されている
- `pane_width` が `40` → `/open-sidebar` の `-l 40` 通り (`~/.config/tmux-sidebar/width` で上書きしている場合はその値)
- `@pane_role` が `sidebar` → ペインロールが正しくセットされている

### コンテンツ

- 出力 1 行目に `Sessions` が含まれる → ヘッダーあり (フォーカス時 `● Sessions` / 非フォーカス時 `○ Sessions`)
- 2 行目に `type to filter...` プレースホルダか入力中の検索クエリ → 検索ボックスが描画されている
- 各セッションヘッダ行が `▾ <session-name>` 形式 → セッション展開トグルが描画されている
- ウィンドウ行が `<index>: <window-name>` 形式 → ウィンドウ一覧が表示されている
- フッター行 `Esc:clear ^C:quit` → キー操作ヒントが描画されている

### 状態バッジ (該当する agent / running 状態がある場合のみ)

- 行末に `[c]` (Claude Code) または `[x]` (Codex CLI) → agent タグが描画されている
- 行末に `🔄Nm` または `🔄Ns` → running バッジ + 経過時間
- 行末に `💬` → permission / ask バッジ

### 現在ウィンドウのハイライト

サイドバーは current tmux window を `▶` カーソル + 背景色で強調する。以下のいずれかで判定:

- 出力に `▶ ` プレフィックス付き行が存在する → カーソル行の表示
- 同じ行のエスケープシーケンスに背景色 `\x1b[48;2;10;48;105m` (濃紺) が含まれる → 選択行のハイライト

> 旧バージョンのスキルでは `[All]/[Running]/[Waiting]` フィルタータブと `[interactive]` フッターを期待していたが、現行 UI には存在しないため検査項目から除外。

## Step 5: doctor の self-check (任意・doctor 周辺を変更したとき)

`internal/doctor/` を変更した場合は doctor の出力にも目を通す:

```fish
go build -o /tmp/tmux-sidebar-doctor-test ./
echo n | /tmp/tmux-sidebar-doctor-test doctor
rm /tmp/tmux-sidebar-doctor-test
```

`Runtime` / `Claude Code settings.json` / `tmux.conf` の 3 セクションが期待通りの severity (OK/WARN/ERROR/OPT) で出ているかを確認する。

## Step 6: 結果の報告

確認完了後、以下の形式で報告する:

```
## 動作確認結果

### 確認済みシナリオ
- [ ] ビルド確認: go build ./...
- [ ] テスト: go test ./... の結果
- [ ] サイドバー表示: 開閉・レイアウト (pane_left/pane_width/@pane_role)
- [ ] コンテンツ: ヘッダー・検索・セッション一覧・フッター
- [ ] 現在ウィンドウのハイライト: ▶ カーソル + 背景色
- [ ] (任意) doctor 出力の確認

### 未確認シナリオ（ある場合のみ）
- 理由とともに記載

### エラー・懸念事項（ある場合のみ）
- 内容を記載
```
