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
