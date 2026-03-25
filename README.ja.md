# mamotama

Coraza + CRS WAFプロジェクト

[English](README.md) | [日本語](README.ja.md)

![管理画面トップ](docs/images/admin-dashboard-overview.png)

## 概要

このプロジェクトは、Coraza WAF と OWASP Core Rule Set (CRS) を組み合わせた
軽量かつ強力なアプリケーション防御システム「mamotama」です。

---

## ルールファイルについて

本リポジトリには、ライセンス順守のため OWASP CRS 本体は同梱していません。  
代わりに、初期状態で動作可能な最小ベースルール `data/rules/mamotama.conf` を同梱しています。

### セットアップ手順

以下のコマンドで CRS を取得・配置してください（デフォルト: `v4.23.0`）。

```bash
./scripts/install_crs.sh
```

バージョン指定例:

```bash
./scripts/install_crs.sh v4.23.0
```

`data/rules/crs/crs-setup.conf` は必要に応じて編集してください（`Paranoia Level` や `anomaly` スコアなど）。

---

## 環境変数

`.env` ファイルで挙動を制御可能です。

### Docker / ローカル MySQL（任意）

| 変数名 | 例 | 説明 |
| --- | --- | --- |
| `MYSQL_PORT` | `13306` | MySQL コンテナ `3306` に割り当てるホスト側ポート（`mysql` profile 有効時）。 |
| `MYSQL_DATABASE` | `mamotama` | ローカル MySQL コンテナで初期作成するDB名。 |
| `MYSQL_USER` | `mamotama` | ローカル MySQL コンテナで作成するアプリ用ユーザー。 |
| `MYSQL_PASSWORD` | `mamotama` | `MYSQL_USER` のパスワード。 |
| `MYSQL_ROOT_PASSWORD` | `mamotama-root` | ローカル MySQL コンテナの root パスワード。 |
| `MYSQL_TZ` | `UTC` | コンテナのタイムゾーン。 |

### WAF / Go（Coraza ラッパー）

