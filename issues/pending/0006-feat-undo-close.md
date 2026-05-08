# window / session の undo close（scrollback 完全復元）

Created: 2026-05-08
Model: Opus 4.7

## 概要

`d` / `D` で kill した window / session を復活させる undo 機能。現状は kill 直前に `tmux capture-pane -p` を `~/.local/share/tmux-sidebar/graveyard/` へ退避し、メッセージ行で path を通知するに留まっている。これを「scrollback 含めて完全復元」する。

## 根拠

誤操作で重要な agent ログ・進行状態を失うリスクへの保険。capture-pane の退避は静的 snapshot にすぎず、agent transcript の進行中状態 / window 環境変数 / cwd / 起動コマンドまでは戻せない。`d` / `D` の confirm 強度は高めているが、復元できない以上は「失っても痛くない」レベルの安心感までは届いていない。

## Pending 理由

tmux primitive に scrollback 完全復元機能がない。tmux-resurrect / tmux-continuum など外部プラグインとの連携で実現する余地はあるが、その設計判断（依存追加の是非、復元粒度、sidebar 内部 UI と plugin output の境界）がまだ整理されていない。

詳細は [docs/history.md](../../docs/history.md) §105「read-only navigation から control surface への scope 拡張」の "採用しなかった代替" 表を参照。
