# tmux-sidebar 実装 TODO

設計ドキュメント (`design.md`) をもとに、Go 実装のタスクを整理したもの。

---

## 0. 事前決定事項（実装前に確定が必要）

| # | 未決定事項 | 選択肢 | 推奨 |
|---|-----------|--------|------|
| 0-1 | TUI ライブラリ | [bubbletea](https://github.com/charmbracelet/bubbletea) / [tview](https://github.com/rivo/tview) / 自前 | bubbletea（Elm アーキテクチャ、テストしやすい） |
| 0-2 | 通常ペイン移動からの除外方式 | `after-select-pane` hook で即座に離脱 / TUI 側でキャプチャして無視 | hook 方式（tmux 側で制御、TUI 実装を単純に保てる） |
| 0-3 | passive / interactive モード切り替え | 常時インタラクティブ / キー入力で切り替え | キー入力で切り替え（`q` / `Esc` で passive に戻る） |

---

## 1. プロジェクト初期化

- [x] `go mod init github.com/ishii1648/tmux-sidebar`
- [x] ディレクトリ構成を決定・作成
  ```
  tmux-sidebar/
  ├── main.go
  ├── internal/
  │   ├── tmux/       # tmux コマンド呼び出し
  │   ├── state/      # 状態ファイル読み取り
  │   └── ui/         # TUI モデル・ビュー
  ├── e2e/            # tmux capture-pane ベースの E2E テスト
  │   ├── helpers.go
  │   ├── display_test.go
  │   ├── polling_test.go
  │   └── keyboard_test.go
  ├── docs/
  └── .github/workflows/
  ```
- [x] TUI ライブラリ（0-1 の決定後）を `go get` で追加

---

## 2. tmux データ取得層 (`internal/tmux`)

- [x] **セッション一覧取得**
  - `tmux list-sessions -F "#{session_id}:#{session_name}"` を実行してパース
- [x] **ウィンドウ一覧取得**
  - `tmux list-windows -a -F "#{session_id}:#{window_id}:#{window_index}:#{window_name}"` をパース
- [x] **現在のペイン/ウィンドウ情報取得**
  - `tmux display-message -p "#{session_id}:#{window_id}:#{pane_id}"` で自分自身を特定
- [x] **ウィンドウ切り替え**
  - `tmux switch-client -t {session_name}` + `tmux select-window -t :{window_index}` を実行
- [x] **ペイン番号の取得**（状態ファイルのキー解決用）
  - `tmux list-panes -a -F "#{session_id}:#{window_id}:#{pane_id}:#{pane_index}"` をパース
  - pane_id（`%N` 形式）から数値 N を抽出

---

## 3. 状態ファイル読み取り層 (`internal/state`)

ADR-007 の仕組みを継続利用する。

- [x] **状態ファイルのスキャン**
  - `/tmp/claude-pane-state/` 配下のファイルを列挙
  - `pane_N` → ステータス（`running` / `idle` / `permission` / `ask`）
  - `pane_N_started` → running 開始時刻（epoch）
- [x] **経過時間計算**
  - `pane_N_started` の値から現在時刻との差分を分数で返す
- [x] **ウィンドウとペインの紐付け**
  - tmux 層から取得したペイン情報と状態ファイルの `pane_N` を突合

---

## 4. TUI モデル (`internal/ui`)

bubbletea を使用する場合の実装。

### 4-1. データ構造

- [x] `Session` / `Window` / `PaneState` 型の定義
- [x] フラット化したリスト行（`ListItem`）型の定義
  - セッションヘッダ行 / ウィンドウ行を区別するフィールドを持つ

### 4-2. Model（状態）

- [x] `cursor int` — 現在選択中の行インデックス
- [x] `items []ListItem` — 表示行のリスト
- [x] `mode` — `passive` / `interactive` の切り替え

### 4-3. Update（キー入力処理）

| キー | 処理 |
|------|------|
| `j` / `↓` | cursor を次のウィンドウ行へ移動（セッションヘッダはスキップ） |
| `k` / `↑` | cursor を前のウィンドウ行へ移動 |
| `Enter` | 選択ウィンドウへ `switch-client` + `select-window` |
| `q` / `Esc` | passive モードに戻る |

- [x] passive モード中はキー入力を完全に無視（ペインに通常の入力が届く）
- [ ] interactive モードへの移行トリガー検討（`prefix+e` でフォーカス後に自動移行 など）

### 4-4. View（描画）

- [x] セッション名のヘッダ行描画
- [x] ウィンドウ行の描画
  - 現在のウィンドウに `▶` カーソルを表示
- [x] 状態バッジの描画
  - `[running 3m]` / `[idle]` / `[permission]` / `[ask]`
  - 状態ごとに色付け（bubbletea の `lipgloss` 使用）
- [x] 1 秒ごとのポーリング更新（`tea.Tick` を使用）

---

## 5. 通常ペイン移動からの除外

0-2 の決定に基づく実装。

### hook 方式（推奨）

- [x] `after-select-pane` フック用のサブコマンド実装
  - `tmux-sidebar focus-guard` などで呼び出す
  - 選択されたペインの `@pane_role` が `sidebar` なら直前のペインに戻る
- [ ] dotfiles 側での設定方法をドキュメント化（Section 10 スコープ外）

---

## 6. 単体テスト

各層を tmux 実環境なしにテストできるよう、依存を interface で抽象化したうえでテストを書く。

### 6-1. tmux 層のモック化

- [x] `TmuxClient` interface を定義する（`ListSessions` / `ListWindows` / `SwitchWindow` など）
- [x] テスト用の `FakeTmuxClient` 実装を用意し、固定データを返せるようにする

### 6-2. 状態ファイル層 (`internal/state`)

- [x] `StateReader` interface を定義し、`fsStateReader`（実装）と `fakeStateReader`（テスト用）を分離
- [x] **正常系**: `running` / `idle` / `permission` / `ask` が正しく読み取れること
- [x] **経過時間計算**: epoch 値から分数が正しく計算されること
- [x] **ファイルなし**: 状態ファイルが存在しないペインは `idle` 扱いになること
- [x] **不正値**: ファイル内容が空や未知の文字列でもパニックしないこと

### 6-3. tmux パース層 (`internal/tmux`)

- [x] `list-sessions` / `list-windows` / `list-panes` の出力文字列をパースする関数の単体テスト
- [x] セッション名にコロンや空白を含む場合も正しく扱えること
- [x] pane_id（`%101` 形式）から数値抽出が正しく行えること

### 6-4. TUI モデル (`internal/ui`)

bubbletea の `Update` 関数は純粋関数（`Model → Msg → Model`）なので、tmux 環境なしにテスト可能。

- [x] **カーソル移動**
  - `j` / `↓` でセッションヘッダ行をスキップして次のウィンドウ行に移動すること
  - `k` / `↑` で同様にスキップすること
  - リスト末尾・先頭でループしないこと（または設計通りに振る舞うこと）
- [x] **passive モードではキー入力を無視**
  - passive モード中に `j` / `Enter` を送っても Model が変化しないこと
- [x] **Enter でコマンドが発行される**
  - `Enter` 時に `SwitchWindowCmd` が返ること（実際の tmux 呼び出しはしない）
- [x] **View の出力検証**
  - 固定データを与えたときの `View()` 文字列に `▶` カーソル・状態バッジ・セッション名が含まれること

### 6-5. テスト実行

- [x] `go test ./...` が CI（GitHub Actions）で通ること
- [x] `.github/workflows/ci.yml` に `go test` ステップを追加

---

## 7. E2E テスト（`tmux capture-pane` ベース）

`tmux -L e2e` で独立したサーバを立て、`tmux capture-pane -p` でペイン内容をテキストとして取得し、
期待文字列と比較する。CI（GitHub Actions）で自動実行する。

### 7-1. テスト基盤の実装 (`e2e/`)

- [x] `e2e/helpers.go` に以下のヘルパーを実装
  - `newTestEnv(t)` — 分離 tmux サーバ起動・`t.Cleanup` でクリーンアップ
  - `capturePane(target string) string` — `tmux capture-pane -p` でペイン内容を返す
  - `sendKeys(target, keys string)` — `send-keys` でキーを送信
  - `waitForText` / `waitForNoText` — capture-pane をポーリング
  - `setupStateFile` / `setupStateFileStarted` / `removeStateFile` — 状態ファイル操作

- [x] テスト用フィクスチャ（ダミーセッション + 状態ファイル）のセットアップ処理を共通化

  | セッション | ウィンドウ | 状態ファイル | 期待バッジ |
  |-----------|-----------|------------|-----------|
  | session-a | 1: main   | なし        | （なし）  |
  | session-a | 2: work   | `running` + `_started` = 3分前 | `[running 3m]` |
  | session-b | 1: idle   | `idle`     | `[idle]`  |
  | session-b | 2: perm   | `permission` | `[permission]` |
  | session-b | 3: ask    | `ask`      | `[ask]`   |

### 7-2. 表示 snapshot テスト (`e2e/display_test.go`)

- [x] **セッション・ウィンドウ一覧の描画**
  - `capturePane` の出力にセッション名・ウィンドウ名が含まれること
- [x] **状態バッジの描画**
  - `[idle]` / `[permission]` / `[ask]` / `[running` が対応ウィンドウ行に含まれること
- [x] **running バッジの描画**
  - epoch ファイルを書き込んで `[running` で始まるバッジが出現すること

### 7-3. ポーリング更新テスト (`e2e/polling_test.go`)

- [x] 状態ファイルなし → 書き込み後に `[idle]` バッジが出現することを確認
- [x] 状態ファイルあり → 削除後にバッジが消えることを確認

### 7-4. キーボード操作テスト (`e2e/keyboard_test.go`)

- [x] **カーソル移動**
  - `j` で `▶` が出現し、2回目の `j` で位置が変わること
  - `k` でカーソルが戻ること（セッションヘッダはスキップ）
- [x] **q で passive モードへ移行**
  - `q` 後に `[i] to activate` が出現し `▶` が消えること
- [x] **i でインタラクティブモードへ復帰**
  - `i` 後に `[i] to activate` が消えること

### 7-5. CI 設定

- [x] `.github/workflows/ci.yml` に E2E テストのステップを追加（`e2e` ジョブで tmux インストール + `go test -tags e2e`）

---

## 8. メインエントリポイント (`main.go`)

- [x] `split-window` 経由で起動された際のサイドバーモード動作
- [x] サブコマンド構成（必要に応じて）
  - `tmux-sidebar` — サイドバー本体起動
  - `tmux-sidebar focus-guard` — `after-select-pane` フック用
- [x] ペイン幅の自動検出（`$COLUMNS` 環境変数または `tmux display-message -p "#{pane_width}"`）

---

## 9. リリース設定

- [x] `goreleaser.yaml` の作成
  - `GOOS=darwin,linux` / `GOARCH=amd64,arm64` でクロスビルド
- [x] `.github/workflows/release.yml` の作成
  - `git tag v*` push をトリガーに `goreleaser release` を実行
- [ ] aqua レジストリ対応
  - `aqua-registry` への PR、または `aqua.yaml` の `inline_registry` で対応

---

## 10. dotfiles 側の設定（実装後に dotfiles リポジトリで対応）

実装完了後、dotfiles 側で以下を設定する（このリポジトリのスコープ外）。

- [ ] `aqua.yaml` にバイナリを追加
- [ ] `tmux.conf` に `after-new-window` フックを追加
  ```
  set-hook -g after-new-window 'run-shell "tmux-sidebar"'
  ```
- [ ] `prefix+e` の toggle keybind 設定
- [ ] `after-select-pane` フックの設定（hook 方式を採用した場合）

---

## 実装優先順位

```
[高] 2. tmux データ取得層          ← interface 設計を先に固める
[高] 3. 状態ファイル読み取り層     ← interface 設計を先に固める
[高] 6. 単体テスト                 ← 2/3/4 と並行して書く
[高] 4. TUI モデル（最低限の表示・Enter 移動）
[中] 7. E2E テスト（scripts/e2e-setup.sh + チェックリスト実施）
[中] 5. 通常ペイン移動からの除外
[中] 9. リリース設定
[低] 8. サブコマンド構成の整備
[後] 10. dotfiles 側設定
```