| 変数名 | 例 | 説明 |
| --- | --- | --- |
| `WAF_LISTEN_ADDR` | `:9090` | Corazaシングルバイナリサービスの待受アドレス。 |
| `WAF_LISTEN_PORT` | `9090` | Compose で使うコンテナ側待受ポート（`ports` / healthcheck / GoTestWAF ターゲット）。`WAF_LISTEN_ADDR` のポートと揃えてください。 |
| `WAF_PROXY_CONFIG_FILE` | `conf/proxy.json` | 必須のProxy設定JSONパス。欠落/不正なら起動失敗。 |
| `WAF_PROXY_ROLLBACK_HISTORY_SIZE` | `8` | `/proxy-rules:rollback` で使うメモリ上ロールバック履歴件数（`1..64`）。 |
| `WAF_LOG_FILE` | (空) | WAFログの出力先。未設定なら標準出力。 |
| `WAF_BYPASS_FILE` | `conf/waf.bypass` | バイパス/特別ルール定義ファイルのパス。 |
| `WAF_BOT_DEFENSE_FILE` | `conf/bot-defense.conf` | Bot defense challenge 設定ファイル（JSON）。管理画面から編集可能。 |
| `WAF_SEMANTIC_FILE` | `conf/semantic.conf` | Semanticヒューリスティック設定ファイル（JSON）。管理画面から編集可能。 |
| `WAF_COUNTRY_BLOCK_FILE` | `conf/country-block.conf` | 国別ブロック定義ファイル（1行1国コード、例: `JP`, `US`, `UNKNOWN`）。 |
| `WAF_RATE_LIMIT_FILE` | `conf/rate-limit.conf` | レート制限定義ファイル（JSON）。管理画面から編集可能。 |
| `WAF_RULES_FILE` | `rules/mamotama.conf` | 使用するルールファイル（カンマ区切りで複数指定も可）。 |
| `WAF_CRS_ENABLE` | `true` | CRSを読み込むかどうか。`false` ならベースルールのみ。 |
| `WAF_CRS_SETUP_FILE` | `rules/crs/crs-setup.conf` | CRSセットアップ設定ファイル。 |
| `WAF_CRS_RULES_DIR` | `rules/crs/rules` | CRS本体ルール（`*.conf`）のディレクトリ。 |
| `WAF_CRS_DISABLED_FILE` | `conf/crs-disabled.conf` | CRS本体の無効化ファイル一覧。1行1ファイル名で指定。 |
| `WAF_FP_TUNER_MODE` | `mock` | FPチューナーのプロバイダモード。`mock` はフィクスチャ/生成提案、`http` は `WAF_FP_TUNER_ENDPOINT` へPOST。 |
| `WAF_FP_TUNER_ENDPOINT` | (空) | `http` モード時の外部LLMプロキシのHTTPエンドポイント。 |
| `WAF_FP_TUNER_API_KEY` | (空) | `WAF_FP_TUNER_ENDPOINT` 向け Bearer トークン。 |
| `WAF_FP_TUNER_MODEL` | (空) | プロバイダへ渡す任意のモデル識別子。 |
| `WAF_FP_TUNER_TIMEOUT_SEC` | `15` | プロバイダ呼び出し時のHTTPタイムアウト（秒）。 |
| `WAF_FP_TUNER_MOCK_RESPONSE_FILE` | `conf/fp-tuner-mock-response.json` | `mock` モードで使うレスポンスフィクスチャのパス。 |
| `WAF_FP_TUNER_REQUIRE_APPROVAL` | `true` | `simulate=false` の適用時に承認トークンを必須化するか。 |
| `WAF_FP_TUNER_APPROVAL_TTL_SEC` | `600` | 承認トークンの有効期限（秒）。 |
| `WAF_FP_TUNER_AUDIT_FILE` | `logs/coraza/fp-tuner-audit.ndjson` | propose/apply 操作の監査ログ出力先。 |
| `WAF_STORAGE_BACKEND` | `file` | ストレージバックエンド選択。`file` は従来のファイル運用、`db` はDBログストア + 設定/ルールBlob同期を有効化。 |
| `WAF_DB_DRIVER` | `sqlite` | `WAF_STORAGE_BACKEND=db` 時のDBドライバ。対応値: `sqlite` / `mysql`（ログストア・設定/ルールBlob用途で実装済み）。 |
| `WAF_DB_ENABLED` | `false` | 互換用フラグ。`WAF_STORAGE_BACKEND` 未指定時のみ参照され、`true` で `db`、`false` で `file` にマップ。 |
| `WAF_DB_DSN` | (空) | ネットワークDB向けDSN（例: MySQL）。`WAF_DB_DRIVER=mysql` 時は必須。sqliteは `WAF_DB_PATH` を利用。 |
| `WAF_DB_PATH` | `logs/coraza/mamotama.db` | `WAF_STORAGE_BACKEND=db` かつ `WAF_DB_DRIVER=sqlite` 時に利用するSQLiteファイルパス。 |
| `WAF_DB_RETENTION_DAYS` | `30` | DBストア `waf_events` の保持日数。これより古い行は同期時に削除。`0` で削除無効（設定Blobは削除対象外）。 |
| `WAF_DB_SYNC_INTERVAL_SEC` | `0` | DB→実行時設定の定期同期間隔（秒）。`0` で無効、`1` 以上で複数Corazaノード間の定期整合を有効化。 |
| `WAF_STRICT_OVERRIDE` | `false` | 特別ルール読み込み失敗時の挙動。`true`で即終了、`false`で警告のみ継続。 |
| `WAF_API_BASEPATH` | `/mamotama-api` | 管理APIのベースパス（Go側のルーティング基準）。 |
| `WAF_API_KEY_PRIMARY` | `…` | 管理API用の主キー（`X-API-Key`）。 |
| `WAF_API_KEY_SECONDARY` | (空) | 予備キー（ローテーション時の切替用。未使用なら空でOK）。 |
| `WAF_API_AUTH_DISABLE` | (空) | 認証無効化フラグ。運用では空（false相当）推奨。テストで無効化したいときのみ truthy 値。 |
| `WAF_API_CORS_ALLOWED_ORIGINS` | `https://admin.example.com,http://localhost:5173` | CORSを許可する Origin 一覧（カンマ区切り）。未設定なら CORS 無効（同一オリジンのみ）。 |
| `WAF_ALLOW_INSECURE_DEFAULTS` | (空) | 弱いAPIキーや認証無効化を許可する開発用フラグ。本番では設定しない。 |

### 管理UI

起動時に `WAF_API_KEY_PRIMARY` が短すぎる/既知の弱い値の場合、Corazaプロセスは安全側で起動失敗します。  
ローカル検証だけ一時的に緩和したい場合は `WAF_ALLOW_INSECURE_DEFAULTS=1` を利用してください。

---

## 管理ダッシュボード

管理UIはGoバイナリに埋め込まれて `/mamotama-ui` で配信されます。  
フロント実装自体は `web/mamotama-admin/` にあり、build後にGoへ埋め込みます。

