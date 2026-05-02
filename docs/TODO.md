# tmux-sidebar 実装 TODO

read-only navigation から control surface への scope 拡張に伴う実装タスク。
背景は `docs/history.md`、目標仕様は `docs/spec.md`、設計は `docs/design.md` を参照。

各 phase は **その phase 単独で merge 可能** な単位とし、上から順に実装する。

---

## Phase 1: modal 入力モデルの基盤

後続フェーズの単打コマンドを成立させるための土台。これがないと `d` / `R` などが search に流れて取れない。

- [ ] `internal/ui` に `inputMode` (`normal` / `search`) を導入し、`handleKey` を分岐
- [ ] `/` で search モードへ進入、`Esc` で normal へ戻る
- [ ] 既存の「任意文字入力 → search」を「`/` 押下後の任意文字 → search」へ変更
- [ ] 既存テストの key dispatch を modal 前提に書き直す
- [ ] `j` / `k` / `Enter` / `Tab` の挙動が normal モードで保たれていることをテスト
- [ ] e2e: `/` で検索開始、`Esc` で解除、normal 中は文字入力が無視されること

---

## Phase 2: 単一行 mutate 操作

最小工数で体感を変えられるコマンド群。modal 化が前提。

### close

- [ ] `internal/tmux` に `KillWindow(target string)` / `KillSession(name string)` を追加
- [ ] `d` で window close、`D` で session close
- [ ] state file の status から confirm 強度を分岐:
  - `idle` → `y/N`
  - `running` → 経過時間付き confirm
  - `permission` / `ask` → 直近 prompt 付き warning
- [ ] confirm dialog UI（normal mode の sub-state）
- [ ] kill 直前に `tmux capture-pane -p` を `~/.local/share/tmux-sidebar/graveyard/` へ退避し、path を message line に通知
- [ ] e2e: idle window の close、running window の confirm、cancel 時に kill されないこと

### 新規 window

- [ ] `internal/tmux` に `NewWindow(target, cwd string)` を追加
- [ ] `n` でカーソル session 内に新規 window 作成（cwd は session の current path）
- [ ] e2e: 新規 window が末尾に追加され、カーソル追従すること

---

## Phase 3: pin / 並べ替え永続化

config file 拡張と並び替えロジック。

### config 拡張

- [ ] `internal/config.Config` に `PinnedSessions` を追加
- [ ] `~/.config/tmux-sidebar/pinned_sessions` を read/write
- [ ] 1 行 1 entry、`#` でコメントの形式を踏襲

### pin toggle

- [ ] `p` で pin toggle（pinned_sessions ファイル更新 + reload）
- [ ] pinned session header に `📌` 表示
- [ ] pinned 群と unpinned 群の境界に区切り線
- [ ] hidden > pinned > 通常順 の優先関係をテスト

### window swap (同 session 内)

- [ ] `internal/tmux` に `SwapWindow` を追加
- [ ] `Shift+J` / `Shift+K` で同 session 内 swap、カーソル追従
- [ ] e2e: 並びが入れ替わり、カーソルが対象 window を追跡すること

### move-window (別 session へ)

- [ ] `internal/tmux` に `MoveWindow` を追加
- [ ] `m` mark → カーソル移動 → `m` drop の 2 段階モード
- [ ] mark 行に視覚マーカー、`Esc` で取消
- [ ] target が session header の場合は session 末尾に挿入
- [ ] e2e: window が別 session に移動し、元 session に痕跡が残らないこと

---

## Phase 4: 多重選択 + バルク

multi-select の UI と一括 close。

- [ ] `Space`（window 上）で multi-select toggle、選択行に視覚マーカー
- [ ] `Esc` で選択解除
- [ ] 選択ありの状態で `d` 押下 → bulk close（全件 idle なら一括 confirm、1 件でも running があれば個別 confirm）
- [ ] e2e: 複数選択 → 一括 close、cancel で残ること

---

## Phase 5: popup picker mode（新規 session）

cross-context lifecycle の最後のピース。tmw を engine として残しつつ UI を統合する。

### 基盤

- [ ] `internal/picker` パッケージを新設、bubbletea で repo→mode→config の wizard を実装
- [ ] subcommand `tmux-sidebar new --context=<file>` を追加
- [ ] `--context` の JSON フォーマット定義（既存 sessions / pinned / sidebar session id）

### 起動経路

- [ ] sidebar pane で `N` 押下 → context を temp file へ書き出し → `tmux display-popup -E -w 80 -h 24 'tmux-sidebar new --context=...'`
- [ ] popup 終了後、pane mode が `loadData()` を発火、新 session にカーソル移動

### Step 1: repo 選択

