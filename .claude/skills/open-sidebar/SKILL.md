---
name: open-sidebar
description: current tmux window にサイドバーを表示する。既存サイドバーがあれば kill してから再表示し、sidebar_id を設定する。
argument-hint: ""
version: 0.1.0
---

# open-sidebar

current tmux window にサイドバーペインを開く。既存サイドバーがある場合は先に kill してから表示し直す。

以下のコマンドを **順番に** Bash ツールで個別実行する（複合コマンドは使わない）:

1. 既存サイドバーのペインIDを取得する:
   ```
   tmux list-panes -F "#{pane_id} #{@pane_role}" | awk '/sidebar/{print $1}'
   ```
2. ステップ1の出力が空でなければ、そのペインIDで kill する:
   ```
   tmux kill-pane -t <pane_id>
   ```
3. 新しいサイドバーを開く:
   ```
   tmux split-window -hfb -l 40 -P -F "#{pane_id}" tmux-sidebar
   ```
4. ステップ3の出力（新しいペインID）に `@pane_role=sidebar` を設定する:
   ```
   tmux set-option -p -t <new_pane_id> @pane_role sidebar
   ```

完了後、`$sidebar_id` に新しく開いたサイドバーのペイン ID が入った状態になる。