![管理画面 Dashboard](docs/images/admin-dashboard-overview.png)

### 主な画面と機能

| パス | 説明 |
| --- | --- |
| `/status` | WAFの動作状況、設定の確認 |
| `/logs` | WAFログの取得・表示 |
| `/rules` | 使用中のベースルールファイル（`rules/mamotama.conf` など）の閲覧・編集 |
| `/rule-sets` | CRS本体ルール（`rules/crs/rules/*.conf`）の有効/無効切替 |
| `/bypass` | バイパス設定の閲覧・編集（waf.bypassを直接操作） |
| `/country-block` | 国別ブロック設定の閲覧・編集（country-block.conf を直接操作） |
| `/rate-limit` | レート制限設定の閲覧・編集（rate-limit.conf を直接操作） |
| `/bot-defense` | Bot defense設定の閲覧・編集（bot-defense.conf を直接操作） |
| `/semantic` | Semantic Security設定の閲覧・編集（semantic.conf を直接操作） |
| `/cache-rules` | Cache Rules の可視化・編集（cache.conf の表編集／Raw編集、Validate/Save対応） |
| `/proxy-rules` | 上流URL・Transport設定の検証/プローブ/更新/ロールバック（`conf/proxy.json`） |

### 画面キャプチャ

#### Dashboard
![Dashboard](docs/images/admin-dashboard-overview.png)

#### Rules Editor
![Rules Editor](docs/images/admin-rules-editor.png)

#### Rule Sets
![Rule Sets](docs/images/admin-rule-sets.png)

#### Bypass Rules
![Bypass Rules](docs/images/admin-bypass-rules.png)

#### Country Block
![Country Block](docs/images/admin-country-block.png)

#### Rate Limit
![Rate Limit](docs/images/admin-rate-limit.png)

#### Cache Rules
![Cache Rules](docs/images/admin-cache-rules.png)

#### Logs
![Logs](docs/images/admin-logs.png)

### ライブラリ

* coraza 3.3.3
* go 1.25.7
* React 19
* Vite 7
* Tailwind CSS
* react-router-dom
* ShadCN UI（TailwindベースUI）

### 起動方法

```bash
./scripts/install_crs.sh
docker compose build coraza
docker compose up -d coraza
```

起動後、管理UIは `http://localhost:${CORAZA_PORT:-9090}/mamotama-ui` で開けます。  
UIヘッダの API キー入力欄に `WAF_API_KEY_PRIMARY` を設定して利用してください。

#### 任意: 旧Proxy環境変数からの移行（`WAF_APP_URL` -> `conf/proxy.json`）

旧来の env 起点設定から移行する場合は、以下で `proxy.json` を生成・検証できます。

```bash
./scripts/migrate_proxy_config.sh
./scripts/migrate_proxy_config.sh --check
```

デフォルトでは `.env` を読み取り、`WAF_PROXY_CONFIG_FILE=conf/proxy.json` をホスト側の `data/conf/proxy.json` に解決します。

#### 任意: ローカル MySQL コンテナ（profile: `mysql`）

将来の DB ドライバ検証用に、ローカル MySQL コンテナを起動できます:

```bash
docker compose --profile mysql up -d mysql
```

MySQL をDBログ/設定運用で使う場合は、`WAF_STORAGE_BACKEND=db`・`WAF_DB_DRIVER=mysql`・`WAF_DB_DSN`（例: `mamotama:mamotama@tcp(mysql:3306)/mamotama?charset=utf8mb4&parseTime=true`）を設定してください。

複数ノード運用では `WAF_DB_SYNC_INTERVAL_SEC`（例: `10`）を設定すると、各ノードが `config_blobs` から定期的に実行時ファイルを同期し、内容差分がある場合のみ reload します。

スケールアウト運用では、共有MySQLを使う `db + mysql` を標準構成にしてください。`file` と `db + sqlite` は基本的に単一ノード運用/ローカル検証向けです。

### WAF回帰テスト（GoTestWAF）

ローカルで回帰テストを実行:

```bash
./scripts/run_gotestwaf.sh
```

前提条件:

- Docker と Docker Compose が利用可能であること
- スクリプトが `coraza` を自動で build/up すること
- 既定のホスト公開ポートは `HOST_CORAZA_PORT=19090`
- 初回実行時は GoTestWAF イメージ取得のため時間がかかる場合があること

デフォルトの合否基準は `MIN_BLOCKED_RATIO=70` です。追加基準は任意で指定できます:

