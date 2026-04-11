---
name: open-sidebar
description: current tmux window にサイドバーを表示する。既存サイドバーがあれば kill してから再表示し、sidebar_id を設定する。
argument-hint: ""
version: 0.1.0
---

# open-sidebar

current tmux window にサイドバーペインを開く。既存サイドバーがある場合は先に kill してから表示し直す。

```fish
# 既存サイドバーペインを kill（なければ何もしない）
set sidebar_id (tmux list-panes -F "#{pane_id} #{@pane_role}" | awk '/sidebar/{print $1}')
if test -n "$sidebar_id"
    tmux kill-pane -t $sidebar_id
end

# サイドバーを開き、ペインオプション @pane_role=sidebar を設定する
set sidebar_id (tmux split-window -hfb -l 35 -P -F "#{pane_id}" tmux-sidebar)
tmux set-option -p -t $sidebar_id @pane_role sidebar
```

完了後、`$sidebar_id` に新しく開いたサイドバーのペイン ID が入った状態になる。
