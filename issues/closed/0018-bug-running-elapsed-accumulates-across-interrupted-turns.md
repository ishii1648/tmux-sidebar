# running 経過時間が中断ターンを跨いで累積し、巨大な値になる

Created: 2026-06-02
Completed: 2026-06-02
Model: Opus 4.8

## 概要

sidebar の running バッジ（`🔄Nm`）が、実際の作業時間とかけ離れた巨大な値（数時間）を表示することがある。原因は `pane_N_started`（経過時間の起点）が「ターン終了時の `--reset`」でしか消えず、Esc 中断・クラッシュなどで Stop hook が発火しなかったターンでは起点が消えずに残り、次のターンの running がその古い起点を再利用して、間の離席時間ごと累積するため。

ユーザ報告:「running 中に時間が非常に長くなるケースがある。多分一度 idle してから再度 running に戻すときに時間が累計になっている」——仮説は正しい。idle→running の境界で起点が張り直されないのが核心。

## 根拠

### コード上のメカニズム

`pane_N_started` のライフサイクル（`issues/closed/0015`・`0016` で確定した現設計）:

- `internal/hook/hook.go:116-131` — `hook running` は既存の `pane_N_started` があれば prevStatus に関わらず**保持**し、無いときだけ現在時刻で作成する
- `internal/hook/hook.go:150-157` — `pane_N_started` を**削除するのは `--reset` 付き呼び出し（= Stop hook の `hook idle --reset`）だけ**
- それ以外（running / 非 reset の idle / permission / ask）はすべて起点を保持する
- `internal/state/state.go:179` — reader は単純に `now - started` を出すだけ。起点が古ければそのまま巨大値になる

したがって起点の寿命は「最初の running 〜 次の `--reset` 呼び出し」。**2 つの running episode の間に `--reset` が一度も走らなければ、後続 running の elapsed = `now − 最初の running` となり、間の idle / 離席時間ごと累積する。**

### `--reset` が走らない条件（トリガ）

Stop hook 設定（`~/.claude/settings.json`）は `tmux-sidebar hook idle --reset` で正しく入っており、**設定ミスではない**。残る原因は Stop hook が発火しないターン終了:

- **Esc / Ctrl-C による中断** — Claude Code は中断時に Stop hook を発火しない。中断で終えたターンは起点を残したまま放置され、次のプロンプトの最初の `running` が古い起点を再利用する
- セッションのクラッシュ／強制終了でも同様

### 実環境での実証（`/tmp/agent-pane-state/`、2026-06-02 18:44 時点）

`pane_5` がまさにこの状態だった:

| ファイル | mtime | 内容 |
|---|---|---|
| `pane_5`（status） | 18:41 | `running` |
| `pane_5_started` | 14:28 | epoch `1780378133` = 14:28:53 |

elapsed = `now(18:44:43) − started(14:28:53)` ≈ **256 分（4h16m）**。現在 running なのでこの巨大値がバッジに出る。

他にも `pane_24_started`(14:58) / `pane_7_started`(14:15) / `pane_37_started`(18:35) が、対応する status 更新時刻より何時間も古いまま残存しており、起点が消えず居座る現象が複数ペインで再現していた。

### 既存設計との関係

`docs/history.md`（2026-05-31「running 経過時間の起点リセット契機をターン境界に変更」）で、permission / ask 待ちを跨いでも起点を保持するのは**意図された設計**。長時間の承認待ちで経過時間が伸びるのはこの設計どおり。ただし今回の数時間級の値は、それとは別の「中断で起点が消えない leak」が主因。同エントリの「採用しなかった代替」で `UserPromptSubmit` でのリセット案が「doctor が deprecated 扱いしている UserPromptSubmit hook を再導入することになり整合しない」として却下されているが、その却下が今回の leak を生んでいる構図。

## 対応方針

### 案 A（推奨）: `UserPromptSubmit` でターン開始時に起点を張り直す