```bash
MIN_TRUE_NEGATIVE_PASSED_RATIO=95 MAX_FALSE_POSITIVE_RATIO=5 MAX_BYPASS_RATIO=30 ./scripts/run_gotestwaf.sh
```

レポート出力先は `data/logs/gotestwaf/` です:

- JSONフルレポート: `gotestwaf-report.json`
- Markdownサマリ: `gotestwaf-report-summary.md`
- Key-Valueサマリ: `gotestwaf-report-summary.txt`

### Proxyチューニングベンチ

ローカル `coraza` に対してプリセット比較ベンチを実行:

```bash
BENCH_REQUESTS=120 WARMUP_REQUESTS=20 ./scripts/benchmark_proxy_tuning.sh
```

このスクリプトは次を自動実行します:

- 一時 upstream（`python3 -m http.server`）を起動
- `/mamotama-api/proxy-rules` 経由でプリセット適用
- `/bench` 経路で遅延サンプリング
- Markdownサマリ出力（既定: `data/logs/proxy/proxy-benchmark-summary.md`）
- 終了時に元の proxy 設定へ復元

推奨プリセット:

| プリセット | 主な設定 | 用途 |
| --- | --- | --- |
| `balanced` | `force_http2=false`, `disable_compression=false`, `buffer_request_body=false`, `flush_interval_ms=0` | 汎用Web向けの標準設定 |
| `low-latency` | `force_http2=true`, `disable_compression=true`, `buffer_request_body=false`, `flush_interval_ms=5` | API/SSE の低遅延重視 |
| `buffered-guard` | `force_http2=true`, `buffer_request_body=true`, `max_response_buffer_bytes=1048576`, `flush_interval_ms=25` | バッファ制御と応答サイズ上限を重視 |

### デプロイ例

実用向けのサンプル構成を以下に用意しています:

- `examples/nextjs`（Next.js フロントエンド）
- `examples/wordpress`（WordPress + 高パラノイア CRS 設定）
- `examples/api-gateway`（REST API + 厳しめレート制限プロファイル）

共通の起動手順は `examples/README.md` を参照してください。

### FPチューナー（モック）送受信テスト

外部LLMの契約を確定していない段階でも、送信→受信→適用までをテストできます:

```bash
./scripts/test_fp_tuner_mock.sh
```

既定では `simulate` 適用（`SIMULATE=1`）です。実際に追記してホットリロードする場合:

```bash
SIMULATE=0 ./scripts/test_fp_tuner_mock.sh
```

### FPチューナー（HTTPスタブ）送受信テスト

`http` モードをローカルスタブで検証する場合:

```bash
./scripts/test_fp_tuner_http.sh
```

このスクリプトは次を自動実行します:

- `127.0.0.1:${MOCK_PROVIDER_PORT:-18091}` に一時的なプロバイダスタブを起動
- `WAF_FP_TUNER_MODE=http` で `coraza` を起動/再ビルド
- `propose` / `apply` の契約を確認
- 外部送信前にマスキング済みペイロードであることを検証

既定のAPI公開ポートは `HOST_CORAZA_PORT=19090` です（`:80` は使用しません）。

### FPチューナー（コマンドブリッジ）送受信テスト

外部ツール連携（将来的な Codex CLI / Claude Code 連携を含む）向けに、`command` モードのブリッジ検証も可能です:

```bash
./scripts/test_fp_tuner_bridge_command.sh
```

関連スクリプト:

- `scripts/fp_tuner_provider_bridge.py`: ローカルHTTPブリッジ（`/propose`）
- `scripts/fp_tuner_provider_cmd_example.sh`: サンプルのコマンドプロバイダ（stdin JSON -> stdout JSON）
- `scripts/fp_tuner_provider_openai.sh`: OpenAI互換API向けコマンドプロバイダ（stdin JSON -> API呼び出し -> stdout JSON）
- `scripts/fp_tuner_provider_claude.sh`: Claude Messages API向けコマンドプロバイダ（stdin JSON -> API呼び出し -> stdout JSON）

独自コマンドに差し替える場合:

```bash
BRIDGE_COMMAND="/path/to/your-provider-command.sh" ./scripts/test_fp_tuner_bridge_command.sh
```

OpenAIコマンドプロバイダの利用例:

```bash
export FP_TUNER_OPENAI_API_KEY="<your-api-key>"
export FP_TUNER_OPENAI_MODEL="<your-model-name>"

BRIDGE_COMMAND="./scripts/fp_tuner_provider_openai.sh" ./scripts/test_fp_tuner_bridge_command.sh
```

