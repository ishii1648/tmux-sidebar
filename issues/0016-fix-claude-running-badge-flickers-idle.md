# Claude の running バッジが tool 間で idle に戻り、permission 状態も張り付く

Created: 2026-05-31
Model: Opus 4.8

## 概要

Claude session を sidebar で監視すると、ユーザから見て連続作業中の 1 ターンでも、tool と tool の間（Claude の応答生成フェーズ）でバッジが `running` から `idle` に戻る。issue 0015 で経過時間のリセットは直したが、status バッジ自体の flicker は残っていた。

## 根拠

Claude の推奨 hook は `PreToolUse → running` / `PostToolUse → idle` / `Stop → idle --reset`。1 ターンの実遷移は次の通りで、tool 完了のたびに idle に戻る。

```
PreToolUse  → running  (🔄)
PostToolUse → idle      ← tool 完了。生成中ずっと idle
PreToolUse  → running  (🔄)  次の tool
```

これは見た目だけでなく `docs/spec.md` の destructive 操作（`d`/`D`）の confirm 強度にも影響する。tool 間の idle 瞬間に kill すると「running 中なのに単純確認」になり、spec の意図（running の kill は強い confirm）に反する。

さらに permission の扱いと絡む。Claude の permission は `Notification` hook（matcher `permission_prompt`）で `💬` を書く設計（issue 0011, spec.md:182-186）。実シーケンスは

```
PreToolUse → running → Notification(permission) → 💬 → 承認 → tool → PostToolUse → idle (💬 クリア)
```

で、`PostToolUse → idle` は「tool 完了時に running / permission 状態をクリアするアンカー」も兼ねている。単純に `PostToolUse` を削除すると（Codex と同型にする案）、permission(`💬`) を書いた後にクリアする中間経路が失われ、次の `PreToolUse(running)` か `Stop(idle)` まで `💬` が張り付く（承認 tool がターン最後だと生成中ずっと `💬`）。

## 対応方針

`PostToolUse` を **`idle` ではなく `running`** に変更する（採用案）。

- tool 間: `PreToolUse(running)` → tool → `PostToolUse(running)` → 生成 → 次 `PreToolUse(running)`。ずっと running で flicker 無し
- permission: 承認後 tool 実行 → `PostToolUse(running)` で `💬` をクリアして running に戻る（張り付かない）
- idle は `Stop(idle --reset)` でのみ。「ターン中 = running、待機 = idle」という正確なモデルになり、0015 で入れた started 保持 / `--reset` とそのまま両立する

スコープは Claude のみ。Codex は `PermissionRequest` で permission を出せており、`PostToolUse` 削除済みの issue 0013 方針を尊重して今回は変更しない。

### 採用しなかった代替

- **`PostToolUse` を削除（Codex と同型）**: flicker は解消するが、permission(`💬`) を tool 完了時にクリアする経路が失われ、次の `PreToolUse` / `Stop` まで張り付く。permission の正確さを優先して却下。
- **現状維持（idle のまま）**: flicker と confirm 強度の問題が残るため却下。

## 変更箇所

| ファイル | 変更内容 |
|---|---|
| `internal/doctor/doctor.go` | Claude の PostToolUse 必須 hook を `tmux-sidebar hook running` に変更。`upsertHookGroup` の purge を「canonical 以外の `tmux-sidebar hook` コマンドを全除去」に強化し、idle→running の status 跨ぎ移行でも重複させない |
| `docs/setup.md` | Claude の PostToolUse 推奨設定を `running` に更新 |
| `docs/spec.md` | tool 間も running 表示が継続し idle は Stop のみ、を明記 |
| `docs/design.md` | hook event と状態遷移の対応を更新 |
| `docs/history.md` | 0015 の option 2 から option 3 への再変更（review-loop での permission 欠陥発見）を記録 |
| `internal/doctor/*_test.go` | PostToolUse=running の回帰テスト |

## 実装チェックリスト

- [ ] Claude PostToolUse 必須 hook を `running` に変更
- [ ] `upsertHookGroup` の purge 強化（status 跨ぎ移行で重複させない）
- [ ] `docs/setup.md` / `docs/spec.md` / `docs/design.md` / `docs/history.md` を更新
- [ ] `go test ./...` を実行
- [ ] `/verify-implementation` で確認