新しいユーザ入力 = 新ターンの**確実な境界**。前ターンが中断・クラッシュでどう終わっても、次の入力で起点が必ずリセットされるため leak に構造的に強い。

- `UserPromptSubmit` hook で起点を「今」に張り直す。実装は 2 案:
  - **A2（本命）**: `hook running` に「起点を now で上書きする」セマンティクスを追加（例: 既存の `--reset` を running にも効かせて再アンカー、または専用フラグ）。`UserPromptSubmit → tmux-sidebar hook running --reset` で、プロンプト投入直後に起点を張り直し running を書く。最初の tool までの thinking 時間も計測でき、バッジも即 running で flicker しない
  - **A1（簡易）**: `UserPromptSubmit → tmux-sidebar hook idle --reset` で起点を消すだけ。次の `PreToolUse → running` が新しい起点を作る。実装は最小だが、最初の tool までの間バッジが idle になり thinking 時間が計測されない
- Stop の `--reset` は残してよい（ターン境界の二重防御）。が、A 採用後は「前ターンがどう終わっても次ターン頭で張り直る」ため、Stop 依存の脆さが解消される
- **コスト**: `doctor` が `UserPromptSubmit` を legacy 扱いしている（`internal/doctor/doctor.go:668-692` `checkLegacyClaudeHooks`「superseded by PreToolUse — safe to remove」）。これを撤回し、`requiredClaudeHooks` に `UserPromptSubmit` を追加する必要がある。`docs/history.md` で一度却下した案の再採用なので、前提反転を append 必須

### 案 B（補完・即効緩和）: reader 側 stale ガード

`internal/state/state.go` で running の elapsed を出す前に上限・妥当性チェックを入れる。hook を増やさず reader だけで完結し、A の実装前でも巨大値の露出を抑えられる。

- ただし**単一スナップショットでは「4 時間連続の正規ターン」と「4 時間前に開始して中断後に再開した leak」を区別できない**（どちらも `started` が古く `pane_N` mtime が新しい、で同一シグネチャ）。よって閾値ベースのヒューリスティック（例: elapsed が極端に大きければ表示抑制）にならざるを得ず、本質的な解にはならない
- 安全網としての価値はある（正規の連続 >90 分ターンは実運用でほぼ皆無）が、**A の代替ではなく補完**

### 採用しなかった代替

- **案 C: `running` 書き込み時に「前回が終端 idle なら張り直す」**: Stop が発火しない中断ケースには終端 idle マーカーが残らないため検出不能。`PostToolUse → running`（issue 0016）で mid-turn idle も消えており、状態遷移から境界を復元できない
- **案 D: `SessionStart` でリセット**: SessionStart はセッション単位で per-turn ではない。1 セッション内で中断ターンを繰り返すと leak が残るため単独では不十分
- **Stop の信頼性に依存し続ける（現状維持 + ドキュメント注意書き）**: 中断は日常操作であり、ユーザ運用で回避不能。バグとして残す選択肢にならない

### 推奨

**案 A2 を本筋**とする（ターン開始＝起点張り直しが意味的に正しく、中断耐性が構造的に得られる）。即効性が必要なら**案 B を安全網として先行**投入し、A2 で恒久対処する 2 段構えも可。

## 変更箇所

| ファイル | 変更内容（案 A2 採用時） |
|---|---|
| `internal/hook/hook.go` | running 時に `ResetElapsed` で起点を now に上書きするセマンティクスを追加 |
| `main.go` | `hook` サブコマンドのヘルプに UserPromptSubmit 用途を追記（フラグ解析は既存 `--reset` を流用する場合は変更不要） |
| `internal/doctor/doctor.go` | `requiredClaudeHooks` に `UserPromptSubmit` を追加。`checkLegacyClaudeHooks` の legacy 判定を撤回（または UserPromptSubmit の canonical コマンドと衝突しないよう調整） |
| `docs/setup.md` | Claude Code hook 例に `UserPromptSubmit` を追加、`pane_N_started` ライフサイクル節を更新 |
| `docs/design.md` | 起点リセット契機に「ターン開始（UserPromptSubmit）」を追記 |
| `docs/history.md` | 「UserPromptSubmit リセット却下」前提の反転と、中断で Stop が飛ぶ leak の経緯を append |
| `internal/*_test.go` | 中断→新ターンで起点が張り直る回帰テスト |

