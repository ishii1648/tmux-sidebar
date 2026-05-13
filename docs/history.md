# tmux-sidebar 経緯

この文書は過去の実装・判断の背景を記録する。
現在のユーザ向け仕様は `docs/spec.md`、実装設計は `docs/design.md` を参照する。

---

## dotfiles における agent 俯瞰の変遷

複数の tmux session で Claude Code を並列実行する際に、
全 session の状態を常時確認できる仕組みを段階的に模索してきた。

| ADR | 手段 | 結果 |
|---|---|---|
| ADR-045 | tmux statusbar に常時表示 | 表示・操作領域コストが大きく撤廃 |
| ADR-046 | popup (`prefix+s`) に集約 | 常時表示を諦める方向へ |
| ADR-047 | Ghostty AppleScript sidebar spike | `command =` との相性問題で断念 |
| ADR-050 | Fish script + `split-window -hfb` | 常時表示は実現。操作面に限界 |
| ADR-063 | agent-pane-state 形式 | Claude Code / Codex CLI 両対応へ拡張 |

---

## ADR-050 Fish 実装

[hiroppy/tmux-agent-sidebar](https://github.com/hiroppy/tmux-agent-sidebar) の調査で
`split-window -hfb` + `after-new-window` hook という実現方式が判明した。
ADR-050 では Rust/外部依存なしに同等の仕組みを Fish script で実装した。

当時の実装は以下を行っていた。

- `split-window -hfb -l 22%` で sidebar pane を左端に作成
- 1 秒 polling で `/tmp/claude-pane-state/pane_N` を読む
- `@pane_role=sidebar` で pane を識別
- `prefix+e` で toggle
- `after-new-window` hook で各 window に自動生成

当時の状態ファイルは Claude Code 専用だった。

```
/tmp/claude-pane-state/
  pane_101
  pane_107
  pane_107_started
```

---

## Go 実装へ移行した理由

Fish + `tput cup 0 0` の passive display では、以下の体験を安定して実現しづらかった。

- キーボード選択と `Enter` による window 移動
- cursor 位置を保ったまま一覧を更新すること
- 全 session/window を表示しつつ agent 状態も重ねること
- pane focus と入力 capture の扱い

このため Bubble Tea を使った Go TUI として再実装した。

---

## `/tmp/agent-pane-state` への移行

初期設計は `/tmp/claude-pane-state` を読み、Claude Code の状態だけを扱っていた。
現在は Codex CLI も同じ sidebar に表示するため、状態 directory を
`/tmp/agent-pane-state` に変更し、`pane_N` の 2 行目に agent kind を追加した。

さらに以下の sidecar file を追加した。

- `pane_N_path`: Git/PR 表示の基準 directory
- `pane_N_session_id`: prompt preview の session key

古い unknown / legacy state は表示上 `[c]` に fallback する。

---

## 1 秒 polling をやめた理由

状態ファイルの変化は `fsnotify` で拾える。
tmux window/session の変化は hook から sidebar process に `SIGUSR1` を送れる。

そのため常時 1 秒ごとに tmux と file system を polling する必要はなくなった。
現在は以下の構成にしている。

- 状態ファイル変更: `fsnotify`
- tmux 変更: `SIGUSR1`
- running elapsed: 1 分 tick
- hook 失敗時の fallback: 10 秒 tick

---

## 幅管理の判断

`split-window -hfb -l 40` の `-l` は絶対セル数指定だが、tmux は client resize 時に
pane 幅を比例 scale する。そのため display 移動や terminal resize の後に
sidebar 幅が 40 列からずれることがある。

現在は sidebar 幅を絶対セル数として扱い、README で `client-resized` hook による
`resize-pane -x` の再適用を案内している。

`enforce-width` サブコマンドは作っていない。処理が単純な resize であり、
tmux.conf の hook だけで完結できるため。

---

## read-only navigation から control surface への scope 拡張

ここまで sidebar は read-only な display + nav 層として位置づけてきた。
ADR-051 で Fish 実装を Go へ移行した時点では、まずキーボード選択 + Enter 移動の
安定提供を優先し、状態を mutate する操作は tmux native (`prefix+&`, `prefix+,` 等) と
外部ツール (tmw / dispatch / popup tmw) に委ねていた。

これを **tmux の cross-context 軸（session / window）の canonical control surface** へ拡張する。
具体的には sidebar pane 内に以下の mutate 操作を持ち込み、新規 session 生成は
sidebar から起動される popup picker に統合する。

- window / session の close, rename, swap, move
- pin / mute / 並べ替えの永続化
- カーソル session 内への新規 window 生成
- 新規 session 生成（sidebar の `N` → tmux popup → repo + agent mode wizard → 完了で sidebar に追従）

sidebar 自身は **常駐 pane (40 cols)** と **popup picker (80×24 程度)** の 2 つの
レンダリング先を持ち、両者は同一バイナリで実装する。

### 採用しなかった代替

| 代替 | 却下理由 |
|---|---|
| 既存の read-only stance を維持 | switch だけ sidebar、close/rename/new は tmux native + popup tmw に分散しており、cross-context 軸の入口が複数ある状態が認知コストになっていた |
| pane 内部操作（split, zoom, copy-mode 等）まで sidebar に取り込む | pane 内部はカーソル位置に対する 1:1 操作で、sidebar を経由する利得がない。「カーソル位置に対する 1 操作は pane、新規入力 + 多軸選択は popup」という線引きを採用 |
| sidebar pane 内部に new-session wizard を描画 | 40 cols では ghq repo 選択 + mode 選択 + mode 別設定を表示しきれない。popup picker として別レンダリング先を持つ方針に修正 |
| 入力モデルを現状維持（任意文字 → search） | mutate 操作のキーバインドを単打で取れず、modal 化（`/` で search モード、normal mode で commands）に変更 |
| popup tmw を完全に廃止 | sidebar 未起動時 / sidebar bug 時の fallback として残す価値があり、両者を tmw engine 共有で並立させる |
| リポジトリ名を control-surface 寄りに rename | sidebar pane が依然として dominant な visible artifact であり、`@pane_role=sidebar` / `TMUX_SIDEBAR_*` 等 identifier への影響も大きい。説明は README/spec.md で行う |
| 完全な undo close（scrollback 完全復元） | tmux primitive にない。tmux-resurrect 等別レイヤの責務として切り分け、sidebar 側は kill 直前 confirm + 退避 path 通知に留める |

### 維持する原則

- 状態の正は依然として tmux + state file（`/tmp/agent-pane-state/`）。sidebar が UI 状態を独占しない
- mutate は tmux primitive（`kill-window`, `rename-window`, `swap-window`, `move-window`, `new-window`, `new-session`）へ素直に翻訳する
- destructive 操作は state file の running 判定を根拠に confirm 強度を変える
- tmux native binding は削らない（sidebar dominant + native fallback）
- tmw / dispatch / orchestrate のロジックは引き続きそれらの責務。sidebar は entry と post-process のみ
- ADR-052 の「one state source, multiple views」は維持（pane mode と popup picker mode は同じ state を読む別 view）

---

## scope 拡張から取り下げた機能（mute / session_order / width config）

control surface 拡張の初版 spec には「pin / mute / 並べ替えの永続化」と並べて
`muted_sessions` / `session_order` / `width` config file を載せていたが、
実装着手前の見直しで以下 3 つを spec から落とした。pinned_sessions（pin 永続化）と
window swap (`Shift+J/K`) / move-window (`m`) は維持する。

### 却下した項目と理由

| 項目 | 却下理由 |
|---|---|
| `muted_sessions`（badge 抑制） | 「行は出すが badge だけ消す」ユースケースは agent 主体の運用では薄い。常駐 watcher / log tail を tmux で抱える運用が顕在化したら再検討。`hidden_sessions` で完全に隠せば足りるケースが大半 |
| `session_order`（unpinned 群の手動並べ替え） | pinned_sessions で重要 session を持ち上げれば、残りは tmux 列挙順で十分。手動順序の維持コストが効用に見合わない（session 作成のたびに位置を意識する必要があり、記憶と乖離した瞬間に逆効果になる）。連動する `Alt+J`/`Alt+K` も削除 |
| `width`（config file） | `TMUX_SIDEBAR_WIDTH` 環境変数と完全に重複。sidebar は tmux.conf 経由で起動するため、`setenv -g TMUX_SIDEBAR_WIDTH N` で inherit すれば足りる。設定の入口を 2 つ持つ理由がない |

### 影響範囲

- spec.md: `M` キー、`Alt+J/K`、Configuration files の 3 行、競合時の優先の muted 行を削除
- design.md: `Config` 構造体の `MutedSessions` / `SessionOrder` / `Width` フィールド、session 並べ替えセクション、合成ロジックの muted/SessionOrder 言及、幅の `~/.config/tmux-sidebar/width` を削除
- TODO.md: Phase 3 から mute / session_order を削除、Phase 5 の `--context` フォーマットと Phase 6 の muted 抑制を削除
- README.md / setup.md: Configuration files 表と `~/.config/tmux-sidebar/width` の案内を削除

---

## inline rename（`R` / `Shift+R`）の取り下げ

control surface 拡張の初版 spec で Phase 2 に含めていた window/session の inline rename
（`R` / `Shift+R` で `bubbles/textinput` を行内展開）を、実装着手前の見直しで取り下げた。

### 却下理由

- tmux の `automatic-rename on`（デフォルト）で window 名は実行中コマンドに自動更新される。手動 rename が必要なケースは限定的
- この sidebar は `pane_N_path` 由来の git branch / PR 番号を表示する設計のため、window 名に頼らなくても識別できる。agent 主体の運用では window 名を編集する文化が薄い
- session 名は tmw 経由で `<owner>_<repo>` 形式に機械生成される。手動 rename したくなるのは命名規則の不備の signal で、UI で対処するより命名規則で吸収すべき
- tmux native の `prefix+,`（window rename）/ `prefix+$`（session rename）が既に動く。「sidebar dominant + native fallback」原則の下、native で済む操作を sidebar に持ち込む価値は薄い
- 実装コスト（`bubbles/textinput` 行内展開、modal sub-state、編集中の他コマンド無効化、e2e）が close より明確に重い割に、得られる体感差が小さい

### 影響範囲

- spec.md: 概要文 / normal mode 説明 / Lifecycle 表から rename 言及を削除
- design.md: `internal/tmux` 責務、modal sub-state 列挙、mutate 翻訳表の R/Shift+R、`## inline rename UI` セクション全体を削除
- TODO.md: Phase 2 の rename サブセクションを削除し、「採用しない・延期する項目」表に inline rename を理由付きで追加
- README.md: 概要文 / 実装中 lifecycle 列挙 / Lifecycle 表 / normal mode 説明から rename 言及を削除

---

## カーソル session 内に新規 window（`n`）の取り下げ

control surface 拡張の初版 spec で Phase 2 に含めていた `n` キー
（カーソル session 内に新規 window 作成、cwd は session の current path）を、
実装着手前の見直しで取り下げた。`N`（popup picker で新規 session）は維持する。

### 却下理由

- current session への new-window は cmd+t（terminal 側 mapping）/ `prefix+c` の 1 ストロークで完結する。sidebar の `n` は「focus → カーソル移動 → `n`」の 3 アクションが必要で明確に遅い
- 「current 以外の session に window を追加したい」シーンは実運用で頻度が低く、必要になっても `prefix+c` で current に作って `m`（move-window）で移すワークフローで吸収できる
- session の current path 引き継ぎは `bind c new-window -c "#{pane_current_path}"` で tmux native でも実現可能。差別化ポイントにならない
- 「sidebar dominant + native fallback」原則の下、native で済む操作を sidebar に持ち込むのは認知負荷を下げる場合のみ。`n` は逆に「current への new-window と挙動が違う（カーソル session 対象）」ことを覚える必要があり、認知負荷を増やす

### 影響範囲

- spec.md: Lifecycle 表から `n` 行を削除（`N` のみ残す）
- design.md: `internal/tmux` 責務から `new` を削除、mutate 翻訳表の `n`（新規 window）行を削除
- TODO.md: Phase 2 の「### 新規 window」サブセクションを削除し、「採用しない・延期する項目」表に `n` を理由付きで追加。実装順序根拠も `Phase 2 (close)` に修正
- README.md: Lifecycle 表の `n / N` を `N` のみに

---

## window swap（`Shift+J/K`）と move-window（`m`）の取り下げ

control surface 拡張の初版 spec で Phase 3 に含めていた window 並べ替え・移動操作を、
実装着手前の見直しで取り下げた。`m` を入口にしていた move-mark sub-state も不要になる。
この取り下げにより Phase 3 は `pin` 永続化のみとなり、cross-context 軸の lifecycle 操作は
switch (`Enter`) / close (`d` / `D`) / pin (`p`) / 新規 session (`N`) の 4 系統に集約される。

### 却下理由

- 同 session 内 window swap (`Shift+J/K`) は tmux native の `prefix+{` / `prefix+}` で同等。同 session 内の window 順を細かく入れ替える運用は薄く、カーソル追従ロジック（`cursorWinID` 維持）の実装コストに見合わない
- 別 session への move-window (`m`) は tmux native の `prefix+.` で代替可能。「動的に session を移す」シーンは頻度が低く、tmw で session を切るなら「最初から正しい session に作る」のが普通。2 段階モード（mark → カーソル移動 → drop）+ 視覚マーカー + Esc 取消 + session header 末尾挿入の実装コストに対する体感差が小さい
- 「sidebar dominant + native fallback」原則の下、native で済む操作を sidebar に持ち込む価値は薄い。rename / `n` の取り下げと同じ論理
- swap / move を落とすことで Phase 3 が pin 永続化だけになり、Phase 2 の close (`d` / `D`) と並んで mutate 操作の core が際立つ

### 影響範囲

- spec.md: 概要文の `move`、`### 並べ替え・移動` セクション全体（`Shift+J/K` と `m` 行）を削除
- design.md: `internal/tmux` 責務から `swap` / `move` を削除、modal sub-state 列挙から move-mark を削除、mutate 翻訳表の `Shift+J/K` / `m` 行、`## 並べ替え・移動` セクション全体を削除
- TODO.md: Phase 3 タイトルを「pin 永続化」に、`### window swap` / `### move-window` サブセクションを削除し、「採用しない・延期する項目」表に両方を理由付きで追加。実装順序根拠も `Phase 3 (pin)` に修正
- README.md: 概要文の `move`、実装中 lifecycle 列挙から `Shift+J/K` / `m`、normal mode キー表の並べ替え行 2 つを削除

---

## 多重選択 + バルク close（Phase 4）と vim 風ジャンプ（`gg`/`G`、Phase 7）の取り下げ

control surface 拡張の初版 spec で Phase 4 / Phase 7 として計画していた
multi-select + bulk close と vim 風先頭/末尾ジャンプを、実装着手前の見直しで取り下げた。

### multi-select + bulk close を取り下げた理由

- session 単位で消すなら `D` 一発、数個程度なら `d` 連打で吸収できる。「複数 window を一括 close したい」は housekeeping 的操作で、agent monitoring 主用途のこの sidebar では頻度が薄い
- 選択 visual を見落として一括 close する事故リスクが残る。confirm 強度を上げても「選んでいたつもりがなかった」事故は完全には防げない
- modal sub-state（select mode、bulk confirm の全件 idle vs 個別降格）と `Space` の役割競合（折りたたみ toggle が同じキー）の実装コストが close 単体より明確に重い
- swap / move / rename / `n` を同じ論理（native + 既存機能で吸収可能、頻度低い）で落としており、同じ判断基準を multi-select にも適用

### `gg` / `G` を取り下げた理由

- session/window 数が爆発的に多い運用は薄く、`j`/`k` 連打 + `/` 検索で目的行に到達できる
- 実装自体は軽いが、TODO.md の元の根拠でも「おまけ、いつでも入れられる」扱いだった。実需が顕在化していない段階で先回り実装する必要はない
- 必要になった時点で再追加すればよい

### Phase 番号の再採番

multi-select（旧 Phase 4）と vim 風ジャンプ（旧 Phase 7）を削除し、後続を繰り上げた。

| 旧 | 新 |
|---|---|
| Phase 1 (modal) | Phase 1 (modal) |
| Phase 2 (close) | Phase 2 (close) |
| Phase 3 (pin) | Phase 3 (pin) |
| Phase 4 (multi-select) | （削除） |
| Phase 5 (popup picker) | Phase 4 (popup picker) |
| Phase 6 (preview) | Phase 5 (preview) |
| Phase 7 (gg/G) | （削除） |
| Phase 8 (doctor/docs) | Phase 6 (doctor/docs) |

### 影響範囲

- spec.md: 操作（normal mode）から「多重選択 + バルク」セクション、`Space`（window 上）の multi-select 行、移動・切替表の `gg` / `G` 行を削除
- design.md: bulk close の confirm 降格ロジック、modal sub-state 列挙の multi-select、normal mode キー例から `gg`/`G` と古い `R`/`n` 残骸を整理
- TODO.md: Phase 4 / Phase 7 を削除して再採番、「採用しない・延期する項目」表に multi-select と `gg`/`G` を理由付きで追加、実装順序根拠を 6 phase に圧縮
- README.md: 実装中 lifecycle から `multi-select + bulk close`、normal mode キー表から「多重選択」行と移動列の `gg`/`G` を削除

---

## Phase 5（preview 拡張 + activity history）の全削除

control surface 拡張の初版 spec で Phase 5 として計画していた 3 機能
（capture-pane preview、unread badge、session 折りたたみ）を、実装着手前の見直しで全部取り下げた。
これにより Phase は 1 (modal) / 2 (close) / 3 (pin) / 4 (popup picker) / 5 (doctor/docs) の
5 段に圧縮される。

### unread badge（`!N`）を取り下げた理由

- permission/ask は **ユーザが応答するまで `💬` が継続表示** されるため、「放置されている window」はリアルタイムで判別できる。`!N` が拾える固有のケース（permission が来て自然に消えた履歴）は現実的にほぼ存在しない
- agent が permission/ask を出して応答なしで停止しても、その時点で `idle` ではないので継続的に `💬` が見える
- state file 形式拡張（`pane_N_event_log`）が必要で、ファイル append は dotfiles 側の Claude/Codex hook の責務 = **外部 repo への依存** が発生する。sidebar 単体で完結しないコストが大きい
- last-attached time 計算、switch 時の truncate、doctor の hook 設定確認の追加実装も必要

### capture-pane preview を取り下げた理由

- 既存の prompt preview（`pane_N_session_id` 経由の agent transcript initial prompt）で agent window の判別は十分。agent 不在 window 用の capture preview は「sidebar が agent monitoring 主用途」の文脈で需要が薄い
- 自己参照ループ防止（sidebar pane 自身を capture して再帰風になる）のガード、10 秒 tick の追加 IO、`previewMode` フラグなど実装コストが小さくない
- `tmux capture-pane` 単体は subcommand でも tmux native でも見れるため、sidebar 経由の差別化が弱い

### session 折りたたみを取り下げた理由

- 1 session あたりの window 数は実運用で通常 5 個前後。折りたたみたい欲求が顕在化していない
- pin で重要 session を持ち上げれば「見渡し」のニーズは吸収できる。折りたたみは pin の代替機能ではなく追加機能で、pin があれば優先度がさらに下がる
- `Space` キーを消費するため、将来別機能を割り当てたいときの競合リスク（multi-select を取り下げた際にも `Space` 競合が問題に挙がっていた）
- 状態が in-memory only（永続化しない）= 再起動のたびに展開状態に戻る = 利便性も限定的

### 副次的な整理

- spec.md 表示例から `▾` / `▸` を削除し、session header の装飾は `📌`（pin）と区切り線のみに
- spec.md Preview セクションを「prompt preview のみ」に簡素化（capture fallback を削除）
- spec.md Status バッジ表から `!N` 行と unread リセット記述を削除
- design.md から `## activity history (unread badge)` と `## capture-pane preview` セクション全体、`internal/state` 責務の「unread/履歴の管理」、状態ファイル一覧の `pane_N_event_log`、dotfiles 分担表の同言及を削除
- TODO.md の Phase 6 を Phase 5 に再採番、doctor の `pane_N_event_log` 仕様確認も削除

---

## pin の意味を「表示順」から「表示順 + 削除保護」に拡張した経緯（Phase 3）

Phase 3 の初期実装では pin の semantics を **表示順だけ** とし、`D`（session kill）に対する保護は持たせていなかった。`pinned_sessions` ファイルの整合性については「kill 成功時に該当 session 名を自動で削除する」サイドエフェクトで担保していた（同名 session を後で作ったときに残骸 entry で意図せず pinned になるのを防ぐため）。

レビューで「pin したものが `D` で消せるのは違和感」「整合性を取るなら自動削除より kill ブロックの方が筋が通る」と指摘を受けて方針を反転した。最終仕様:

- `D` 押下時に対象 session が pinned ならブロック → message line で `p` による unpin を案内
- `d`（window kill）は session 単位ではないので非対象
- 自動削除ロジックは撤去（kill が通らないので残骸は構造的に発生しない）

### 採用しなかった代替

- **自動削除（初期案）**: kill 時に pinned 行をサイドバーが書き換える。ファイルが意図せず書き換わる驚き、pin の semantics が緩い（「削除可能だが上に並ぶ」という弱い意味）、設定ファイルがミューテートされる動作の発見しにくさが問題
- **弱保護（confirm 文に [pinned] ラベルだけ表示）**: 誤爆耐性は上がらず、`y` を押せば結局 kill が通る。「ラベル表示」だけでは意味的整合が取れない

---

## pin の管理を `p` キー toggle からファイル一本管理に変えた経緯（Phase 3）

削除保護を入れた直後の実装では `p` キーで pin/unpin の toggle を提供していた（カーソル window の所属 session を即時 pin、`pinned_sessions` ファイルへ自動書き戻し、削除保護のために kill 前に unpin する経路として機能）。

レビューで「pin/unpin は頻繁な操作ではない（pin 対象は安定的なメンバーで、1 度設定したら長く使う）」「ファイル一本でいいのでは」と指摘を受けて方針を転換した。最終仕様:

- `p` キーは廃止（footer hint からも削除）
- pin/unpin は `~/.config/tmux-sidebar/pinned_sessions` を **エディタで直接編集**
- 編集は sidebar 側の `loadData()` 起点で毎回 `config.Load` を呼び直すことで自動反映（最大 10 秒ラグ）
- 削除保護のメッセージも「`p` で unpin」から「`pinned_sessions` から該当行を削除して unpin」に変更

利点:
- 内部 API が縮小（`config.WritePinnedSessions` / `config.TogglePinned` を撤去、Model から `pinnedPath` を撤去）
- pin の順序変更・コメント記入・複数項目の一括編集はエディタで自然にできる（toggle では末尾追加しかできず、順序変更は手動編集が必要だった）
- キーバインド `p` を将来の他機能のために空けられる
- 「設定ファイルが意図せず書き換わる」副作用がそもそもなくなる

採用しなかった代替:
- **fsnotify で `~/.config/tmux-sidebar/` を監視して即時反映**: 実装コストはあるが、pin/unpin の頻度の低さに対しては過剰。10 秒 tick での自動反映で実用上問題ない

---

## dispatch を Go で取り込んで tmux-sidebar の subcommand に格上げした経緯（Phase 4 追加）

popup picker mode の Step 2 (mode 選択) を最初に組んだ時点では、`dispatch` mode は「`claude /dispatch ` を pre-fill して送る」という形にしていた。`/dispatch` は dotfiles 配信の Claude Code skill (`~/.claude/skills/dispatch/SKILL.md` + `dispatch.sh`) であり、tmux-sidebar 側は名前を呼ぶだけだった。

しかし dispatch には 2 つの利用経路がある:

1. **Claude session 内の `/dispatch` slash command**: LLM が引数解釈・branch 名生成・in-session 判断を行ってから `dispatch.sh launch` を呼ぶ
2. **tmux popup launcher (`prefix+S` の `dispatch_launcher.fish`)**: ユーザが fzf で repo を選び prompt を打って即起動

両者は `dispatch.sh` を共通 engine としていたが、(1) は LLM の意思決定が必要な semantic 層、(2) は決定論的な「worktree 作成 + tmux session 起動 + send-keys + prompt-file injection」だけで完結する deterministic 層、と性質が違う。同じ shell スクリプトに 2 つの役割を持たせていたため、将来 (1) と (2) で要件が乖離した時に破綻する設計だった。

方針転換:
- deterministic 層を `internal/dispatch` パッケージとして Go で再実装し、`tmux-sidebar dispatch <repo> [prompt] [flags]` subcommand として公開
- popup picker mode の `dispatch` mode も Step 3 で prompt 入力欄を出して、内部で `internal/dispatch.Launch` を直接呼ぶ
- dotfiles 側の `dispatch.sh` は将来的に `tmux-sidebar dispatch "$@"` の thin wrapper になる前提（dotfiles 側の置き換えは別 PR）。Claude skill SKILL.md は変更不要（呼び出し先が同じ CLI を保つ）

採用しなかった代替:
- **dispatch を tmux-sidebar に取り込まず picker から外す（Option A）**: dotfiles の `prefix+S` で十分という考え方は成立するが、sidebar の `N` から dispatch まで導線が繋がる開発体験を失う
- **runtime check で `dispatch.sh` の存在を確認するだけ（Option B）**: 既存の thin wrapper 関係をそのまま延命するだけで、共通 engine の二面性問題（slash command と launcher の要件乖離）は何も解決しない
- **orchestrate も同じ枠組みで取り込む**: orchestrate は dotfiles 側でも実用に至っておらず、取り込むほどの YAGNI 圧力がない。Phase 4 の picker からも除外した

利点:
- dispatch の deterministic な workflow（worktree 命名規則、`@<branch-dirname>` suffix、衝突回避、`.claude/settings.local.json` コピー、tmux session 名衝突回避、codex の attached client 待ち）が Go の単一 source of truth になる
- tmux-sidebar 単体（dotfiles なし）でも `tmux-sidebar dispatch` で worktree + launcher 起動が完結する
- skill SKILL.md の API は維持しつつ engine だけ移管できる（互換切替を段階的にできる）

実装上の注意:
- git 操作は go-git ではなく `os/exec` で git CLI を呼ぶ。dispatch.sh の挙動を再現する確実性を優先（worktree porcelain 解析、`origin/HEAD` 検出、`fetch origin <branch>:<branch>` 等）
- `~/.config/dispatch/no-worktree-repos` の設定は引き続き読む（dotfiles ユーザの既存設定を尊重）
- 構造化出力 (`STATUS:` / `SESSION:` / ...) は dispatch.sh と同じ形式を保つ（grep する Claude skill / scripts の互換性のため）

---

## popup wrapping を `tmux-sidebar new` から外し、呼び出し側に委譲した経緯（Phase 4 追加）

`tmux-sidebar new` を直接 CLI から叩いた時にも popup として表示してほしいという UX 要望を受けて、subcommand 自身が `tmux display-popup -E` で self-exec するラッピングを一度実装した（`shouldWrapInPopup` + `relaunchInPopup` + `TMUX_SIDEBAR_NO_POPUP=1` sentinel で再帰防止）。pane mode の `N` キーも単に `tmux-sidebar new --context=<f>` を呼ぶだけになり、ユーザは「popup 起動方法を知らなくていい」体験になっていた。

レビューで「popup 化の責務は呼び出し側に寄せる方が設計・テスト観点で良いのでは」と指摘を受け、再検討して逆方向に振り直した。最終仕様:

- `tmux-sidebar new` は picker TUI を **その場のターミナルで** 起動するだけ（popup framing なし）
- popup として表示したい場合は呼び出し側が `tmux display-popup -E -w 80 -h 24 'tmux-sidebar new'` で囲む
- pane mode の `N` キーは `internal/ui.launchPopupViaTmux` の中で display-popup を組み立てて呼ぶ（call site が popup geometry を所有する）
- tmux.conf の bind-key 例（`bind-key N display-popup -E -w 80 -h 24 'tmux-sidebar new'`）を setup.md §10 に追加

採用しなかった代替（一旦実装したが破棄した内側 wrapping）:
- **subcommand 内部で `shouldWrapInPopup` 判定 + self-exec**: 「subcommand が文脈で起動方法を変える」のは Unix 的でなく、`ls` が paginate を勝手に挟むようなアンチパターン。`TMUX_SIDEBAR_NO_POPUP` env sentinel も再帰防止の workaround で設計ではない
- **--popup フラグで明示**: 呼び出し側が「popup にする」を選ぶなら `tmux display-popup -E` を直接書けばよく、二重の syntax は要らない

利点:
- subcommand の責務が「picker TUI を動かす」だけになり予測可能性が上がる
- popup geometry (`-w 80 -h 24`) のハードコードが消え、呼び出し側で `-w 100 -h 30` や全画面に変えられる
- subcommand の単体テストに env sentinel ガードが要らない（self-exec の中間プロセスを意識しなくていい）
- split-window や非 tmux 環境で `tmux-sidebar new` を叩いた時の挙動が「裸で picker が動く」と単純で説明しやすい
- 可逆性: 後で「便利モードとして内側 wrap を opt-in できる `--popup` フラグを追加」は可能。逆方向（内側 wrap を後で外す）は破壊的

副次的な変更:
- main.go から `runNew` / `shouldWrapInPopup` / `relaunchInPopup` / `shellQuoteMain` の wrapping ロジックを削除し、`runNew` は picker bubbletea を直接起動するだけに
- internal/ui の `launchPopupViaTmux` を `tmux display-popup -E -w 80 -h 24 '<bin> new --context=<f>'` 直接呼びに戻し、ローカル `shellQuote` を再導入
- `TMUX_SIDEBAR_NO_POPUP` env var を documentation から削除

---

## picker mode を 3-step (repo → mode → prompt) から 2-step (repo+launcher → prompt) に再編した経緯（Phase 4 追加）

dispatch を Go に取り込んだ直後の picker は 3-step だった: Step 1 で repo 選択、Step 2 で claude / codex / dispatch の radio mode 選択、Step 3 で（dispatch のときだけ）prompt 入力。これは「claude / codex の素起動」と「dispatch の worktree + prompt フロー」を mode 選択で分岐させる設計だった。

レビューで「dispatch_launcher.fish の UX に揃える方が運用上わかりやすい」「mode 選択は実質 launcher (claude / codex) 選択でしかない」という指摘を受け、2-step に再編した。最終仕様:

- Step 1 (repo 選択): `Tab` で claude ↔ codex を toggle、ヘッダーに current launcher を表示。`Enter` で既存 session があれば switch、無ければ Step 2 へ
- Step 2 (prompt 入力): `claude / codex  <repo>` ヘッダー + `> ` 入力欄。`Tab` で launcher 再 toggle、`Enter` で dispatch 実行
- 「claude / codex 素起動」mode は廃止。picker からは常に dispatch 経由で session を作る

採用しなかった代替:
- **素起動 mode を別 step として残す**: 1 つの UI に「素起動」と「prompt 付き dispatch」が同居すると、「Enter で何が起きるか」が context 依存になり予測しづらい。素起動が欲しい場合は CLI (`tmux-sidebar dispatch --no-prompt`) や tmux native (`prefix+c`) で済む頻度
- **空 prompt = 素起動 として silently 解釈**: 「空 Enter で何が起きるか分かりにくい」UX 問題が残る。明示的にエラー表示する方が安全
- **claude モード時のみ Step 2 で dispatch ↔ orchestrate を tab 切替（dispatch_launcher.fish 互換）**: orchestrate は採用していないので不要

利点:
- dispatch_launcher.fish と UX が一致し、既存ユーザの mental model がそのまま使える
- mode 選択 step が消えて画面遷移が 1 段減る（repo → prompt の 2 段）
- Tab で launcher を切り替える操作が両 step で対称（Step 1 でも Step 2 でも同じ挙動）
- Runner interface から `HasSession` / `NewSession` / `SendKeys` を削除でき、`SwitchClient` + `Dispatch` の 2 メソッドだけになる

副次的な変更:
- `Mode` / `modes` 配列, `commandFor`, `execChoice`, `stepMode`, `viewMode`, `modeIdx` を削除
- `launcher dispatch.Launcher` フィールドを Model に追加（default: claude）、`toggleLauncher` を Tab で呼ぶ
- `viewPrompt` のヘッダーをスクリーンショット指定通り 2 行構成 (`tab: モード切替 enter: 実行 :<branch> で既存 remote branch を checkout` + `claude / codex  <repo>` + 区切り線) に変更
- ExecRunner からも `HasSession` / `NewSession` / `SendKeys` を削除（dead code）

---

## codex 起動時の attached client 待機が picker から deadlock していたのを Options.Switch で解消した経緯（Phase 4 追加）

ADR-065 (`dispatch.sh` の codex launcher は attached client が来るまで待機する) の挙動を `internal/dispatch.waitForAttachedClient` として移植したが、picker から呼ぶと 5 分間ブロックする deadlock になっていた。

原因:

- dispatch.sh は `tmux run-shell -b` で **background** 実行され、popup 終了後にユーザが手動で `tmux switch-client` した時点で待機が解除される設計
- tmux-sidebar の picker は `dispatch.Launch` を **同期** 呼び出ししていた（同じ Go プロセス内）
- 結果として「session 作成 → 待機ループ → 誰も attach しない → 5 分タイムアウト → send-keys」になり、ユーザは popup が固まったように見える

修正:

- `dispatch.Options.Switch bool` を追加し、true のとき `createTmuxTarget` 直後に `tmux switch-client -t <session>` を実行
- picker は `Switch: true` を渡すので、待機ループ突入時には既に client が attach 済み → 即時 send-keys
- main.go の `tmux-sidebar dispatch` CLI には `--switch` フラグを追加。デフォルト false で dispatch.sh と挙動互換、明示時のみ switch する

採用しなかった代替:
- **dispatch.Launch を goroutine で非同期実行**: エラーが伝播しなくなる、picker の状態管理が複雑化
- **picker から待機を skip するフラグ（`SkipWait` 等）**: codex の OSC 11 解決に attached client が必要なのは変わらないので、skip しても codex は色なしで起動する。「switch を先にやる」方が原因に対する解決
- **dispatch.sh と同じく background 起動**: 同一プロセス内の goroutine とは別の問題（picker quit 後の生存）が出る、ライフサイクルが煩雑

副次的な変更:
- picker.execDispatch から `runner.SwitchClient(name)` の post-dispatch 呼び出しを削除（switch は dispatch.Launch の中で行われる）
- Step 1 の「既存 session があれば switch」経路は引き続き `runner.SwitchClient` を使う（dispatch.Launch を経由しないため）

---

## picker prompt の multi-line paste 対応（Phase 4 追加）

dispatch_launcher.fish の `read -p` は paste で複数行を受け付ける（コメント `# 改行を含むペーストに対応:` から明示）。tmux-sidebar の picker でも同等の挙動を取り込んだ。

挙動の整理:

| 観点 | 状態 |
|---|---|
| bubbletea の bracketed paste | デフォルト有効。paste 全体が `KeyMsg{Type: KeyRunes, Paste: true, Runes: [...]}` 1 件で届く |
| ターミナルが paste 中の LF を CR に変換する慣例 | あり（多くの terminal で `\n` → `\r`） |
| CR / CRLF / LF が混在しうる | あり（OS や端末・コピー元の差異） |

修正:

- `picker.handlePromptKey` で `msg.Paste == true` のとき `\r\n` / `\r` を `\n` に正規化してから `m.prompt` に append（`normalizeNewlines`）
- `dispatch.firstLine` を `\n` / `\r\n` / `\r` のどれでも改行扱いに変更（defense-in-depth: CLI 直叩きや thin wrapper 経由で正規化されていないペイロードが来ても branch 名生成が破綻しないように）
- `picker.viewPrompt` を multi-line 対応に書き直し:
  - 先頭行は `> ` プレフィックス（bold）
  - 続く行は `│ ` で indent（faint）してカーソル列を揃える
  - cursor `▏` は最終行末尾にだけ表示

採用しなかった代替:
- **手キー入力での改行サポート（Shift+Enter / Ctrl+J で newline 挿入）**: terminal によって detection が不安定。dispatch_launcher.fish も持っていないので非対称になる
- **bubbles/textarea component の導入**: 依存追加に対する得が薄い（multi-line paste しか使わない）。自前 split + render で十分
- **paste 中の `\r` を文字としてそのまま保持**: `firstLine` が破綻し、render 時に terminal の CR 解釈で表示が崩れる（cursor が column 0 に戻って後続が前を上書き）。実際に検証で破綻を確認済み

---

## キーボードでの改行 + dispatch 非同期化（Phase 4 追加）

multi-line paste を入れた直後、ユーザから 2 件のフィードバック:

1. Shift+Enter を押すと dispatch が誤発火する（terminal によっては Shift+Enter が plain Enter として届くため、prompt の途中で submit してしまう）
2. Enter 押下後 → session 作成までのラグが「ハングしているのか正常動作中なのか」分からない

### Shift+Enter / Alt+Enter / Ctrl+J で newline 挿入

`isNewlineKey` が以下を識別して `\n` を prompt に挿入する:

- `KeyCtrlJ`: terminal 非依存。LF (0x0a) は Enter (CR, 0x0d) と別のキー扱いなので確実に区別できる
- `msg.String() == "shift+enter"` / `"alt+enter"`: bubbletea v1 でも terminal が **kitty keyboard protocol** 等で modifier 付きエンターを識別できるシーケンスとして送る場合だけ届く。Ghostty + tmux passthrough が有効な環境では効く

採用しなかった代替:
- **bubbletea v2 へのアップグレード**: API 互換性が取れない範囲が大きい。Shift+Enter のためだけに移行する価値が薄い
- **Shift+Enter サポートを諦めて paste のみ**: ユーザの自然な expectation（多くのチャット UI が Shift+Enter で改行）に逆行する。Ctrl+J が確実なフォールバックとして用意されているなら、Shift+Enter は best-effort で動けばよい

### dispatch を非同期化 + spinner status

dispatch の処理は worktree 作成（fetch + add）と tmux session 生成で 2-5 秒かかる。同期 Dispatch だと bubbletea の Update が return しないので画面が固まり、ユーザは「ハングしたかも」と感じる。

修正:

- `execDispatch` が `tea.Batch(dispatchCmd, spinnerTick())` を返す。`dispatchCmd` は goroutine で `runner.Dispatch` を回し、完了後に `dispatchResultMsg` を送る
- `Model.dispatching bool` / `dispatchTarget string` / `spinFrame int` を追加
- `viewPrompt` は dispatching=true の間、入力欄と branch preview の代わりに `⠋ dispatching <repo>...` を spinner 付きで表示
- `spinnerTickMsg` を 100ms 間隔で受け取り、frame を進めて次の tick を schedule。dispatching=false になったら自然に tick chain が止まる（goroutine リーク無し）
- 処理中の キー入力は `handlePromptKey` の先頭で drop する（Enter 連打で重複 dispatch を防ぐ）
- `dispatchResultMsg` 受信時に dispatching=false にし、err なら errMsg、成功なら quitting + statusMsg

採用しなかった代替:
- **spinner 無しで static "dispatching..." だけ表示**: 動作確認的には十分だが、「動き」がないと不安が残る。100ms tick の cost は無視できる小ささ
- **bubbles/spinner component の導入**: 依存追加に対する得が薄い。10 文字の Unicode frame array で十分

---

## picker を fire-and-forget 化（popup 即閉じ + dispatch は run-shell -b）（Phase 4 追加）

dispatch を非同期化 + spinner status を入れた直後、ユーザから「TUI のなかに status 表示してしまうと tmux session 起動までユーザは同期的に待たされることになるよね？」という指摘を受けた。これは正しい。

問題の本質:
- picker は popup process 内で動いている
- `dispatch.Launch` を goroutine で呼んでも、popup 自体が `dispatchResultMsg` を待つ
- 結果として popup が dispatch 完了まで開いたまま = ユーザは閉じる操作以外できない
- spinner で「動いてる感」を出してもユーザの待ち時間そのものは縮まらない

dispatch_launcher.fish を改めて読み直すと、これは `tmux run-shell -b` で **完全に分離** している:

```fish
tmux run-shell -b "bash ~/.claude/skills/dispatch/dispatch.sh launch ..."
```

popup 側 (fish process) は `run-shell -b` 一発で fire-and-forget して即終了。dispatch.sh 側は tmux server が管理する別プロセスとして worktree 作成・session 起動・send-keys を行う。エラーは dispatch.sh 側の `die` が `tmux display-message -d 5000` で通知。

修正:

- `Runner.Dispatch(opts) (string, error)` を `Runner.SpawnDispatch(opts) error` に置き換え
- `ExecRunner.SpawnDispatch` は `tmux run-shell -b 'tmux-sidebar dispatch <args>'` を発火して即 return
- picker.execDispatch は SpawnDispatch を呼んで成功したら `tea.Quit` を返すだけ。dispatch 完了は待たない
- `Options.ToArgs() []string` を新設して、Options を `tmux-sidebar dispatch` の argv tail に変換
- `WriteTempPrompt(string) (string, error)` を新設して、prompt 本文を tempfile に書く（spawn される CLI の `--prompt-file` で渡す。シェル経由で literal を渡すと改行・metacharacter が壊れるため）
- main.go の `case "dispatch":` エラー処理に `tmux display-message` を追加。`tmux run-shell -b` で stderr が破棄されてもユーザに見えるようにする
- spinner / dispatching state / dispatchResultMsg / spinnerTickMsg を全部削除（同期化が unfeasible になったので不要）

採用しなかった代替:
- **picker process 内で goroutine + popup を delay close**: bubbletea のライフサイクル外で popup を閉じる確実な手段がない。picker process が exit しないと popup は開いたまま
- **`exec.Command(...).Start() + Process.Release()` で直接 fork**: tmux に管理されないので、ssh disconnect 等の signal が dispatch process に届くと死ぬ。`tmux run-shell -b` は tmux server プロセスの子になるので、ユーザのシェルが閉じても生存する

利点:
- popup は Enter 押下から < 300ms で閉じる（実機で確認）
- worktree 作成や git fetch（数秒）は完全に非同期。ユーザは popup を閉じた直後から他の操作ができる
- dispatch 完了時の `Switch=true` で新 session に自動 attach される（ADR-065 codex 待機も attach 後なので問題なく動く）
- dispatch_launcher.fish と挙動・実装パターンが一致 → 将来 dotfiles 側を thin wrapper 化したときに違和感がない

副次的な変更:
- ExecRunner から `Dispatch` を削除し `SpawnDispatch` を追加（in-process Launch は CLI 経由でのみ呼ばれる形に）
- `dispatch.Options.Prompt` フィールドは picker 経路では使われなくなった（PromptFile 経由）。CLI 直叩きのときだけ使われる
- picker_test の `TestPickerDispatchFlowClaude` 等は PromptFile に書かれた内容を読んで assert する形に変更
- `TestPickerKeysIgnoredWhileDispatching` は dispatching state ごと削除されたので不要に。代わりに `TestPickerSpawnErrorShownNotQuit` で SpawnDispatch エラー時の挙動を pin down

---

## branch 名生成を機械 slugify から LLM (`claude -p`) + slugify フォールバックに変更（Phase 5 追加）

### 動機

それまでは `dispatch.BranchFromPrompt` の `[^a-zA-Z0-9]` を `-` に潰す決定論的 slugify が popup picker から呼ばれていた。意図的に `dispatch_launcher.fish:124` の slug derivation を mirror して同等の branch 名が出るようにしてあった。

不満点:
- 日本語のみの prompt（例: `"todo.md フェーズの実装"`）は ASCII 部分しか拾えず、`feat/todo-md` のような短すぎ・情報量不足の名前になりがち
- 英単語混在の prompt も語順そのままに長い slug を吐く（例: `"add a new health check endpoint"` → `feat/add-a-new-health-check-endpoint`）
- 結果として 40-col の sidebar に session 名 (`<repo>@<slug-with-dashes>`) が収まらず折り返す

dotfiles の `/dispatch` skill 経由（Claude Code が直接呼ぶ経路）は LLM が prompt 内容から `feat/<短い説明>` を生成していたが、popup 経路（fish の `dispatch_launcher.fish` および tmux-sidebar の picker）はその恩恵を受けていなかった。

### 採用した方式: dispatch サブプロセス側で `claude -p` を呼ぶ

- `internal/dispatch/branch.go` に `Namer` interface と `ClaudeNamer` struct（`claude -p --system-prompt <namer rules> <user>` を 5s timeout で起動）を追加
- `DeriveBranch(ctx, namer, prompt)` で「namer 出力を `^(feat|fix|chore)/[a-z0-9][a-z0-9-]{1,24}$` で検証 → 合格なら採用、不合格なら `BranchFromPrompt` にフォールバック」を一括ルーチン化
- `dispatch.Launch` に `namer Namer` 引数を追加。`opts.Branch == ""` かつ worktree 作成時に `DeriveBranch` を呼ぶ
- `main.go` の `runDispatch` は `dispatch.ClaudeNamer{}` を渡す。CLI 直叩き経路でも LLM 命名が効く
- popup picker は **branch 名を一切設定しない**（checkout モードを除く）。`opts.Branch` 空のまま `tmux run-shell -b` で dispatch サブプロセスを spawn する
- popup の入力中プレビューは `branch:` から `slug:` ラベルに変更し、「これは fallback 用の決定論的 slug で、実際の branch 名は LLM 出力で異なる場合がある」というニュアンスにする
- system prompt 内で **40-col sidebar に収まる長さ（≤20 chars 推奨、25 chars 上限）** を明示的に縛る。`branchShapeRE` の char class でも上限を強制

### 採用しなかった代替

- **popup 内で `claude -p` を同期呼び出し + spinner 表示**: 1-5 秒の待ち時間が popup に乗ると、Phase 4 の fire-and-forget 化（即閉じ）の前提が崩れる。spinner / dispatching state / dispatchResultMsg は意図的に削除した経緯があり、それを蒸し返すのは筋が悪い
- **temp slug で worktree を先に作って後から `git branch -m` で rename**: rename 失敗時の状態が複雑（worktree dir 名と branch 名がズレる）、tmux session 名も後追いで変える必要があり実装重い
- **picker 内で LLM 案を表示 → ユーザに承認させる確認モード**: 1 ステップ増えて UX が遅い。「fire-and-forget で即閉じ」のリズムと噛み合わない
- **codex を命名にも使う（`codex exec` 経由）**: codex は code-tuned で短答が苦手、レイテンシも重い。命名は launcher choice と直交させ、常に `claude -p` に固定。codex 単独ユーザは `claude` 不在の slugify フォールバックで動く

### 採用しなかったロジック上の選択肢

- **`Namer` を `Options` のフィールドに含める**: `Options.ToArgs()` で argv 化されるので serializable な値しか入れたくない。`Launch` の追加引数として渡す形にした
- **package-level の default namer 変数 + `SetDefaultNamer()`**: 隠れたグローバル状態は test に優しくない。明示的に引数で渡す
- **正規表現で hyphen 連続を許す広めの shape**: `feat/--foo` のような奇形を弾けないので、`{1,24}` で min length も 1 にして leading hyphen も禁止

### 残課題（将来）

- `claude` CLI の認証切れを 1 度検出したら以後その popup 起動中はスキップする per-session キャッシュ（毎回 5s 待つ無駄を消す）
- `doctor` に `claude -p` 疎通チェックを追加して、初期セットアップ時に LLM 命名が効くか可視化する
- LLM 名と既存 branch の衝突時に `feat/foo-2` のような suffix を付ける衝突回避ロジック（現状は `worktree.CreateWorktree` の resume 動作に依存している）

---

## picker の auto-switch を撤回し、display-message 通知だけにとどめた経緯（Phase 5 追加）

### それまでの方針

`Options.Switch=true` を picker から強制し、dispatch サブプロセスが session 作成直後に `tmux switch-client -t <session>` を発火させていた。理由は二つ:

1. ユーザが popup で Enter を押してから新 session に attach するまでが 1 stroke で完結する利便性
2. codex 起動時の `waitForAttachedClient`（ADR-065 / openai/codex#4744）を抜けるため、attached client が早期に必要だった

### 反転の動機

実運用で「作業中の pane が突然新 session に切り替わる」のがユーザ体験として強すぎることが分かった。typing 途中で session が乗っ取られ、popup 起動前に見ていた transcript / log を失う。新 session を作ったことそのものは sidebar に反映されるので、ユーザは attach したいタイミングで `prefix s` か sidebar から自分で飛べばよい。

### 採用した方式

- `internal/picker/picker.go` の `execDispatch` が組み立てる `Options` から `Switch: true` を外した（zero value の false で初期化）
- launch 成功時の通知は **出さない**。最初は `tmux display-message -d 5000 "dispatch: launched [<session>]"` を残す案だったが、(1) 数秒で消えるので見逃しやすく信頼できる通知になっていない、(2) 新 session は reload tick (最大 10s、SIGUSR1 で即時) で sidebar に出現するため status line と sidebar に同じ情報が出ることになる、(3) status line のノイズが増えるだけ、という理由で削除した
- codex の `waitForAttachedClient` は **そのまま残す**。dispatch サブプロセス内で 5 分間 polling して、ユーザが attach するのを待つ。dispatch.sh CLI と同じ挙動。timeout した場合は OSC 11 background-color query が応答されないまま codex が起動するが、入力枠の背景色が崩れるだけで動作はする
- 失敗時の通知は `main.go:runDispatch` のエラーハンドラに既にある `tmux display-message "tmux-sidebar dispatch: <err>"` をそのまま使う（成功時とは違い、失敗は sidebar から読み取れない情報なので display-message が必要）
- `dispatch.Options.Switch` フィールドおよび `--switch` CLI フラグは残す（自動化スクリプト等の明示的「飛びたい」用途）

### 採用しなかった代替

- **launcher が claude のときだけ Switch=false、codex のときは Switch=true**: 「launcher 種類で UX が一貫しない」のが説明しづらい。codex の OSC 11 失敗はソフトな副作用で、致命的でないので claude と挙動を揃えるほうが筋がよい
- **`waitForAttachedClient` の timeout を 30s に縮める**: ユーザがチャットや別作業に集中している間に過ぎる可能性が高く、結局 OSC 11 失敗で起動する確率を上げるだけ。5 min なら attach の機会窓が現実的に十分
- **launch 完了後に `tmux switch-client -t <session>` ではなく `tmux display-popup` で「attach しますか？」を聞く**: popup を再度開く UX が冗長。display-message で session 名を見せれば十分

### 影響範囲

- `TestPickerDispatchFlowClaude` は `Switch == false` を期待する形に書き換え（旧: `!Switch` で fail）
- `TestPickerCheckoutMode` は元から Switch を assert していないので影響なし
- spec.md と design.md の auto-attach に関する記述を「display-message で通知 → 手動 attach」に書き換え
- README とユーザ向けセットアップに自動移動する旨を書いた箇所はないので影響なし

### 補足: ADR-065 との関係

ADR-065 は「codex は attached client が必要」という事実そのものを否定するものではない。codex の OSC 11 background-color 取得失敗は **致命的でないコスメティック問題** であり、ユーザが急いで attach しなくても codex プロセスは生存する。ADR-065 で picker が Switch=true を強制したのは「prompt 投入と OSC 11 を両方確実にするため」だったが、ユーザの作業を奪うコストの方が大きいと判断して反転した。OSC 11 を確実にしたい codex ユーザは `prefix s` か sidebar からすぐに attach すればよい。

---

## 新規 session を sidebar 上で時限ハイライトする「fresh session」表示の追加（Phase 5 追加）

### 動機

dispatch 完了通知として display-message を撤廃した結果、「dispatch が成功したのか分からない」という認知ギャップが残った。新 session は sidebar に出現するが、長いリストの中で見落としやすく、「いま作ったやつ」を一目で識別できない。display-message も sidebar も持たない第三の affordance として、**新 session を時限的に色付けする** ことにした。

### 採用した方式

- `tmux.PaneInfo` に `SessionCreated time.Time` を追加し、`ListAll` の format string に `#{session_created}` を 10 列目として追加。`parseAllPanes` は Unix epoch をパースし、不正値は zero time で握りつぶす（古い session が誤ってハイライトされない方向に倒す）
- `ui.ListItem` に同フィールドを伝播し、render で `time.Since(SessionCreated) < 10s` のとき session ヘッダ行と所属 window 行を緑系の foreground (`colRunning`) で描く
- ハイライト解除を確実にするため、**fresh session が存在する間だけ走る 1Hz tick** (`freshTickMsg`) を導入。`Model.freshTicking` でスタッキング防止し、fresh が無くなった tick で自動停止して idle 時の cost をゼロにする
- 閾値は `freshSessionWindow = 10 * time.Second`。これは「dispatch から sidebar 反映までの reload tick (≤10s) + ユーザが視線を戻す時間」を覆うバランス点

### 採用しなかった代替

- **常時 1Hz tick**: 待機時の wakeup を毎秒 1 回固定で消費する。fresh session が存在しないときは redraw 不要なので、条件付き tick の方がエネルギー効率が良い
- **閾値を 30s に伸ばす**: 「最近性」のメンタルモデルが薄まり、本当に新しいかどうかの discrimination 力が落ちる。10s 程度が「ついさっき作った」と感じる範囲
- **行末に `[NEW]` バッジを付ける**: 40-col の限られた幅で badge 領域を 1 つ占有することになり、agent タグ / running バッジ / PR 番号と競合する。色だけで表現すれば layout はそのままで済む
- **カーソルを新 session に自動で移動する**: 「auto-switch を撤回した」設計と整合しない。ユーザがいま見ている行を勝手に動かさない方針を一貫させる
- **カラーを赤・黄など警告系**: 新規作成は警告ではないので緑（既存の `colRunning` を流用）が自然。running バッジと色は同じだが、render する位置と装飾（ヘッダの bold）が違うので混乱はない

### 残課題

- pinned session が新規作成された場合 (今は非常に稀だが将来 pin の動的管理が入った場合) の見た目は `📌` + 緑色になる。`📌` のメタ情報と「fresh」のメタ情報が同居するが、競合はしない
- サイドバーが非フォーカスの状態で 10 秒が経過すると、そのフレームで色が落ちる。再描画タイミングが微妙にズレる場合があるが、視認上の影響は小さい

---

## サイドバー `N` キーで popup picker を起動するパスを撤回（2026-05-03）

### 動機

サイドバー pane mode から `N` を押すと内部で `tmux display-popup -E ... 'tmux-sidebar new --context=...'` を呼び出し、開いている session の重複検出のためにコンテキスト JSON を一時ファイルに書き出して picker に渡していた。実運用では「サイドバーを開いた状態で `N` を押す」というユースケースが想定通りに使われておらず、popup picker は tmux.conf bind-key (`prefix + N` 等) から直接起動する方が普通の tmux UX に揃って自然、という判断に至った。設計上の二重エントリポイント（サイドバー → picker と bind-key → picker）が、実装側に context file 受け渡しという余計な機構を抱え込ませていた。

### 採用した方式

- `internal/ui/model.go` から `case 'N'` ハンドラと popup 起動関連 (`launchPopupPicker` / `launchPopupViaTmux` / `writePopupContext` / `popupSessionInfo` / `popupClosedMsg` / `popupLauncher` / `shellQuote`) をまとめて削除
- `tmux-sidebar new` サブコマンドそのものは残し、tmux.conf からの bind-key 経由で `tmux display-popup -E -w 80 -h 24 'tmux-sidebar new'` を呼び出すのを唯一の起動経路にする
- `--context=<file>` フラグを廃止。`internal/picker.Context` / `WriteContext` / `ReadContext` / `SessionInfo` を全部削除し、`picker.New(repos, openSessionNames, runner)` というシグネチャに簡素化
- 重複 session 検出（同名 session が既に開いている repo を dim 表示し、Enter で switch）は機能維持。`runNew` 側で `tmux.NewExecClient().ListSessions()` を呼び、session 名スライスを picker に渡す形にした
- ドキュメントは setup.md §10 を「bind-key を書く」の単一手順に圧縮、spec.md からは「`N` で popup picker mode を起動」の記述を撤回し picker mode は独立エントリポイントとして書き直し

### 採用しなかった代替

- **`N` キーは残しつつ context 渡しを削除し、picker 側でも tmux 直問い合わせで dup 検出する**: サイドバー起動経路を残す価値が見えない（重複したエントリポイント）。残すと「どっち推奨？」の判断を README/spec に永続的に残すことになる
- **popup 起動機構を完全に残し、`N` のキーバインドだけ封印する**: dead code が残るうえ、将来別のキーで再有効化したくなったときに同じ context-file 機構を引きずる。今やめるのが楽
- **`--context` フラグを残し、内部実装だけ削除する**: 互換のための「使われない引数」を CLI に残すと、後から誰かが期待値を勘違いする。サイドバー以外に書く caller がいないので消し切る方が誠実

### 影響範囲

- `internal/picker/context.go` および `internal/picker/context_test.go` を削除
- `internal/picker/picker.go` の `New` シグネチャ変更に伴い `picker_test.go` を更新（`New(Context{}, ...)` → `New(..., nil, ...)`、`Context{Sessions: ...}` → 文字列スライス）
- `internal/doctor/doctor.go` のメッセージから `popup picker (\`N\`)` の表現を `\`tmux-sidebar new\` popup picker` に変更
- `main.go` の `runNew` を簡素化（`--context=` パース削除、tmux 直問い合わせで openSessionNames を構築）
- `docs/spec.md`: `N` の Lifecycle 表エントリ削除、Popup picker mode 節の前文を独立エントリポイントとして書き換え、Subcommands 表の `new` 行から「sidebar の `N`」削除
- `docs/setup.md` §10 を rewrite（picker 挙動の重複説明を spec.md にリンクで集約）

### 残課題

- 既存ユーザの tmux.conf にサイドバー側の `N` 想定で bind を書いていない人は、§10 の bind-key 例を追加しないと popup picker を使えなくなる。リリースノートで明示する必要がある
- dotfiles の `dispatch_launcher.fish` が `tmux-sidebar dispatch` の thin wrapper に置き換わる長期移行は変わらず別タスク

---

## agent-pane-state の書き出しを `tmux-sidebar hook` サブコマンドに集約（2026-05-03）

### それまでの方針

agent (Claude Code / Codex CLI) が状態ファイルを書き出す経路は、setup.md と doctor の auto-fix の両方で **inline shell snippet を `.claude/settings.json` に埋める** ことで提供していた。具体的には `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=/tmp/agent-pane-state; mkdir -p "$dir"; printf 'running\nclaude\n' > "$dir/pane_${num}"; date +%s > ...; if [ -n "$CLAUDE_SESSION_ID" ]; then ...; fi` のような行を `command` フィールドに直書きしていた。

### 反転の動機

レビューで「agent-pane-state writer の責務を呼び出し側 (=ユーザの settings.json + doctor) に押し付けている」「呼び出し側の責務は hook 設定で済むようにすべき」と指摘を受けた。具体的に問題視された点:

1. **仕様の二重持ち**: `pane_N` の 2 行構造、sidecar files (`pane_N_started` / `pane_N_path` / `pane_N_session_id`) の意味は `internal/state.FSReader` が source of truth だが、書き出し側は doc / doctor 内の shell 文字列が独立に同じ仕様を再実装していた。reader を変更しても writer が追従せず silently 壊れる経路ができていた
2. **Claude Code hook 入力規約の取りこぼし**: Claude Code は hook に stdin JSON で `session_id` / `cwd` を渡す規約だが、shell snippet は `$CLAUDE_SESSION_ID` env var を読んでいた。これは規約と一致せず、`session_id` が取れていないケースが想定された
3. **他の subcommand との不整合**: `dispatch` / `doctor` / `restart` 等は既にバイナリに統合されているのに、hook handler だけ doc の shell snippet なのは設計上ちぐはぐ
4. **Codex CLI への流用が雑**: setup.md は「Codex も同じスクリプトを呼ぶか kind だけ書き換えた専用版を呼び出す」と曖昧な指示で、ユーザ側で AGENT_KIND env var を扱う必要があった

### 採用した方式

- `internal/hook` パッケージを新設し、状態ファイル書き出しロジックを `Write(Options{...})` として実装
  - stdin を best-effort で JSON パースして `session_id` / `cwd` を抽出（パース失敗・空 stdin は無視して env var / `os.Getwd()` にフォールバック）
  - `pane_N_path` は最初の running 遷移時のみ書く write-once セマンティクス
  - 1 MiB の `io.LimitReader` で病的 payload を遮断
- `tmux-sidebar hook <status> [--kind claude|codex]` を main.go に追加
  - `<status>` は `running` / `idle` / `permission` / `ask`
  - `--kind` 既定値は `claude`、Codex 用は `--kind codex` を明示
- `internal/doctor` の `stateRunningCmd()` / `stateIdleCmd()` を `tmux-sidebar hook running` / `tmux-sidebar hook idle` に変更
- 旧 inline shell snippet を識別する `inlineShellHookSig` ヘルパを追加し、`upsertHookGroup` の purge 対象に追加。`checkClaudeSettings` の diagnosis でも「inline shell snippet → upgrade to subcommand」と表示
- setup.md §8 を rewrite。shell script のサンプルは削除し、settings.json 例 1 つに圧縮

### 採用しなかった代替

- **inline shell snippet を維持しつつ stdin JSON を `jq` で抜くだけ修正**: `jq` 依存が増える + 仕様が doc / doctor の 2 箇所に独立して残る問題が解決しない
- **subcommand 化はしつつ `command` フィールドは shell 経由で組み立てる**: hook configuration を「1 行の subcommand 呼び出し」に保ちたいので shell 経由は冗長。`tmux-sidebar hook running` 一発で済む方が doc も短い
- **stdin JSON 必須にする**: Codex CLI が将来同じ JSON 規約を採るとは限らず、stdin が空でも動く best-effort のほうが robust
- **`--kind` を env var (`AGENT_KIND`) で受ける**: 引数として明示する方が settings.json の読みやすさが上がる。env var は隠れた依存を増やす

### 追補: Codex CLI も同じ subcommand で扱えると判明した（2026-05-03）

レビューで「§8 を推奨化 + Claude / Codex 個別の設定例 + doctor チェックを追加すべき」と指摘を受け、Codex CLI 側の hook 規約を確認したところ、Codex も Claude Code と **ほぼ同型** だと分かった:

- 同じ event 名（`PreToolUse` / `PostToolUse` / `Stop`、ほか `SessionStart` / `PermissionRequest` / `UserPromptSubmit`）
- 同じ JSON shape（`{"hooks": {<event>: [{"matcher": ..., "hooks": [{"type": "command", "command": "..."}]}]}}`）
- 同じ stdin payload（`session_id` / `transcript_path` / `cwd` / `hook_event_name` / `model` を共通フィールドとして含む）
- 設定ファイルパスのみ差異（`~/.codex/hooks.json` または `~/.codex/config.toml` の inline `[hooks]`）

そのため `tmux-sidebar hook` サブコマンド本体は **再利用そのまま**、`--kind codex` を付ける運用ガイドを setup.md に追加し、doctor を path/required-command 別の `agentTarget` にリファクタして両 backend を並列にチェックするように変更した。

採用しなかった代替:
- **Codex 側の hook 機構を未確認のまま「将来対応」のスタブだけ書く**: 想像で書いた設定例は誤情報になりやすい。Codex 公式 docs で event 命名と JSON shape が一致することを確認したうえで具体例を載せた
- **Codex 用に専用 subcommand (`hook-codex`) を作る**: 仕様が同型なので分ける利得がない。`--kind` flag で十分
- **`~/.codex/config.toml` の inline `[hooks]` も doctor で扱う**: TOML パースを doctor に持ち込むコストに見合わない。user-level JSON (`~/.codex/hooks.json`) のみ管理。TOML に書いている既存ユーザは setup.md の例を参照して JSON 側に移行してもらう

doctor の追加検査:
- 設定ファイル不在 / event 不足 → WARN + auto-fix 対象
- 旧 inline shell snippet → WARN + 置換
- legacy state dir (`/tmp/claude-pane-state`) 参照 → WARN + 置換
- subcommand kind 不一致（Codex の settings に `tmux-sidebar hook running` のみで `--kind codex` が無いケース）→ WARN + 置換

### 影響範囲

- 新規: `internal/hook/hook.go` + `internal/hook/hook_test.go`
- 修正: `main.go`（`hook` subcommand とヘルプ）、`internal/doctor/doctor.go`（`stateRunningCmd` / `stateIdleCmd`、`upsertHookGroup` の purge 条件、`checkClaudeSettings` の diagnosis）、`internal/doctor/doctor_test.go`（subcommand 形式に書き換え）
- ドキュメント: `docs/setup.md` §8 全面書き換え、`docs/design.md` の責務一覧と subcommand 表に追記、`docs/history.md` に当エントリ

### 残課題

- 既存ユーザの settings.json に inline shell snippet が残っている場合、`tmux-sidebar doctor --yes` を 1 回走らせて subcommand 形式に置き換える必要がある。リリースノートで案内する
- Codex CLI が将来 hook 機構を提供したとき、stdin JSON のシェイプが Claude Code と異なる場合は `internal/hook.readPayload` を agent kind で分岐する必要が出る可能性がある。現時点では Codex 側の hook protocol が未確立のため、stdin が来なければ env / `os.Getwd()` にフォールバックする現挙動で十分

---

## サーバ境界制御 / MRU 自動ソートの取り下げ（2026-05-08）

control surface 拡張の初版検討で「採用しない・延期する項目」として TODO.md 末尾の表に列挙していたうち、history.md の他セクションでまだ rationale を残していなかった 2 項目を、TODO.md 廃止に合わせてここに転記する。背景は §105「read-only navigation から control surface への scope 拡張」を参照。

### server 境界制御（attach / detach / new-server）の取り下げ

- attach / detach / new-server は **sidebar が起動する前の世界** で、cross-context navigation の対象外。tmw / 起動 profile / dotfiles 側の責務として明確に分離する
- sidebar pane は server attach 済みの状態を前提に動作するため、その前段の制御を sidebar UI に持ち込んでも常駐の利得がない
- 同種の「sidebar の手前の世界」（ghq repo 探索 → 新規 session 起動）は popup picker (`tmux-sidebar new`) として独立エントリポイントに切り出している。server 境界もそれと同じく外部ツール側の責務に留める

### session 並びの MRU 自動ソート（保留扱い）

- カーソル追従ロジック（select-window のたびに sidebar が SIGUSR1 で再描画してカーソルを当てる）と相性が悪い。「いま見ている session が突然動く」UX 混乱が出る
- 「直近触った session」を上部に持ってくる需要は pinned_sessions で吸収できる範囲が大きい。MRU 自動化が拾えるのは pin していない session 群の中での優先度だが、ここは元々「列挙順 + `j` / `k` 連打」で足りている
- 実装は「session ごとの last-attached time を state file から取り、unpinned 群を再ソート」だが、確信を持って入れられる UX 設計に至っていないため**却下ではなく保留**。需要が顕在化した時点で再検討する

---

## TODO.md の廃止（2026-05-08）

`docs/TODO.md` を削除し、内容を以下に分配した。

- Phase 1〜5 の完了チェックリスト: 削除（git log と spec.md / design.md で結果は追える）
- 「採用しない・延期する項目」表: 大半は history.md の既存セクション（§105 / §147 / §171 / §193 / §215 / §238 / §280）で rationale を保持済み。未カバーの `server 境界制御` と `MRU 自動ソート` は本ファイルの直前セクションに追記
- 「実装順序の根拠」ブロック: §238「multi-select / vim 風ジャンプの取り下げ」と §280「Phase 5 全削除」の再採番表で実質カバー済みのため削除

### 動機

- TODO.md は当初 control surface 拡張の進行管理として作成したが、Phase 1〜5 が全て `[x]` 完了済みになり、実装トラッキングとしての役割を終えていた
- 「採用しない・延期する項目」表は実質的に rationale の墓場であり、本来 history.md（append-only な経緯）に置くべき性質。CLAUDE.md の 3 本柱（spec / design / history）にも TODO.md は含まれておらず、運用ルール上も孤立していた
- 前向きな実装計画は今後 `issues/` ディレクトリ（per-issue markdown、SEQUENCE 採番）で扱うため、global な TODO.md を維持する意味がなくなった

### 採用しなかった代替

- **TODO.md を archive として残す**: 中身が完了済みチェックボックスと history と重複する rationale だけになっており、リンク切れと混乱の温床になる。完全に消すほうが誠実
- **完了チェックリストを history.md に転記**: 「何をやったか」は git log + 現在の spec / design がスナップショットとして示しており、history.md は「方針反転や却下した代替」を残す場であって完了タスクの墓場ではない
- **TODO.md を per-phase に分割して残す**: Phase 単位の生きた issue が今後発生するなら `issues/` で扱う方針と整合しない

### 影響範囲

- 削除: `docs/TODO.md`
- 修正: `README.md` の TODO.md リンク行を削除
- 残置: history.md 既存エントリ（"TODO.md: ..." の記述）は append-only ルールに従い書き換えない（過去時点での編集箇所として正しい）

### 残課題

なし。3 本柱（spec / design / history）+ issues/ の体制に収束した。

### 補遺: 保留扱い項目を pending issue に登録（2026-05-08）

TODO.md 末尾の「採用しない・延期する項目」表のうち、純粋な却下ではなく**保留**として扱う 2 件は history.md だけに残すと前向きな未着手案件として discoverable でないため、`issues/pending/` に issue として登録した:

- `issues/pending/0005-feat-mru-session-sort.md` — session 並びの MRU 自動ソート
- `issues/pending/0006-feat-undo-close.md` — window / session の undo close

判定基準: 「却下 rationale」は history、「外部依存 / 設計判断待ちで凍結中」は pending issue。`gg` / `G` のような "実需が出れば追加" 系は demand-driven なので history のみに残す（pending issue にすると forward-looking 案件として見えるが実体は rejection）。

---

## Claude Code Agent View 登場時の方針判断（2026-05-13）

Claude Code v2.1.139 で **Agent View** (`claude agents`) が導入された。これは tmux に紐付かない supervisor process 経由のバックグラウンドセッションを 1 画面で監視・ディスパッチする UI で、tmux-sidebar と機能が部分的に重なる:

- バックグラウンドセッションを一覧表示する viewer
- prompt 入力でのディスパッチ
- セッション状態 (working / needs input / idle / completed / failed / stopped) の可視化
- worktree の自動分離 (`.claude/worktrees/<auto>`)
- セッション命名の auto-naming (Haiku クラスモデル)

これを機に本ツールのどこを置き換える / 廃止するかを検討した結果、**リサーチプレビュー期間中は経過観察に徹し、現在の仕組みを維持し、最低限の衝突回避 (spec.md への分担明文化) のみを行う** に決めた。

### 検討候補と判定

| 候補 | 判定 | 受け皿 |
|---|---|---|
| `internal/dispatch/branch.go` の `ClaudeNamer` (`claude -p`) を Haiku 直叩き / Agent View 命名に寄せる | pending | `issues/pending/0007-design-rethink-claude-p-branch-naming.md` |
| `internal/dispatch` の Claude launcher を `claude --bg` (supervisor model) に置き換え | pending | `issues/pending/0008-design-claude-launcher-vs-bg-supervisor.md` |
| Claude 状態取得を hook ベース (`/tmp/agent-pane-state`) から `~/.claude/jobs/<id>/state.json` に寄せる | pending | `issues/pending/0009-design-claude-state-from-supervisor-jobs.md` |
| popup picker と `claude agents` の役割整理 (案 D 採用、spec への明文化) | 実施 | `issues/closed/0010-design-popup-picker-vs-agent-view-dispatch.md` + `docs/spec.md` の「Agent View との分担」節 |

### 採用しなかった代替

- **Claude launcher の全面廃止 / Agent View 一本化**: ghq の `<host>/<owner>/<repo>` 3 階層 namespace を Agent View が解けない (`@<repo>` は basename 解決のみ)、Codex CLI が supervisor model を持たない、`disableAgentView` を強制する組織で fall back 経路が必要、リサーチプレビューに依存することになる、の 4 点で却下
- **`claude --bg` の wrapper として popup picker を残す案 (#0010 案 C)**: `@<repo>` の階層パス対応がない見込みで wrapper として破綻、`claude --bg` の CLI スキーマが安定 API として保証されていない、で却下
- **ghq そのものを捨てて Agent View 中心の repo レイアウトに移行**: repo 物理配置の一斉移行はリスクが高く、ghq の生態系 (`ghq get`、`peco`/`fzf` 統合、フォーク運用) を同時に手放すことになる、で却下

### 衝突しないことを確認した点

`tmux-sidebar` と Agent View は以下のリソースで衝突しない:

| リソース | tmux-sidebar | Agent View |
|---|---|---|
| 状態ファイル | `/tmp/agent-pane-state/pane_N` | `~/.claude/jobs/<id>/state.json`, `~/.claude/daemon/roster.json` |
| Worktree | `<main>@<branch-dirname>` (兄弟ディレクトリ) | `.claude/worktrees/<auto>` (project-local) |
| Transcript 読み取り | `~/.claude/projects/` (read-only) | 同上 (read-only) |
| settings.json hook | `tmux-sidebar hook` を追記 | Agent View 自体は触らない (supervisor が state.json を直接書く) |
| Claude プロセス本体 | tmux pane 内 | supervisor 親 |

同じ Claude セッションが両方の view に同時に現れることもない (tmux pane 内起動 vs `claude --bg` 起動は排他)。

### 再評価トリガ

将来 pending を解除して再評価する条件を記録する:

- Agent View が research preview から GA になる (公式 changelog / docs での明示)
- `~/.claude/jobs/<id>/state.json` と `daemon/roster.json` のスキーマが public stable と明記される
- `@<repo>` の解決が `<host>/<owner>/<repo>` のような階層パスに対応する
- `claude --bg` の標準出力スキーマと `--permission-mode bypassPermissions` / `auto` の非対話受け渡しが解禁される
- `disableAgentView` を強制する組織でも壊れない fall back 経路が公式に用意される
- ユーザから「sidebar に `claude --bg` 由来のセッションも出してほしい」「Claude 命名と sidebar の branch 名を揃えてほしい」等の具体的要望が出る
