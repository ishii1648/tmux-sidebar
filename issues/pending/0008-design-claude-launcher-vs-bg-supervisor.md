# `tmux-sidebar dispatch --launcher claude` と `claude --bg` (supervisor model) の関係整理

Created: 2026-05-13
Model: Opus 4.7

## 概要

Claude Code v2.1.139+ で **エージェントビュー** が追加され、`claude --bg` / `/bg` / `claude agents` 経由でバックグラウンドセッションを起動できるようになった。バックグラウンドセッションは **supervisor process** が親で、ターミナル (tmux pane) に縛られずに走り続ける。本リポジトリの dispatch engine (`internal/dispatch`) は逆に「tmux session を作って、その pane の中で `claude` プロセスを起動する」モデルなので、Claude 側の supervisor model と前提が食い違っている。Claude launcher 経路をそのまま残すか、`claude --bg` ベースに置き換えるか、共存させるかを判断する。

## 根拠

- `internal/dispatch/dispatch.go` の `Launch` は `tmux new-session ... claude` 相当を組み立て、pane が死ねば claude プロセスも死ぬ。
- 公式 (https://code.claude.com/docs/ja/agent-view) 抜粋:
  - 「バックグラウンドセッションは作業を続けるためにターミナルを開く必要がありません。別のスーパーバイザープロセスがセッションを実行するため、エージェントビューを閉じたり、シェルを閉じたり、新しいインタラクティブセッションを開始したりしても、ディスパッチされた作業は続きます。」
  - 「エージェントビュー、`/bg`、または `claude --bg` から開始されたすべてのバックグラウンドセッションは、作業ディレクトリで開始されますが、そこにファイルを書き込むことがブロックされています。セッションがファイルを編集する必要がある場合、Claude は自動的にセッションを `.claude/worktrees/` の下の分離された [git worktree](/ja/worktrees) に移動するため、並列セッションは同じチェックアウトを読み取ることができますが、それぞれが独自のものに書き込みます。」
  - 「シェルからモードを設定するには、`claude --bg` で `--permission-mode` を渡します。」
- dispatch engine が自前でやっている要素のうち、Claude 経路の以下は公式機能で代替可能:
  - **git worktree 作成** ← `claude --bg` の auto-worktree (`.claude/worktrees/`)
  - **session 命名** ← Agent View の auto-naming
  - **prompt の background dispatch** ← `claude --bg "<prompt>"`
  - **後付けで attach** ← `claude attach <id>`
  - **永続化 / respawn** ← supervisor 経由 (`claude respawn`)
- 一方、tmux-sidebar が独自に持つ価値:
  - **tmux session として実体化** することで、`prefix s` などの tmux native navigation の対象になる
  - sidebar pane で window 単位の表示・lifecycle 操作 (`d` / `D` / pin) が効く
  - **Codex CLI** はこの supervisor model を持たないので、tmux session ベースのままにする必要がある
- 結果として、「Claude launcher だけ supervisor 側に寄せて、Codex は従来どおり tmux session 起動」というハイブリッドが現実解になりうる。が、これをやると sidebar に Claude セッションが出てこなくなる (tmux にぶら下がっていないので) — そのため次の issue「Claude セッションの状態取得を `~/.claude/jobs/<id>/state.json` 経由に寄せる」と合わせて検討する必要がある。

## 対応方針

| 案 | 内容 | メリット | デメリット |
|---|---|---|---|
| A. 現状維持 | tmux session 内で `claude` 起動 | 既存テスト・dispatch.sh と完全互換、sidebar 表示は自動で効く | 並列実行時の terminal リソース消費、permission/auto モードを安全に渡せない、auto-worktree と二重 |
| B. 置き換え | Claude launcher を `claude --bg` 呼び出しに変更し、tmux session は作らない | supervisor の永続性・auto-worktree を活用、claude agents と一貫 | sidebar 上で Claude セッションが見えなくなる (要 jobs/state.json 連携)、`Switch` 動作の再設計 |
| C. ハイブリッド | `--bg` フラグ追加で opt-in。デフォルトは現状維持 | 段階的移行、Codex 経路は影響なし | コード分岐が増える、ユーザに二モード説明が必要 |
| D. 廃止 | Claude launcher 自体を dispatch から外し、Claude は `claude --bg` を直接使うよう案内 | コードがシンプルに | popup picker の Claude 経路が消える (#0010 と連動) |

## 変更箇所

- `internal/dispatch/dispatch.go` — `Launcher == LauncherClaude` 分岐の処理を分ける、または削除
- `internal/dispatch/worktree.go` — Claude 経路では worktree 作成を skip するか判定追加
- `internal/picker/picker.go` — Claude 選択時の挙動の見直し (#0010 と統合検討)
- `internal/dispatch/branch.go` — Branch 命名は claude agents 側に任せるならロジック削減 (#0007 と連動)
- `docs/spec.md` / `docs/design.md` — Claude 経路の振る舞い記述更新
- `docs/history.md` — 方針反転を伴うので append

## 実装チェックリスト

- [ ] `claude --bg` の出力 / exit code 仕様を実機で確認 (短い session ID, `claude attach <id>` 等)
- [ ] `claude --permission-mode` の interactive accept 要件を確認 (bypassPermissions / auto を非対話で渡せないという制約)
- [ ] sidebar 上で `claude --bg` 由来のセッションを可視化する方法を #0009 で決める
- [ ] 4 案から方針決定
- [ ] Codex 経路に影響しないことをテストで担保
- [ ] `docs/{spec,design,history}.md` を更新

## pending 理由

2026-05-13: Agent View はリサーチプレビュー段階。`claude --bg` の出力フォーマット、`--permission-mode` のインタラクティブ受諾制約、auto-worktree のパス規約 (`.claude/worktrees/`)、`disableAgentView` での無効化 — いずれも安定 API として保証されていない。launcher 対称性 (Claude/Codex 両 launcher を同じ UX で扱う)、tmux native との一体感 (`switch-client` / `kill-window` / pin)、ghq 横断 dispatch (#0010 参照) の価値を踏まえ、現状の tmux session 内起動モデルを維持する。観察ポイント:

- `claude --bg` の標準出力スキーマ (現状 `backgrounded · <id>`) が安定するか
- `--permission-mode bypassPermissions` / `auto` の非対話受け渡しが解禁されるか
- supervisor 経由のセッションが tmux 側で attach できる経路 (`claude attach <id>`) の互換性
- `disableAgentView` を強制している組織で fall back する公式パスが用意されるか

再評価のトリガは「Agent View の GA アナウンス」と「`claude --bg` の CLI スキーマが changelog で stable と明記されること」。それまでは現状の tmux session 内 `claude` 起動を維持する。