OpenAIコマンドプロバイダのローカルモックテスト:

```bash
./scripts/test_fp_tuner_openai_command.sh
```

Claudeコマンドプロバイダの利用例:

```bash
export FP_TUNER_CLAUDE_API_KEY="<your-api-key>"
export FP_TUNER_CLAUDE_MODEL="claude-sonnet-4-6"

BRIDGE_COMMAND="./scripts/fp_tuner_provider_claude.sh" ./scripts/test_fp_tuner_bridge_command.sh
```

Claudeコマンドプロバイダのローカルモックテスト:

```bash
./scripts/test_fp_tuner_claude_command.sh
```

### FPチューナー（管理UI）運用フロー

管理画面（`/fp-tuner`）で、最近の `waf_block` ログから対象イベントを1件選択して提案生成できます。

基本フロー:

1. 管理UIの `FP Tuner` を開く
2. `Pick From Recent waf_block Logs` で調整対象の行の `Use` を押す
3. 自動反映されたイベント項目（`path` / `rule_id` / `matched_variable` / `matched_value`）を確認
4. `Propose` を実行し、`proposal.rule_line` を必要に応じて編集
5. `Apply` を実行（まず `simulate`、必要なら承認トークン付きで実適用）

1回の提案で送る外部プロバイダ向け入力は選択した1イベントのみです（送信量を抑制）。

---

## API管理エンドポイント（/mamotama-api）

### エンドポイント一覧

| メソッド | パス | 説明 |
| --- | --- | --- |
| GET | `/mamotama-api/status` | 現在のWAF設定状態を取得 |
| GET | `/mamotama-api/logs/read` | WAFログ（tail）を取得（`country` クエリで国別フィルタ可） |
| GET | `/mamotama-api/logs/stats` | WAFブロック統計 + 時間別seriesを取得（`hours` / `scan` クエリ対応） |
| GET | `/mamotama-api/logs/download` | WAFログファイル（`waf`）をダウンロード |
| GET | `/mamotama-api/rules` | ルールファイル一覧を取得（複数対応） |
| POST | `/mamotama-api/rules:validate` | 指定ルールファイルの構文検証（保存なし） |
| PUT | `/mamotama-api/rules` | 指定ルールファイルを保存し、WAFベースルールをホットリロード（`If-Match`対応） |
| GET | `/mamotama-api/crs-rule-sets` | CRS本体ルール一覧と有効/無効状態を取得 |
| POST | `/mamotama-api/crs-rule-sets:validate` | CRS本体ルール選択の検証（保存なし） |
| PUT | `/mamotama-api/crs-rule-sets` | CRS本体ルール選択を保存し、ホットリロード（`If-Match`対応） |
| GET | `/mamotama-api/bypass-rules` | バイパス設定ファイルの内容を取得 |
| POST | `/mamotama-api/bypass-rules:validate` | 送信内容の構文・検証のみ（保存なし） |
| PUT | `/mamotama-api/bypass-rules` | バイパス設定ファイルを上書き保存（`If-Match` に `ETag` を指定して楽観ロック） |
| GET  | `/mamotama-api/country-block-rules` | 国別ブロック設定ファイルの内容を取得 |
| POST | `/mamotama-api/country-block-rules:validate` | 国別ブロック設定の構文検証のみ（保存なし） |
| PUT  | `/mamotama-api/country-block-rules` | 国別ブロック設定ファイルを保存（`If-Match` に `ETag` を指定して楽観ロック） |
| GET  | `/mamotama-api/rate-limit-rules` | レート制限設定ファイルの内容を取得 |
| POST | `/mamotama-api/rate-limit-rules:validate` | レート制限設定の構文検証のみ（保存なし） |
| PUT  | `/mamotama-api/rate-limit-rules` | レート制限設定ファイルを保存（`If-Match` に `ETag` を指定して楽観ロック） |
| GET  | `/mamotama-api/bot-defense-rules` | Bot defense設定ファイルの内容を取得 |
| POST | `/mamotama-api/bot-defense-rules:validate` | Bot defense設定の構文検証のみ（保存なし） |
| PUT  | `/mamotama-api/bot-defense-rules` | Bot defense設定ファイルを保存（`If-Match` に `ETag` を指定して楽観ロック） |
| GET  | `/mamotama-api/semantic-rules` | Semantic設定と実行統計を取得 |
| POST | `/mamotama-api/semantic-rules:validate` | Semantic設定の構文検証のみ（保存なし） |
| PUT  | `/mamotama-api/semantic-rules` | Semantic設定ファイルを保存（`If-Match` に `ETag` を指定して楽観ロック） |
| POST | `/mamotama-api/fp-tuner/propose` | リクエスト入力または最新 `waf_block` ログからFP調整案を生成 |
| POST | `/mamotama-api/fp-tuner/apply` | 調整案の検証/適用（既定は `simulate=true`、実適用は承認トークン必須設定可） |
| GET  | `/mamotama-api/cache-rules` | cache.conf の現在内容（Raw + 構造化）と `ETag` を返す |
| POST | `/mamotama-api/cache-rules:validate` | 送信内容の構文・検証のみ（保存なし） |
| PUT | `/mamotama-api/cache-rules` | cache.conf を保存（`If-Match` に `ETag` を指定して楽観ロック） |


