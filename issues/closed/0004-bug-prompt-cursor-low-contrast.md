# popup picker の prompt 入力で block cursor のコントラスト不足によりカーソル位置が分からない

Created: 2026-05-08
Completed: 2026-05-08
Model: Opus 4.7

## 概要

`#0003` で `▏` (font fallback で full block 化する問題) を回避するために `lipgloss.NewStyle().Reverse(true)` の reverse-video block cursor 方式に切り替えたが、半透明 / 暗色テーマの端末ではカーソル位置の cell の色変化が肉眼でほとんど判別できず、結局 cursor 位置が見えなくなっていた。

例: prompt に `hello world` を入力し ← で `r` 上にカーソルを置いても、その cell が周辺と同じく暗色のままで「カーソルが今どこにあるか」が一目で分からない。

## 根拠

`internal/picker/picker.go` で

```go
styleCursorBlock = lipgloss.NewStyle().Reverse(true)
```

としていた。Reverse は端末の現在 fg/bg を入れ替えるだけなので、

- 端末背景が半透明 (画像が透けて見える dark theme 等) → 反転後も暗いまま
- iTerm2 の minimum contrast 設定 → 反転色のコントラストが落ちる
- 多くの dark theme で fg(白) と bg(黒) の差が小さい場合がある

といった環境では「反転している cell」と「素の cell」の見た目の差が小さく、視認できない。`#0003` の **rune が見える** という要件は満たしているが、**カーソル位置が一目で分かる** という UX 上の本質的要件を満たしていない。

## 対応方針

reverse-video をやめ、**端末テーマに依存しない明示的な fg/bg 色付きブロック** に切り替える。

```go
styleCursorBlock = lipgloss.NewStyle().
    Background(lipgloss.Color("4")).   // blue (prompt prefix と同じ系統)
    Foreground(lipgloss.Color("15")).  // bright white
    Bold(true)
```

- 背景色を明示するので半透明テーマでも cell が確実に塗られる
- 前景色も明示するので minimum contrast 設定の影響を受けにくい
- prompt prefix `> ` が blue なので同系統の色味で UI 全体に統一感が出る
- block 内の rune は引き続き可視 (`#0003` の要件を維持)

テスト helper の reverse-video 検出も意味を失うので、`styleCursorBlock.Render("\x00")` 実体から prefix/suffix を切り出して cursor span を検出する方式 (color profile / SGR 表現に依存しない) に書き換える。

## 解決方法

コミット ae02f96 で `styleCursorBlock` を明示的な fg/bg 色付きブロックに変更。

変更ファイル:

- `internal/picker/picker.go`:
  - `styleCursorBlock = lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15")).Bold(true)`
  - prompt prefix と同系統の blue 背景 + bright white 前景 + bold で半透明テーマや low-contrast 設定でも確実に視認できる
- `internal/picker/picker_test.go`:
  - `renderForTest` を「`styleCursorBlock.Render("\x00")` の戻り値から動的に prefix/suffix を切り出して cursor span を検出する」方式に書き換え。SGR 表現 (4-bit `\x1b[44m` / 256-color `\x1b[48;5;n` / RGB `\x1b[48;2;...`) に依存しない
  - reverse-video 専用の regex を削除

これにより `#0003` で達成した「rune が見える」要件を保ちつつ、UX 上の本質的要件「カーソル位置が一目で分かる」を満たした。
