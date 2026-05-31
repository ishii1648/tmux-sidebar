# PR status が削除済み worktree の window で stale なまま無期限に残る

Created: 2026-05-31
Model: Opus 4.8

## 概要

PR をマージした後も、サイドバーの PR バッジがマージ前の色（`draft`=灰色 / `open`=緑）のまま 5 分以上、実質無期限に残ることがある。`gh pr view` 自体はマージ済みブランチでも `MERGED` を正しく返すため、原因はサイドバー側の `gitData` キャッシュにある。

## 根拠

実機の tmux window を `fetchGitInfo`（`internal/ui/model.go:490`）と同じ手順でなぞると、次の window が存在した。

```
@116 active_pane_path = /Users/.../agent-telemetry@fix-flush-ensure-pr-metrics-view
     dir exists: NO
     git -C <path> rev-parse → fatal: cannot change to '...': No such file or directory
```

pane のプロセス（claude）は生きている（`pane_dead=0`）ため tmux の `pane_current_path` は **削除済み worktree のパスを指したまま**残り、サイドバーには window も表示され続ける。だが該当ディレクトリは既に無いので git/gh は解決できない。

この repo は ghq + worktree を多用し **マージ後に worktree を削除**する運用のため、この状態が頻発する。

stale 化のメカニズムは次の 3 点の合わせ技:

1. `fetchGitInfo` は path 不在 / 非 git のとき空の `gitInfo{}` を返す（`model.go:497-499`, `502-505`）
2. `loadGitInfo` は `info.branch != "" || info.prNumber != 0` のときだけ `data` に採用する。空データは採用されない（`model.go:476`）
3. `gitDataMsg` 受信は **merge only でクリーンアップが無い**（`model.go:712-714`）。filtered-out window のデータを守るため意図的に merge にしている

→ 一度記録された PR 状態は、その後 path が解決できなくなっても **上書きも削除もされず無期限に残る**。5 分 TTL は「毎回 path/branch を解決できる window」にしか効かず、削除済み worktree を指す window は TTL の土俵に乗らない。

## 対応方針

`loadGitInfo` で「visible かつ fetch 対象だったのに path 解決に失敗した（空 `gitInfo`）」window ID を stale として収集し、`gitDataMsg` で受信側に渡して `gitData` から削除する。

- filtered-out（そもそも fetch されない）window のデータは従来どおり保持される（merge を維持）
- 削除されるのは「現在 visible で fetch を試みたが path を解決できなかった」window のみ → 削除済み worktree の stale バッジが消える

### 採用しなかった代替

- **`gitDataMsg` を全置換に戻す**: filtered-out window の PR データを毎回捨ててしまう（`model.go:710-711` のコメントが merge にした理由）。却下。
- **TTL を短縮**: path が解決できない window には TTL 経路自体が効かないため無効。却下。

## 変更箇所

| ファイル | 変更内容 |
|---|---|
| `internal/ui/model.go` | `gitDataMsg` に `stale []string` を追加。`loadGitInfo` で空 fetch の window ID を収集。`gitDataMsg` ハンドラで `stale` のキーを `delete` |
| `internal/ui/model_test.go` | `gitDataMsg` の stale 削除と filtered-out 保持の回帰テスト |
| `docs/design.md` | 既知の制約 or gitData キャッシュの記述に「path 解決不能 window はバッジを掃除する」を反映 |

## 実装チェックリスト

- [ ] `gitDataMsg` に `stale []string` を追加
- [ ] `loadGitInfo` で空 fetch の window ID を `stale` に収集
- [ ] `gitDataMsg` ハンドラで `stale` のキーを削除
- [ ] 回帰テスト追加（stale 削除 / filtered-out 保持）
- [ ] `go test ./...`
- [ ] `/verify-implementation`
