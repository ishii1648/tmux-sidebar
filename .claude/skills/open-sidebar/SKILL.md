---
name: open-sidebar
description: current tmux window にサイドバーを表示する。既存サイドバーがあれば kill してから再表示し、sidebar_id を設定する。
argument-hint: ""
version: 0.1.0
---

# open-sidebar

current tmux window にサイドバーペインを開く。既存サイドバーがある場合は先に kill してから表示し直す。

以下のコマンドを **順番に** Bash ツールで個別実行する（複合コマンドは使わない）:

1. 現在のウィンドウの既存サイドバーを**全て** kill する（0個でもエラーにならない）:
   ```
   tmux list-panes -F "#{pane_id} #{@pane_role}" | awk '/sidebar/{print $1}' | xargs -I{} tmux kill-pane -t {}
   ```
2. 新しいサイドバーを開く:
   ```
   tmux split-window -hfb -l 40 -P -F "#{pane_id}" tmux-sidebar
   ```
3. ステップ2の出力（新しいペインID）に `@pane_role=sidebar` を設定する:
   ```
   tmux set-option -p -t <new_pane_id> @pane_role sidebar
   ```

完了後、`$sidebar_id` に新しく開いたサイドバーのペイン ID が入った状態になる。
