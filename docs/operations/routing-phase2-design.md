# URLルーティング フェーズ2 設計メモ

この文書は、`mamotama-proxy` のフェーズ1 URL ルーティング実装を前提に、フェーズ2を安全に分割実装するための設計整理です。

前提:

- フェーズ1 corrective patch は `main` まで反映済み
- 今回はコード変更を行わない
- フェーズ2/3をまとめて実装しない
- `dry-run` と runtime の整合を壊さない
- 大規模抽象化や全面改修を避ける

## 1. フェーズ2候補一覧

対象は次の3機能に限定する。

1. `response header rewrite`
2. `regex path match`
3. `host rewrite`

フェーズ3送り:

- weighted upstream
- canary / blue-green
- mirror traffic
- query rewrite
- method / source IP / arbitrary header condition
- retry / circuit breaker の高度化

## 2. 推奨順序

推奨順序は次の通り。

1. `response header rewrite`
2. `regex path match`
3. `host rewrite`

理由:

- `response header rewrite` は route 選択ロジックに入らず、既存 phase1 の経路選択を壊しにくい
- `regex path match` は route 選択に関わるが、rewrite や upstream 接続先そのものは変えない
- `host rewrite` は upstream virtual host、TLS upstream、監査ログに影響しやすく、最も破壊リスクが高い

## 3. 分割案

### フェーズ2.1

`response header rewrite` のみ実装する。

### フェーズ2.2

`regex path match` のみ実装する。

### フェーズ2.3

`host rewrite` のみ実装する。

同時実装しない理由:

- `regex path match` と `host rewrite` を同時に入れると、route 選択の変化と upstream 応答変化の切り分けが難しい
- `response header rewrite` は phase1 の route/action schema に自然に追加できるため、先に独立で入れやすい

## 4. 各機能の整理

### 4.1 response header rewrite

目的:

- upstream 応答に対して route 単位で `set` / `add` / `remove` を適用する
- security header、cache header、アプリ固有の補助 header を付与できるようにする

主な影響箇所:

- `coraza/src/internal/handler/proxy_routing.go`
  - route action schema
  - validation
  - `dry-run` の表示項目整理
- `coraza/src/internal/handler/proxy_runtime.go`
  - `ModifyResponse`
- `coraza/src/internal/handler/proxy_routing_test.go`
- `README.md` / `README.ja.md`

後方互換リスク:

- 低い
- 未設定時は既存挙動のままにできる

セキュリティ/運用上の注意:

- hop-by-hop headers は禁止する
- 初手では `Content-Length` / `Transfer-Encoding` / `Connection` / `Upgrade` / `Trailer` を触らせない
- `Set-Cookie` は影響が大きいため、初手は対象外にする方が安全
- 既存 `error_html_file` / `error_redirect_url` による保守応答へ適用するかどうかを明確に決める
  - 推奨: upstream 応答のみに適用し、proxy 自前エラー応答は対象外

必要テスト:

- `set` / `add` / `remove` の順序
- 禁止 header の reject
- route 未一致時は無変更
- `default_route` 選択時にも正しく適用されること
- `dry-run` と runtime で対象 route が一致すること

READMEに追記すべき内容:

- `response_headers` の schema
- 禁止 header 一覧
- 適用対象は upstream 応答のみであること
- phase1 の request header 操作との違い

### 4.2 regex path match

目的:

- phase1 の `exact` / `prefix` では表現しづらい path 条件を route match に追加する

主な影響箇所:

- `coraza/src/internal/handler/proxy_routing.go`
  - `match.path.type`
  - regex compile/validate
  - route match
  - `dry-run`
- `coraza/src/internal/handler/proxy_routing_test.go`
- `README.md` / `README.ja.md`

後方互換リスク:

- 中
- 新設定使用時だけ影響するが、route 選択順に直接入る

セキュリティ/運用上の注意:

