# session 並びの MRU 自動ソート

Created: 2026-05-08
Model: Opus 4.7

## 概要

unpinned session 群を「直近 attach した順」で自動再ソートする機能。

## 根拠

複数 session を運用していると、直近触った session が一覧の中で埋もれて見つけにくい。`pinned_sessions` で重要 session を持ち上げる仕組みはあるが、明示的に pin していない session 群の中で「最近性」のヒントが何もない。pinned > unpinned (MRU) > hidden の順序関係を保てれば pin と相互補完的に機能する。

## 対応方針

- session ごとの last-attached time を取得（現状 state file は記録していないため、`pane_N_last_attached` 等の sidecar を新設するか、tmux native の attached-time format を流用）
- unpinned 群を last-attached desc で再ソート
- pinned 群と hidden 群の順序は不変

## Pending 理由

カーソル追従ロジック（select-window のたびに sidebar が SIGUSR1 で再描画してカーソルを当てる）と相性が悪い。「いま見ている session が突然動く」UX 混乱が懸念され、確信を持って入れられる UX 設計に至っていない。需要が顕在化した時点で再検討する。

詳細な却下／保留判断の経緯は [docs/history.md](../../docs/history.md) の「サーバ境界制御 / MRU 自動ソートの取り下げ（2026-05-08）」を参照。
