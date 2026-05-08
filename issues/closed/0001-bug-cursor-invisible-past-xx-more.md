# popup picker で `↓ XX more` より下にスクロールするとカーソルが見えなくなる

Created: 2026-05-08
Completed: 2026-05-08
Model: Opus 4.7

## 概要

`tmux-sidebar new` の repo 一覧 (popup picker、step=stepRepo) で、cursor が viewport (`maxRows`) を超えた位置に移動すると、選択中の repo 行が描画されず、どの repo にカーソルが当たっているか分からなくなる。

## 根拠

`internal/picker/picker.go` の `viewRepo()` が、cursor 位置を一切考慮せず単純に `m.filtered[0..maxRows]` を描画して打ち切っていた:

```go
maxRows := m.viewportRows()
for i, r := range m.filtered {
    if i >= maxRows {
        sb.WriteString(styleFaint.Render(fmt.Sprintf("  ↓ %d more", len(m.filtered)-maxRows)) + "\n")
        break
    }
    // ...
}
```

一方 `moveCursor` は cursor を `len(m.filtered)-1` まで動かせるため、ghq 配下の repo 数が viewport (typically 16〜20 行) を超えると、`KeyDown` で cursor が `maxRows` 以降に移動した瞬間にカーソル行ごと描画範囲外に消える。ユーザは `Enter` を押すまでどの repo を選択しているかを確認できなくなる。

## 対応方針

Model に `scrollOffset` フィールドを追加し、render 時に cursor が常に可視範囲に収まるよう window をスクロールする。隠れた item は `↑ N more` / `↓ N more` で表示する。

- cursor が window の上下に出たら必要なだけスクロール
- scroll offset は再描画間で sticky (cursor が範囲内なら window は動かない)
- マーカー行は item slot 1 行を消費 (上下両方なら `maxRows-2` slots)
- `applyFilter` 時は scrollOffset を 0 リセット (検索クエリ変更で結果集合が変わるため)

## 解決方法

PR #47 (`feat/tmux-sidebar-new-xx-more` ブランチ) で対応。コミット d1edad8。

変更ファイル:

- `internal/picker/picker.go`: `Model` に `scrollOffset int` 追加、`applyFilter` で 0 リセット、`viewRepo` を書き換え、可視 window 計算ヘルパー `computeRepoViewport` を新設
- `internal/picker/picker_test.go`: `TestComputeRepoViewportKeepsCursorVisible` (8 ケース) と `TestViewRepoScrollsCursorIntoView` を追加

`computeRepoViewport(cursor, total, maxRows, savedStart)` の挙動:

- `total <= maxRows`: 全件描画、マーカー無し
- それ以外: cursor を `[start, end)` に収めるよう必要なだけ start を再計算 (capacity が start に依存するため fixed-point 反復、最大 5 回)
- `start > 0` なら `↑ N more` / `start + slots < total` なら `↓ N more` を別行で描画
