# state ファイル堆積で reload が重くなり kill 反応がラグる

Created: 2026-06-09
Model: Opus 4.7

## 概要

`/tmp/agent-pane-state/` 配下の `pane_N` / `pane_N_started` / `pane_N_path` / `pane_N_session_id` が、対象 pane の死後もクリーンアップされず堆積する。実環境で観測した時点では **live pane 14 に対し state ファイル 287 件**、graveyard 404 件。

`internal/state/state.go:70-196` の `FSReader.Read()` は `os.ReadDir` 後に `pane_*` 全件に対して `os.ReadFile` を直列に発行する仕様で、コストが **生 pane 数ではなく dir のエントリ総数** にスケールする。reload 経路（`loadData()` / `loadStateOnly()` / fsnotify / 10秒tick）はすべてこれを通るため、堆積に応じてラグが線形に悪化する。

ユーザ視点では「数時間前まで `d` / `D` で軽快に消せていたのに、コードを変えていないのに突然 kill の反応が重くなった」として顕在化した。

## 根拠

### state ファイルが減らない構造

- 書き出しは `internal/hook/hook.go` 経由（agent hook で `tmux-sidebar hook <status>`）。書き込み専用で削除契機を持たない。
- pane 自身が死ぬと、そこから state 削除を発行するのは構造的に不可能（hook は死にゆく pane の中で動く）。
- macOS の `/tmp` periodic cleanup は「3 日アイドル」基準なので、活発に動く環境では掃除されない。
- 結果として「過去に存在したことのある pane 全部」が dir に積み重なる。

### reload 経路がすべて O(dir 内ファイル総数) で走る

`internal/state/state.go:70-196`:

```go
entries, err := os.ReadDir(r.dir)         // ← N 件
for _, entry := range entries {
    ...
    data, err := os.ReadFile(filepath.Join(r.dir, name))   // ← per-file syscall
    ...
}
```

呼び出し経路:

- `internal/ui/model.go:314` `loadData()` — kill 直後 / SIGUSR1 / 10 秒 tick / 起動時
- `internal/ui/model.go:456` `loadStateOnly()` — fsnotify (`StateChangedMsg`) / 1分tick (`minuteTickMsg`)

特に fsnotify は **debounce 無しで 1 event = 1 `loadStateOnly`** （`main.go:232-247`）。agent hook 連発時、StateChangedMsg がキューに積まれ、`killResultMsg → loadData` の `dataMsg` がその後ろに並ぶ。これが「kill 押してから対象行が消えるまでの遅延」の体感原因。

### graveyard も累積するが本件の主因ではない

`~/.local/share/tmux-sidebar/graveyard/` は kill 時に capture を保存する write-only ストア。`captureToGraveyard` は `os.MkdirAll` + `os.WriteFile` のみで、エントリ数に O(N) の処理は無い。本 issue では対象外（将来別 issue で TTL 削除を検討する余地はある）。

## 対応方針

### 採用: sidebar 側の reload で stale GC を相乗りさせる（案 C）

`loadData()` はすでに `tmux.ListAll()` で **生 pane 番号の集合** を持っている。これを `state.FSReader` に渡し、Read 中に live set に含まれない `pane_N*` を `os.Remove` する。

- 追加 syscall は「stale ファイルに対する unlink 1 回」だけ。Read 中の `ReadDir` 結果を再利用するので二重 walk なし。
- 「sidebar が動いている限り絶対に掃除される」というシンプルな保証。tmux hook や agent hook を増やさない。
- `/tmp` の sticky bit によって他ユーザのファイルは `os.Remove` が EACCES で no-op。安全。
- `loadStateOnly()` は live set を持たないので GC しない（fsnotify 連発時に毎回掃除すると逆に I/O 増になる）。loadData の経路（kill / SIGUSR1 / 10秒tick / 起動）で十分掃除される。

### 採用しなかった代替

- **tmux `pane-exited` hook で発火**: dotfiles 依存。`#{hook_pane}` から pane number 抽出が面倒。hook が壊れていると気付けない。
- **agent hook（`Stop`）で自分の state を消す**: クラッシュ / Esc 中断 / 外部 kill で発火しないため、まさに stale 化するケースを取り逃す。
- **起動時の一括掃除のみ**: 長時間起動中に堆積する本件には弱い。
- **graveyard と同時に掃除**: graveyard はサイズ上限 / TTL の別問題。同じ責務に混ぜない。

### 補完: fsnotify debounce

GC で「1 回あたりの Read コスト」を下げる一方、fsnotify 起源の **発火頻度** も下げる。`main.go` の fsnotify ループに 50–100 ms の coalesce を入れて連発を畳む。GC とは独立に効くが、本件のラグ原因の片輪なので同時に直す。

## 変更箇所

| ファイル | 変更内容 |
|---|---|
| `internal/state/state.go` | `Reader` interface に `ReadAndGC(live map[int]struct{})` を追加。`FSReader.ReadAndGC` を実装し、live set に含まれない `pane_N` / `pane_N_started` / `pane_N_path` / `pane_N_session_id` を best-effort で `os.Remove`。`Read()` は無改変。 |
| `internal/state/state_test.go` | ReadAndGC で stale を消すこと / live を残すこと / 全 suffix を網羅すること / live set 未提供の Read は無削除 のテスト追加。 |
| `internal/ui/model.go` | `loadData()` で `allPanes` から live set を作って `ReadAndGC` を呼ぶ。`loadStateOnly()` は `Read()` のまま（debounce 後の頻度低下に任せる）。 |
| `main.go` | fsnotify ループに `time.Timer` ベースの 80 ms debounce を追加。 |
| `docs/design.md` | 「状態ファイル」節に「sidebar reload 時に live set ベースで stale を GC する」と書き加える。 |

## 実装チェックリスト

- [ ] `internal/state.FSReader.ReadAndGC` を実装
- [ ] `internal/state/state_test.go` に GC 回帰テスト
- [ ] `internal/ui/model.go` `loadData()` の Read 呼び出しを ReadAndGC に切替
- [ ] `main.go` fsnotify debounce 追加
- [ ] `docs/design.md` 「状態ファイル」節を更新
- [ ] `go test ./...`
- [ ] `/verify-implementation`

## 解決方法

(close 時に追記)
