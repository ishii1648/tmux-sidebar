# popup picker の prompt 入力で left/right によるカーソル移動が効かない

Created: 2026-05-08
Completed: 2026-05-08
Model: Opus 4.7

## 概要

`tmux-sidebar new` の prompt step (stepPrompt) で、入力中のテキストに対して左右矢印キー (←/→) や Ctrl+B/F、Home/End、Delete が効かず、キャレットが常にバッファ末尾に固定されていた。途中の文字を編集するには右側を全削除するしかなく、誤入力の修正コストが高かった。

## 根拠

`internal/picker/picker.go` の `handlePromptKey` は KeyRunes / KeySpace / Newline を一律 `m.prompt += s` で末尾追記し、Backspace も末尾の rune だけを切り詰める実装だった:

```go
case tea.KeyBackspace:
    if r := []rune(m.prompt); len(r) > 0 {
        m.prompt = string(r[:len(r)-1])
    }
case tea.KeyRunes:
    // ...
    m.prompt += s
```

`Model` に挿入位置を表すフィールドが無いため左右矢印やカーソル移動キーは noop で、`renderPromptInput` も末尾にしかキャレット `▏` を描画できない。日本語などマルチバイト入力でも rune 単位の途中編集ができない。

## 対応方針

`Model` に rune index ベースの `promptCursor` を導入し、編集系操作をすべてこの cursor 経由に統一する。

- `promptCursor int` を `Model` に追加 (`0 ≤ promptCursor ≤ runeLen(prompt)`)
- Left/Right (Ctrl+B/F)、Home/End (Ctrl+A/E)、Delete を `handlePromptKey` に bind
- 挿入 (`insertAtCursor`)・後方削除 (`deleteBeforeCursor`)・前方削除 (`deleteAtCursor`) は cursor 位置で動作
- `renderPromptInput` は cursor の rune index を wrap 後セグメントに解決し、soft-wrap 境界では次行先頭に `▏` を描画する (次入力が見える位置に出る)
- repo step → prompt step 遷移時 (`m.prompt = ""`) に `promptCursor = 0` リセット

## 解決方法

コミット 3dc43af (`fix(picker): support left/right cursor navigation in prompt input`) で対応。

変更ファイル:

- `internal/picker/picker.go`:
  - `Model` に `promptCursor int` 追加
  - `handlePromptKey` に `KeyLeft/KeyCtrlB`、`KeyRight/KeyCtrlF`、`KeyHome/KeyCtrlA`、`KeyEnd/KeyCtrlE`、`KeyDelete` 分岐を追加
  - 既存の Backspace / KeySpace / KeyRunes / 改行系を `insertAtCursor` / `deleteBeforeCursor` 経由に書き換え
  - rune-aware ヘルパー `promptRuneLen` / `insertAtCursor` / `deleteBeforeCursor` / `deleteAtCursor` を新設
  - `renderPromptInput` のシグネチャに `cursor int` を追加し、segment ごとに `runeOffset` / `runeLen` を保持して該当セグメントにのみ `▏` を描画。soft-wrap 境界では次セグメント先頭に出す
- `internal/picker/picker_test.go`: cursor 移動・挿入・削除・soft-wrap 境界での描画を網羅するテストを追加 (+193 行)
