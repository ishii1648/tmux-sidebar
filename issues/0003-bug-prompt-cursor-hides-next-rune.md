# popup picker の prompt 入力で cursor を中間に戻すと直後の rune が見えなくなる

Created: 2026-05-08
Model: Opus 4.7

## 概要

`tmux-sidebar new` の prompt step で、文字列末尾から左矢印 (←) でカーソルを戻すと、カーソル直後にあるはずの 1 文字が視覚的に消える。例えば `hogete` を入力し ← を 1 回押すと `hoget e` のようにカーソル位置の `e` だけがブロックに隠れて見えなくなる。

## 根拠

`internal/picker/picker.go::renderPromptInput` は cursor 位置に区切りグリフ `▏` (U+258F LEFT ONE EIGHTH BLOCK) を **挿入** している:

```go
b.WriteString(string(runes[:cursorOffset]))
b.WriteString("▏")
b.WriteString(string(runes[cursorOffset:]))
```

`▏` は本来「セルの左端に thin vertical line」を描画する 1 cell 幅文字だが、利用するフォント/端末によってはグリフが欠落し fallback で **セル全体を塗るブロック** として表示される。この場合 cursor の cell が視覚的にブロック化し、`runes[cursorOffset]`（カーソル直後の rune）が cell 1 つ右にずれて表示されるため、ユーザは「カーソル位置にあったはずの 1 文字が消えた」と認識する。

実際の論理状態は正しい (rune は失われていない) が、UI 上は明らかに 1 文字 missing なので使い物にならない。

## 対応方針

カーソル位置に別グリフを挟む方式をやめ、**カーソル位置の rune を reverse-video で描画する block-cursor 方式** に変更する。

- `runes[:cursorOffset]` をそのまま、`runes[cursorOffset]` を reverse-video 化、`runes[cursorOffset+1:]` をそのまま
- 描画対象 rune が無い場合 (バッファ末尾 / hard-break 直後の空行 / soft-wrap 境界で次セグメント先頭に飛ばした場合) のみ従来通り `▏` を末尾/先頭に出す
- フォントに依存しない (どの端末でも reverse-video は確実に動く)
- カーソル直後の rune が必ず可視のまま残る
- 標準的な block-cursor UX に揃う

スタイルは新規 `styleCursorBlock = lipgloss.NewStyle().Reverse(true)` を追加。

## 解決方法

(close 時に追記)
