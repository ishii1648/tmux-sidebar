# `tmux-sidebar upgrade` が GitHub API の未認証レート制限で 403 になる

Created: 2026-05-26
Completed: 2026-05-26
Model: Opus 4.7

## 概要

`tmux-sidebar upgrade` が latest release を取得する際、GitHub Releases API を未認証で叩いていた。GitHub API は未認証だと IP あたり 60 req/h の core rate limit が課されるため、共有 NAT 配下や短時間に複数回 upgrade を試した環境では `403 API rate limit exceeded` で失敗する。

## 根拠

`internal/upgrade/upgrade.go` の `fetchLatestRelease` は `http.Get(url)` のみで API を呼んでおり、`User-Agent` / `Accept` / `Authorization` のいずれも付けていなかった。

実環境で再現したレスポンス:

```
HTTP/2 403
x-ratelimit-limit: 60
x-ratelimit-remaining: 0
x-ratelimit-resource: core
{ "message": "API rate limit exceeded for <ip>.", ... }
```

認証ヘッダを付けると core rate limit は 5000 req/h に上がるため、token があれば実質的に回避できる。

## 対応方針

- `fetchLatestRelease` を `http.NewRequest` + `http.DefaultClient.Do` に書き換える
- 常に `User-Agent: tmux-sidebar` と `Accept: application/vnd.github+json` を付ける（GitHub API の慣行）
- 環境変数 `GITHUB_TOKEN` または `GH_TOKEN` が設定されていれば `Authorization: Bearer <token>` を付ける（両方ある場合は `GITHUB_TOKEN` を優先）
- 既存のエラーハンドリング・戻り値の形は変えない
- `downloadToTemp` 側はリリースアセットの CDN URL で API レート制限の対象外なので触らない

## 解決方法

PR #58 で対応済み。

- `internal/upgrade/upgrade.go`: `fetchLatestRelease` を `http.NewRequest` + `http.DefaultClient.Do` 化し、`User-Agent` / `Accept` を常時付与、`GITHUB_TOKEN`（優先）/ `GH_TOKEN` があれば `Authorization: Bearer <token>` を付与する `githubToken()` ヘルパを追加

検証として `go build ./...` / `go vet ./...` が通ることを確認した（`internal/upgrade` にはテストなし）。
