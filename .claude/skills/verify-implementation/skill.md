---
name: verify-implementation
description: 実装後の動作確認を行う。ビルド確認・current tmux window でのサイドバー表示・目視確認を順に実施し、結果を報告する。
argument-hint: ""
version: 0.2.0
---

# verify-implementation

実装・修正が完了したら、以下のステップで動作確認を行い、結果を報告する。

## Step 1: ビルド確認

```fish
go build ./...
```

エラーがあれば修正してから次のステップへ進む。

## Step 2: サイドバーを current tmux window に表示

`/open-sidebar` スキルを実行する。完了後 `$sidebar_id` にペイン ID が入る。

## Step 3: サイドバーの表示内容を自律的に検証

`tmux capture-pane` でペインの内容を取得し、各チェック項目を自律的に確認する。

```fish
# ペインの位置・サイズを確認
tmux list-panes -F "#{pane_id} #{pane_left} #{pane_width} #{@pane_role}"

# ペインの表示内容を取得（エスケープシーケンス込み）
tmux capture-pane -ep -t $sidebar_id
```

取得した出力を以下のチェックリストに照らして確認する:

- `pane_left` が `0` → サイドバーが左側に配置されている
- `pane_width` が `35` → サイズが意図通り
- 出力に `Sessions` が含まれる → Sessions ヘッダーあり
- 出力に `[All]`・`[Running]`・`[Waiting]` が含まれる → フィルタータブあり
- 出力にセッション名・ウィンドウ名が含まれる → 一覧が表示されている
- 出力にアンダーラインのエスケープシーケンス（`\x1b[4m` 等）が含まれる → 現在ウィンドウが強調されている
- 出力に `[interactive]` が含まれる → フッターが表示されている

## Step 4: 結果の報告

確認完了後、以下の形式で報告する:

```
## 動作確認結果

### 確認済みシナリオ
- [ ] ビルド確認: go build ./... の出力
- [ ] サイドバー表示: 開閉・レイアウトの目視確認
- [ ] 既存サイドバーの再表示: kill → 再生成の動作

### 未確認シナリオ（ある場合のみ）
- 理由とともに記載

### エラー・懸念事項（ある場合のみ）
- 内容を記載
```