ログやルールが設定されていない場合は `500` で `{"error": "...説明..."}` を返します。

---

## WAFバイパス・特別ルール設定について

mamotamaでは、CorazaによるWAF検査を特定のリクエストに対して除外（バイパス）したり、特定のルールのみを適用する機能を備えています。

### バイパスファイルの指定

環境変数 `WAF_BYPASS_FILE` で除外・特別ルール定義ファイルを指定します。デフォルトは `conf/waf.bypass` です。

### ファイル記述形式

```text
# 通常のバイパス指定
/about/
/about/user.php

# 特別ルール適用（WAFバイパスせず、指定ルールを使用）
/about/admin.php rules/admin-rule.conf

# コメント（先頭 #）
#/should/be/ignored.php rules/test.conf
```

### UIからの編集

管理ダッシュボード `/bypass` 画面から、`waf.bypass` ファイルの内容を直接編集・保存できます。
この画面では、全体の設定内容をテキスト形式で表示・編集し、保存ボタンで即時適用できます。

### 国別ブロック設定

管理ダッシュボード `/country-block` から、`WAF_COUNTRY_BLOCK_FILE`（既定: `conf/country-block.conf`）を編集できます。  
1行に1つの国コードを記述します（例: `JP`, `US`, `UNKNOWN`）。  
該当する国コードのアクセスは WAF 前段で `403` になります。

### レート制限設定

管理ダッシュボード `/rate-limit` から、`WAF_RATE_LIMIT_FILE`（既定: `conf/rate-limit.conf`）を編集できます。  
設定は JSON 形式で、`default_policy` と `rules` を管理します。  
超過時は `action.status`（通常 `429`）を返し、`Retry-After` ヘッダを付与します。

#### JSONパラメータ早見表（何を変えるとどうなるか）

| パラメータ | 例 | 影響 |
| --- | --- | --- |
| `enabled` | `true` / `false` | レート制限全体の有効/無効。`false` なら全リクエストを素通し。 |
| `allowlist_ips` | `["127.0.0.1/32", "10.0.0.5"]` | 一致IPは常に制限対象外。CIDRと単体IPの両方を指定可。 |
| `allowlist_countries` | `["JP", "US"]` | 一致国コードは常に制限対象外。 |
| `default_policy.enabled` | `true` | デフォルトポリシー自体の有効/無効。 |
| `default_policy.limit` | `120` | ウィンドウ期間内の基本許可回数。 |
| `default_policy.burst` | `20` | `limit` に上乗せする瞬間許容量。実効上限は `limit + burst`。 |
| `default_policy.window_seconds` | `60` | カウント窓の秒数。短いほど厳密、長いほど緩やか。 |
| `default_policy.key_by` | `"ip"` | 集計キー。`ip` / `country` / `ip_country`。 |
| `default_policy.action.status` | `429` | 超過時のHTTPステータス。`4xx/5xx`のみ。 |
| `default_policy.action.retry_after_seconds` | `60` | `Retry-After` ヘッダ秒数。`0` なら次ウィンドウまでの残秒を自動計算。 |
| `rules[]` | 下記参照 | 条件一致時に `default_policy` より優先して適用。先頭から順に評価。 |
| `rules[].match_type` | `"prefix"` | ルールの一致方式。`exact` / `prefix` / `regex`。 |
| `rules[].match_value` | `"/login"` | 一致対象。`match_type` に応じて完全一致/前方一致/正規表現。 |
| `rules[].methods` | `["POST"]` | 対象メソッド限定。空なら全メソッド対象。 |
| `rules[].policy.*` |  | ルール一致時に使う制限値（`default_policy` と同じ意味）。 |