案 B を併用する場合は `internal/state/state.go` と `internal/state/state_test.go` を追加で変更。

## 実装チェックリスト

- [ ] 方針確定（A2 / A1 / A+B 二段）
- [ ] hook 側で UserPromptSubmit による起点張り直しを実装
- [ ] doctor の UserPromptSubmit legacy 判定を撤回し required に追加（重複・mismatch を出さない）
- [ ] `docs/setup.md` / `docs/design.md` / `docs/history.md` を更新
- [ ] 回帰テスト追加（中断ターン跨ぎ・正規ターン・permission 跨ぎ）
- [ ] `go test ./...`
- [ ] `/verify-implementation` で実 sidebar の表示を確認

## 解決方法

案 A2 を採用。`UserPromptSubmit` でターン開始時に `pane_N_started` を「今」に張り直すことで、前ターンが Stop hook を発火せず終わっても（Esc 中断・クラッシュ）次ターンが古い起点を引き継がないようにした。

- `internal/hook/hook.go`: `ResetElapsed` のセマンティクスを status 依存に拡張。running かつ `--reset` のとき `pane_N_started` を現在時刻で**上書き**（再アンカー）し、非 running かつ `--reset` のときは従来どおり**削除**する。plain running（`--reset` なし）は既存値を保持。
- `main.go`: `hook` サブコマンドのヘルプ・doc コメントを `running --reset`（UserPromptSubmit）/ `idle --reset`（Stop）の二境界に更新（既存の `--reset` フラグ解析を流用するので解析変更は不要）。
- `internal/doctor/doctor.go`:
  - `requiredClaudeHooks` に `UserPromptSubmit = tmux-sidebar hook running --reset` を追加（`stateTurnStartCmd`）。
  - `UserPromptSubmit` を legacy 扱いしていた `checkLegacyClaudeHooks` を撤回・削除（deprecation の反転）。
  - `checkAgentSettings` に「event は存在するが canonical な state-writer コマンドが無い」検出（`canonicalPresent`）を追加。これが無いと、`UserPromptSubmit` に別用途 hook（SSH banner 等）や `PostToolUse` に Skill counter だけがある実環境で doctor が誤って OK と判定し、`doctor --yes` が writer を入れられなかった。
- docs: `setup.md`（Claude hook JSON に UserPromptSubmit 追加・ライフサイクル節更新・doctor 必須 event 一覧）、`design.md`（起点ライフサイクルと CLI 表）、`spec.md`（経過時間がユーザ入力ごとに張り直る保証）、`history.md`（前提反転と却下案を append）。
- テスト: `hook_test.go`（`running --reset` の再アンカー・新規作成）、`doctor_test.go`（UserPromptSubmit 必須化・reset 欠落 upgrade・PreToolUse stray reset・state-writer 欠落検出）、`e2e/hook_state_test.go`（中断後の UserPromptSubmit 再アンカーを実 tmux で検証）。

検証として `go test ./...`（全 package OK）、`go test -tags e2e`（`TestHookUserPromptSubmitReanchorsAfterInterrupt` 含め PASS）、`/verify-implementation`（サイドバー表示・doctor self-check）を実施。doctor self-check で実環境の `UserPromptSubmit` / `PostToolUse` が正しく WARN 表示され、`doctor --yes` で既存 hook を残しつつ writer を追加できる状態を確認した。

スコープは Claude のみ（Codex は issue 0013 の方針を継続して据え置き。`hook` 本体は kind 非依存なので手動設定で同等の恩恵は得られる）。
