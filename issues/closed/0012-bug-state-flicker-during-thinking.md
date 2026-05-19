# PostToolUse hook が turn 中の thinking 期間に idle を書き出し state badge が flicker する

Created: 2026-05-19
Completed: 2026-05-19
Model: Opus 4.7

## 概要

issue #0011 の回避策として Claude Code を 2.1.138 に downgrade した結果、サイドバーの state badge が turn の途中で次のような不安定な遷移を見せるようになった:

- `🔄` (running) が turn 中に何度も消えて非表示 (idle) に切り替わる
- 復帰時に経過時間 (`🔄Ns`) が 0 秒から数え直しになる
- 結果として「running 中なのに wait のように見える」「アイコンが消える」とユーザに認識される

これは 2.1.138 の regression ではなく、`~/.claude/settings.json` の `PostToolUse → tmux-sidebar hook idle` 配線と Claude の thinking フェーズが噛み合っていない設計上の問題で、本来は以前のバージョンでも潜在していた（新版では PostToolUse の発火タイミングが微妙に違っていたため目立たなかった可能性が高い）。

## 根拠

### hook 配線（issue 発生時点の `~/.claude/settings.json`）

```
PreToolUse  (matcher: "")  → tmux-sidebar hook running   # 各 tool 呼び出し直前に発火
PostToolUse (matcher なし) → tmux-sidebar hook idle      # 各 tool 終了直後に発火
Stop                       → tmux-sidebar hook idle      # turn 終了時に発火
```

### 1 turn 中の writer 挙動

```
T0  ユーザ submit
T1  Claude thinking ─ 何も発火しない（前回の idle が残る）
T2  Tool 1 起動: PreToolUse  → running, started=T2     🔄1s
T3  Tool 1 終了: PostToolUse → idle                    badge 消失
T4  Claude thinking ─ idle のまま                       badge なし
T5  Tool 2 起動: PreToolUse  → running, started=T5     🔄1s (経過時間がリセット)
T6  Tool 2 終了: PostToolUse → idle                    badge 消失
…
TN  Stop → idle
```

問題点:

1. **badge flicker**: PostToolUse → idle と次の PreToolUse → running の間（= thinking フェーズ）に毎回 badge が消える。spec.md §State / Activity badges で idle は意図的に非表示なので、`🔄` → 消失 → `🔄` を繰り返す
2. **elapsed リセット**: `internal/hook/hook.go:107-135` が `running` 遷移時に `pane_N_started` を毎回上書きするので、turn 全体の経過時間ではなく直近 tool の経過時間が表示される

### 検証

- `pane_N` が `running\nclaude\n` と `idle\nclaude\n` を交互に書き換わるのを `/tmp/agent-pane-state/` で観測
- `pane_N_started` が PreToolUse の発火ごとに新しい epoch に上書きされるのも観測

## 対応方針

state model は spec.md L181-186 の 4 値（running / idle / permission / ask）を維持。turn 境界として信頼できるのは `Stop` だけなので writer の発火点を `Stop` に集約する:

| 案 | 内容 | 採否 |
|---|---|---|
| A. PostToolUse hook を retire し PreToolUse + Stop の 2 点に | turn 全体が running 表示になる。Stop を待つので thinking フェーズも running 扱いだが、ユーザの実体感（「Claude が動いている」）と一致 | **採用** |
| B. UserPromptSubmit + Stop の 2 点 | PreToolUse も外す方向。tool 実行と pure thinking を区別できないが、それは元々 spec が区別していない | 却下: tool ベースの粒度を失うのは過剰。PreToolUse は「実際に tool が動いている」根拠として価値がある |
| C. writer 側で idle 書き込みを debounce / sticky 化 | reader/writer 間に暗黙の時間状態が増えて debug しづらい | 却下 |
| D. spec を後退させて flicker を「正しい挙動」に位置付ける | sidebar の機能差別化が失われる | 却下 |

採用案 A に加えて、PreToolUse が turn 中に複数回発火する事実は変わらないので、writer 側でも `running → running` 遷移時に `pane_N_started` を維持する sticky 化を入れる（PostToolUse=idle が万一残っていても elapsed リセットだけは抑える保険）。

## 変更箇所

| パッケージ / ファイル | 変更内容 |
|---|---|
| `internal/hook/hook.go` | sticky started: 既存の pane_N の status を読んで running → running なら epoch を上書きしない |
| `internal/hook/hook_test.go` | `TestWriteRunningStickyStarted` / `TestWriteRunningRestampsAfterIdle` を追加 |
| `internal/doctor/doctor.go` | `requiredClaudeHooks` / `requiredCodexHooks` から PostToolUse を削除、`staleEvents` 配列と `isTmuxSidebarHookCmd` / `purgeStaleHooks` を追加、`hookFix.purge` フラグで `applySettingsFixes` を分岐 |
| `internal/doctor/doctor_test.go` | 必須 hook 数 / kind mismatch テストを 2 つに、`TestCheckAgentSettings_PurgesStalePostToolUse` を追加 |
| `docs/setup.md` §8 | 例から PostToolUse を削除、retire 理由のコラム、doctor チェック一覧に「廃止 hook 検出」を追加 |
| `docs/history.md` | append-only エントリ「PostToolUse hook を retire し turn 境界を Stop に集約」 |
| `~/.claude/settings.json` (dotfiles) | `PostToolUse → tmux-sidebar hook idle` を削除、Skill matcher の skill-call-counter.sh はそのまま |

## 解決方法

1. writer ロジックを fix: `internal/hook/hook.go` で `pane_N` 既存内容を読み、`running → running` 遷移時は `pane_N_started` を維持。`idle → running` の新規 turn 境界でのみ epoch を更新
2. PostToolUse の hook event を retire: `internal/doctor` の `requiredClaudeHooks` / `requiredCodexHooks` から削除し、`staleEvents` で既存設定を検出・自動 purge できるよう拡張。`hookFix.purge` フラグで apply 時に削除動作に分岐
3. setup.md §8 の例を 2 hook 構成（PreToolUse + Stop）に更新し、PostToolUse を hook しない理由を明示
4. history.md に方向反転（PostToolUse を turn 境界とする → Stop に集約する）を append-only で記録
5. dotfiles の `~/.claude/settings.json` から `PostToolUse → tmux-sidebar hook idle` を直接削除（同じ event 内の Skill matcher エントリは保持）

`tmux-sidebar doctor --yes` を走らせれば既存ユーザも自動 upgrade される。

残課題:

- Stop hook が発火しないケース（crash, kill -9, terminal closed 等）で状態が running のまま固定される。元々 PostToolUse=idle 時代も「直近 tool の epoch が新しい running」表示で誤認しうる性質はあったが、現在は turn 全体が running になるので影響度は若干大きい。10 分以上の stale running を別バッジ（`⚠` 等）に降格する fallback は今後の検討（必要なら別 issue を立てる）
- 同じ flicker が Codex CLI にあるかは未検証