#### 運用でよくやる調整

- 全体を一時停止したい: `enabled=false`
- 短時間スパイクに強くしたい: `burst` を増やす
- ログインだけ厳しくしたい: `rules` に `match_type=prefix`, `match_value=/login`, `methods=["POST"]` を追加
- 同一IP内で国別に分けたい: `key_by="ip_country"`
- 特定拠点を除外したい: `allowlist_ips` または `allowlist_countries` に追加

### Bot Defense 設定

管理ダッシュボード `/bot-defense` から、`WAF_BOT_DEFENSE_FILE`（既定: `conf/bot-defense.conf`）を編集できます。  
有効時は、対象パスの GET リクエストに対して（`mode` に応じて）challenge レスポンスを返し、通過後に通常処理へ進みます。

#### JSONパラメータ早見表

| パラメータ | 例 | 影響 |
| --- | --- | --- |
| `enabled` | `true` / `false` | Bot challenge の全体ON/OFF。 |
| `mode` | `"suspicious"` | `suspicious` は UA 条件一致時のみ、`always` は一致パスを常に challenge。 |
| `path_prefixes` | `["/", "/login"]` | challenge 対象のパス前方一致。 |
| `exempt_cidrs` | `["127.0.0.1/32"]` | challenge 除外する送信元 IP/CIDR。 |
| `suspicious_user_agents` | `["curl", "wget"]` | `suspicious` モードで使う UA 部分一致。 |
| `challenge_cookie_name` | `"__mamotama_bot_ok"` | challenge 通過に使う Cookie 名。 |
| `challenge_secret` | `"long-random-secret"` | challenge トークン署名シークレット（空ならプロセス起動ごとに一時生成）。 |
| `challenge_ttl_seconds` | `86400` | challenge トークン有効期限（秒）。 |
| `challenge_status_code` | `429` | challenge 応答時の HTTP ステータス（`4xx/5xx`）。 |

### Semantic Security 設定

管理ダッシュボード `/semantic` から、`WAF_SEMANTIC_FILE`（既定: `conf/semantic.conf`）を編集できます。  
これは機械学習ではなくルールベースのヒューリスティック検知で、`off | log_only | challenge | block` の段階制御に対応します。

#### JSONパラメータ早見表

| パラメータ | 例 | 影響 |
| --- | --- | --- |
| `enabled` | `true` / `false` | semantic スコアリング全体の有効/無効。 |
| `mode` | `"challenge"` | 実行モード。`off` / `log_only` / `challenge` / `block`。 |
| `exempt_path_prefixes` | `["/healthz"]` | 一致パスは semantic 検査をスキップ。 |
| `log_threshold` | `4` | anomaly ログを出す最小スコア。 |
| `challenge_threshold` | `7` | `challenge` モードで challenge 応答にする最小スコア。 |
| `block_threshold` | `9` | `block` モードで `403` にする最小スコア。 |
| `max_inspect_body` | `16384` | semantic が検査するリクエストボディ最大バイト数。 |

### ルールファイル編集（複数対応）

管理ダッシュボード `/rules` では、アクティブなベースルールセットを選択して編集できます（`WAF_RULES_FILE` と、CRS有効時は `crs-setup.conf` + 有効化されている `WAF_CRS_RULES_DIR` の `*.conf`）。  
保存時はサーバ側で構文検証した後に反映され、Coraza のベースルールセットをホットリロードします。  
リロード失敗時は自動でロールバックされます。

### CRSルールセット切替

管理ダッシュボード `/rule-sets` では、`rules/crs/rules/*.conf` の各ファイルを有効/無効で切り替えられます。  
状態は `WAF_CRS_DISABLED_FILE` に保存され、保存時にWAFをホットリロードします。

### 優先順位

* 特別ルールが優先されます（同じパスにバイパス設定があっても無視）
* ルールファイルが存在しない場合

  * `WAF_STRICT_OVERRIDE=true` のときは即時強制終了（log.Fatalf）
  * `false` または未設定時はログ出力して通常ルールで処理継続

### 例

```text
/about/                    # /about/ 以下すべてバイパス
/about/admin.php rules/special.conf  # admin.php だけは WAF で特別ルール適用
```

### 注意

* ルール記述はファイル上で上から順に評価されます
* `extraRuleFile` を指定した行が優先されます
* コメント行（`#`で始まる）は無視されます

---

## ログの確認

本システムのログは API 経由で取得できます。