- `priority first-match` を維持し、暗黙 specificity は導入しない
- match 対象は query を含まない path のみとする
- 初手では Go 標準 `regexp` に限定し、複雑な flags や capture 利用は入れない
- catastrophic backtracking は Go の RE2 で避けられるが、複雑な正規表現で保守性が落ちる点は README に書く

必要テスト:

- valid / invalid regex
- `exact` / `prefix` / `regex` の共存
- `priority` が最優先であること
- `dry-run` と runtime の一致
- routes 未設定時の後方互換

READMEに追記すべき内容:

- `type=regex` の追加
- match 対象は request path のみで query を含まないこと
- 優先順は `priority` に完全依存すること

### 4.3 host rewrite

目的:

- upstream 側の virtual host 要求に合わせて outbound `Host` を固定値にする

主な影響箇所:

- `coraza/src/internal/handler/proxy_routing.go`
  - route action schema
  - validation
  - `dry-run` 出力
  - log 項目
- `coraza/src/internal/handler/proxy_runtime.go`
  - ReverseProxy `Rewrite`
- `coraza/src/internal/handler/proxy_routing_test.go`
- `README.md` / `README.ja.md`

後方互換リスク:

- 中〜高
- upstream が Host ベースで動作している場合、route の見た目以上に挙動が変わる

セキュリティ/運用上の注意:

- 初手は固定文字列のみとし、テンプレート展開は入れない
- `request_headers` 経由で `Host` を変更する抜け道は引き続き禁止する
- HTTPS upstream では `Host` header と SNI のズレを README に明記する
  - 初手は `Host` のみ rewrite、SNI 制御は入れない
- `X-Forwarded-Host` は従来どおり proxy 側管理に任せ、route action からは触らせない

必要テスト:

- HTTP upstream に対する `Host` rewrite
- 未設定時の完全後方互換
- `dry-run` と runtime の selected route / final upstream 表示
- log に rewritten host が出ること

READMEに追記すべき内容:

- `host_rewrite` の schema
- 接続先 URL は変えず、送信 `Host` だけを書き換えること
- HTTPS upstream の注意点

## 5. 実装難易度・破壊リスク・依存関係

| 機能 | 実装難易度 | 破壊リスク | 主依存 |
| --- | --- | --- | --- |
| response header rewrite | 低〜中 | 低 | `ModifyResponse`, route action validation |
| regex path match | 中 | 中 | route match helper, `dry-run`, README |
| host rewrite | 中 | 中〜高 | ReverseProxy `Rewrite`, log/dry-run, HTTPS upstream 注意 |

## 6. 実装開始時の最小パッチ単位

### フェーズ2.1 最小パッチ

- `ProxyRouteAction` に `response_headers` を追加
- validation を追加
- `ModifyResponse` に route action 適用を追加
- unit test + README 更新

### フェーズ2.2 最小パッチ

- `match.path.type=regex` を追加
- regex compile/validate と route match helper を追加
- `dry-run` / unit test / README 更新

### フェーズ2.3 最小パッチ

- `ProxyRouteAction` に `host_rewrite` を追加
- validation を追加
- ReverseProxy `Rewrite` で `Out.Host` を上書き
- log / `dry-run` 表示追加
- unit test + README 更新

## 7. 実装時のレビュー重点

フェーズ2着手時に特に見るべき点:

1. `dry-run` と runtime が同じ helper を使っているか
2. route 選択順 (`priority` first-match) を壊していないか
3. phase1 legacy fallback を壊していないか
4. 禁止 header / `Host` / `X-Forwarded-*` の安全側が維持されているか
5. README が実装の制約を隠していないか

## 8. 今回の結論

フェーズ2は次の順で、小さく独立実装するのが妥当:

1. `response header rewrite`
2. `regex path match`
3. `host rewrite`

この順なら、フェーズ1の安定性を保ったまま、route schema を段階的に拡張できる。
