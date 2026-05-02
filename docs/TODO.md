# tmux-sidebar 実装 TODO

read-only navigation から control surface への scope 拡張に伴う実装タスク。
背景は `docs/history.md`、目標仕様は `docs/spec.md`、設計は `docs/design.md` を参照。

各 phase は **その phase 単独で merge 可能** な単位とし、上から順に実装する。

---

## Phase 1: modal 入力モデルの基盤

後続フェーズの単打コマンドを成立させるための土台。これがないと `d` / `R` などが search に流れて取れない。

- [x] `internal/ui` に `inputMode` (`normal` / `search`) を導入し、`handleKey` を分岐
- [x] `/` で search モードへ進入、`Esc` で normal へ戻る
- [x] 既存の「任意文字入力 → search」を「`/` 押下後の任意文字 → search」へ変更
- [x] 既存テストの key dispatch を modal 前提に書き直す
- [x] `j` / `k` / `Enter` / `Tab` の挙動が normal モードで保たれていることをテスト
- [x] e2e: `/` で検索開始、`Esc` で解除、normal 中は文字入力が無視されること

---

## Phase 2: 単一行 mutate 操作

最小工数で体感を変えられるコマンド群。modal 化が前提。

### close

- [x] `internal/tmux` に `KillWindow(target string)` / `KillSession(name string)` を追加
- [x] `d` で window close、`D` で session close
- [x] state file の status から confirm 強度を分岐:
  - `idle` → `y/N`
  - `running` → 経過時間付き confirm
  - `permission` / `ask` → 直近 prompt 付き warning
- [x] confirm dialog UI（normal mode の sub-state）
- [x] kill 直前に `tmux capture-pane -p` を `~/.local/share/tmux-sidebar/graveyard/` へ退避し、path を message line に通知
- [x] e2e: idle window の close、running window の confirm、cancel 時に kill されないこと

---

## Phase 3: pin 永続化

session 装飾の永続化。

### config 拡張

- [x] `internal/config.Config` に `PinnedSessions` を追加
- [x] `~/.config/tmux-sidebar/pinned_sessions` を read/write
- [x] 1 行 1 entry、`#` でコメントの形式を踏襲

### 表示と保護

- [x] pinned session header に `📌` 表示
- [x] pinned 群と unpinned 群の境界に区切り線
- [x] hidden > pinned > 通常順 の優先関係をテスト
- [x] pinned session への `D`（session kill）はブロックする削除保護
- [x] ファイル編集の自動反映（loadData 起点で config 再読込）

---

## Phase 4: popup picker mode（新規 session）

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

## Phase 5: doctor 更新と documentation

- [ ] `tmux-sidebar doctor` に tmux 3.2 以上の確認（popup 必須）を追加
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
| カーソル session 内に新規 window（`n`） | current session への new-window は cmd+t / `prefix+c` の 1 ストロークで完結する。sidebar 経由は「focus → カーソル移動 → `n`」の 3 アクションが必要で明確に遅い。「current 以外の session に window を追加したい」シーンは頻度が低く、`prefix+c` で current に作って tmux native の move-window で移すワークフローで吸収できる。`N`（popup picker で新規 session）は tmw + agent mode wizard で代替不能なので維持 |
| 同 session 内 window swap（`Shift+J` / `Shift+K`） | tmux native の `prefix+{` / `prefix+}` で同等。同 session 内の window 順を細かく入れ替える運用は薄く、カーソル追従ロジックの実装コストに見合わない |
| 別 session への move-window（`m`） | tmux native の `prefix+.` で代替可能。「動的に session を移す」シーンは頻度が低く、2 段階モード（mark → カーソル移動 → drop）+ 視覚マーカー + Esc 取消 + session header 末尾挿入の実装コストに対して体感差が小さい |
| 多重選択 + バルク close（`Space` で multi-select、選択ありの `d` で一括 close） | session 単位なら `D` 一発、数個程度なら `d` 連打で吸収できる。選択 visual を見落とした事故リスクが残り、modal sub-state（select mode、bulk confirm の全件 idle vs 個別降格）の実装コストに対して、agent monitoring 主用途のこの sidebar では使用頻度が薄い |
| `gg` / `G`（vim 風の先頭/末尾ジャンプ） | session/window 数が爆発的に多くなる運用は薄く、`j`/`k` の連打 + `/` 検索で目的行に到達できる。実装は軽いが「いつでも入れられるおまけ」のまま実需が顕在化していないので、必要になった時点で追加する |
| capture-pane preview | agent transcript の prompt preview があれば agent window の判別はできる。agent 不在 window 用の preview は需要が薄く、自己参照ループ防止 + 10 秒 tick の実装コストに見合わない |
| unread badge（`!N` で permission/ask の未読数を表示） | permission/ask は応答するまで `💬` が継続表示されるため「放置されている window」はリアルタイムで分かる。「過去 N 回 permission/ask が来た」履歴を知りたいシーンが現実的に薄く、state file 形式拡張（`pane_N_event_log`）と外部依存（dotfiles の Claude/Codex hook 更新）のコストに見合わない |
| session 折りたたみ（session header 上 `Space` で toggle、`▾`/`▸` 表示） | 1 session あたりの window 数は通常 5 個前後で、折りたたみたい欲求が顕在化していない。pin で重要 session を持ち上げれば見渡しは足りる。`Space` を消費する点も他機能との競合リスク |

---

## 実装順序の根拠

```
Phase 1 (modal)        ← ブロッカー。これがないと 2 以降が成立しない
Phase 2 (close)        ← 単発で体感が変わる、リスクが低い
Phase 3 (pin)          ← 永続化の追加
Phase 4 (popup picker) ← 別バイナリモードの新規実装、最大ボリューム
Phase 5 (doctor/docs)  ← 全体が形になってから整える
```