```bash
curl -s -H "X-API-Key: <your-api-key>" \
     "http://<host>/mamotama-api/logs/read?src=waf&tail=100&country=JP" | jq .
```

* src: ログ種別 (waf)
* tail: 取得件数
* country: 国コード（例: `JP`, `US`, `UNKNOWN`。未指定または`ALL`で全件）
  * Cloudflare配下では `CF-IPCountry` ヘッダを利用します。未取得時は `UNKNOWN` になります。

API キーは .env で設定した API_KEY を使用してください。
実運用環境ではアクセス制限や認証を必ず設定してください。

## キャッシュ機能

キャッシュ対象のパスやTTLを動的に設定できる機能を追加しました。

### 設定ファイル
キャッシュ設定は `/data/conf/cache.conf` に記述します。  
設定変更はホットリロードに対応しており、ファイル保存後すぐに反映されます。

#### 記述例

```bash
# 静的アセット（CSS/JS/画像）を10分キャッシュ
ALLOW prefix=/_next/static/chunks/ methods=GET,HEAD ttl=600 vary=Accept-Encoding

# 特定HTMLページ群を5分キャッシュ（正規表現）
ALLOW regex=^/about/.*.html$ methods=GET ttl=300

# API全域禁止（安全側）
DENY prefix=/mamotama-api/

# 認証ユーザーのプロフィールはキャッシュ禁止（正規表現）
DENY regex=^/users/[0-9]+/profile

# その他はデフォルトでキャッシュ禁止
```

- ALLOW: キャッシュ許可（TTLは秒単位、Varyは任意）
- DENY: キャッシュ対象外
- メソッドは `GET` または `HEAD` を推奨（POST等はキャッシュされません）

フィールド説明
- prefix: 指定パスで始まる場合にマッチ
- regex: 正規表現でマッチ（`^`や`$`を使って指定可能）
- methods: 対象HTTPメソッド（カンマ区切り）
- ttl: キャッシュ時間（秒）
- vary: レスポンスに付与するVaryヘッダ値（カンマ区切り）

### 動作概要

- Go側でルールに一致したレスポンスに `X-Mamotama-Cacheable` と `X-Accel-Expires` を付与
- 必要に応じて外部キャッシュ/CDNがこれらのヘッダを利用可能
- 認証付きリクエスト、Cookieあり、APIパスはデフォルトでキャッシュされません
- `Set-Cookie` を含む上流レスポンスは保存されません（共有キャッシュ誤配信防止）

### 確認方法

- レスポンスヘッダに以下が含まれているか確認
  - `X-Mamotama-Cacheable: 1`
  - `X-Accel-Expires: <秒数>`
- 既定構成には内部HTTPキャッシュ層は含まれません

---

## 管理画面のアクセス制限について

本プロジェクトにはデフォルトでアクセス制限機能は含まれていません。  
管理画面（`/mamotama-ui`）を利用する場合は、必ず Basic 認証や IP 制限などのアクセス制御を設定してください。

---

## 品質ゲート（CI）

GitHub Actions の `ci` ワークフローで以下を検証します。

- `go test ./...`（`coraza/src`）
- `docker compose config` の妥当性確認
- MySQL ログストア統合テスト（`docker compose --profile mysql up -d mysql` + `go test ./internal/handler -run TestLogsStatsMySQLStoreAggregatesAndIngestsIncrementally`）
- Proxy管理スモーク（`./scripts/ci_proxy_admin_smoke.sh`: 埋め込みUI + `proxy-rules` validate/probe/PUT/rollback + ETag競合）
- `./scripts/run_gotestwaf.sh`（`waf-test` マトリクス、`MIN_BLOCKED_RATIO=70`、`WAF_DB_ENABLED=false/true`）

運用では、以下をブランチ保護の Required Checks に設定してください。

- `ci / go-test`
- `ci / mysql-logstore-test`
- `ci / compose-validate`
- `ci / waf-test (file)`
- `ci / waf-test (sqlite)`

---

## 誤検知チューニング運用

誤検知の削減手順は以下を参照してください。

- `docs/operations/waf-tuning.md`
- `docs/operations/fp-tuner-api.md`

## DB運用

SQLite 運用手順は以下を参照してください。

- `docs/operations/db-ops.md`

---

## mamotama とは？

**mamotama** は日本語の **「護りたまえ」 (mamoritamae)** に由来し、
「どうか護ってください」や「護りを与えてください」という意味を持ちます。

この名前には、Webアプリケーションとインフラを守るというプロジェクトの目的を込めています。