- [ ] `internal/repo` に ghq 配下 repo 列挙を実装（`ghq list` 呼び出し）
- [ ] fuzzy filter（`sahilm/fuzzy` などのライブラリ採用）
- [ ] context の既存 sessions と突き合わせ、open 中の repo を dim 表示
- [ ] `Enter` で:
  - 未 open repo → mode 選択へ進む
  - open 中 repo → 既存 session へ switch して終了

### Step 2: mode 選択

- [ ] `claude` / `codex` / `dispatch` / `orchestrate` の 4 択を radio 形式で表示
- [ ] 各 mode の説明を 1 行で添える

### Step 3: mode 別追加設定（必要時のみ）

- [ ] worktree branch 名入力（tmw worktree 対象 repo の場合）
- [ ] orchestrate chain 種別選択（orchestrate 選択時）
- [ ] 詳細仕様は tmw / 各 skill の引数に従う

### 実行

- [ ] tmw / agent 起動コマンドを `os/exec` で実行
- [ ] 失敗時は stderr 内容を popup 内 error line に表示、`Esc` で popup 終了
- [ ] 成功時は popup を閉じて pane mode に戻る

### 既存 popup tmw との並存

- [ ] dotfiles 側の popup tmw キーバインドを維持（fallback として）
- [ ] README にて「sidebar の `N` が primary、popup tmw は fallback」と明記

---

## Phase 6: preview 拡張 + activity history

cursored window の補助情報を強化する。

### capture-pane preview

- [ ] `internal/tmux` に `CapturePane(target string, lines int)` を追加
- [ ] 10 秒 tick で cursored window の代表 pane を capture
- [ ] prompt preview があればそちら優先、なければ capture を fallback で表示
- [ ] sidebar pane 自身の capture を読まないこと（自己参照ループ防止）

### unread badge

- [ ] state 形式に `pane_N_event_log` を追加（仕様: `<epoch>:permission|ask` を改行区切り append）
- [ ] `internal/state` で last-attached time 以降の event 数をカウント
- [ ] `!N` バッジを window 行に表示
- [ ] switch-client で当該 window に移動した際、`pane_N_event_log` を truncate
- [ ] dotfiles 側の Claude/Codex hook を更新する追記タスクを README に明記（ファイル append 仕様）

### session 折りたたみ

- [ ] session header 上で `Space` 押下時の toggle ロジック
- [ ] 折りたたみ状態は in-memory のみ（永続化しない）
- [ ] header の `▾`/`▸` 表示

---

## Phase 7: ナビゲーション補助

vim 風の追加移動キー。

- [ ] `gg` で先頭、`G` で末尾の window 行へ
- [ ] e2e: 大きな session 群でジャンプが効くこと

---

## Phase 8: doctor 更新と documentation

- [ ] `tmux-sidebar doctor` に以下を追加:
  - tmux 3.2 以上の確認（popup 必須）
  - `pane_N_event_log` 仕様の hook 設定確認（dotfiles 側）
- [ ] README.md を Phase 進捗に応じて更新（features / keyboard shortcuts / subcommands）
- [ ] docs/setup.md に `N` キーバインドの推奨設定を追記

---

## 参考: 採用しない・延期する項目

| 項目 | 理由 |
|---|---|
| 完全な undo close（scrollback 完全復元） | tmux primitive にない。tmux-resurrect 連携で別途検討 |
| pane 内部操作（split, zoom, copy-mode）の sidebar 経由化 | 「カーソル位置に対する 1:1 操作」は tmux native の責務 |
| server 境界制御（attach/detach/new-server） | sidebar 起動前の世界。tmw / 起動 profile の責務 |
| repo rename | sidebar pane が dominant artifact、識別子への影響大 |
| session 並びの MRU 自動ソート | カーソル追従と相性が悪く、UX が混乱しやすい。要検討項目として保留 |
| inline rename（`R` / `Shift+R`） | tmux の自動 rename + sidebar の path/PR 表示で識別が足りる。手動 rename 需要が薄く、`prefix+,` / `prefix+$` の native 代替で十分。実装コスト（inline textinput + modal sub-state + e2e）に対する価値が見合わない |

---

## 実装順序の根拠

```
Phase 1 (modal)        ← ブロッカー。これがないと 2 以降が成立しない
Phase 2 (close/new-window) ← 単発で体感が変わる、リスクが低い
Phase 3 (pin/move)     ← 永続化の追加、設計の中核
Phase 4 (multi-select) ← Phase 2/3 の上に乗る派生
Phase 5 (popup picker) ← 別バイナリモードの新規実装、最大ボリューム
Phase 6 (preview)      ← state 形式拡張を伴うため後ろに置く
Phase 7 (gg/G)         ← おまけ、いつでも入れられる
Phase 8 (doctor/docs)  ← 全体が形になってから整える
```
