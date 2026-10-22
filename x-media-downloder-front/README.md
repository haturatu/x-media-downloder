# X Media Downloader frontend

Node.js 26.12.1 (Current) + Express 5 + Vite + Preact のフロントエンドです。
既存の画面をサーバーで HTML に描画し、同じデータでブラウザ側を hydrate します。
Go の Queue API / Worker は引き続き利用します。

## ローカル開発

Node.js 26 と npm 11 以降を利用してください。nvm の場合は `nvm use` で切り替えられます。
リポジトリのルートで Queue API / Worker と Redis を起動してから実行します。

```sh
cd x-media-downloder-front
npm ci
npm run dev
```

既定のアクセス先は `http://localhost:8000` です。Vite がブラウザ側の変更を反映し、
tsx の watch がサーバー側の変更時に再起動します。
ルートの `.env` とフロントエンドの `.env` を読み込みます。
同名の設定は OS の環境変数、フロントエンドの `.env`、ルートの `.env` の順で優先します。

| 環境変数 | 既定値 | 用途 |
| --- | --- | --- |
| `HOST` | `0.0.0.0` | 待ち受けアドレス |
| `PORT` | `8000` | 待ち受けポート |
| `ASYNQ_API_BASE_URL` | `http://localhost:8001` | API 転送・WebSocket 状態取得先 |
| `API_BASE_URL` | `ASYNQ_API_BASE_URL` または `http://localhost:8001` | SSR のデータ取得先 |
| `MEDIA_ROOT` | `../downloaded_images` | 画像ディレクトリ（相対パスは起動ディレクトリ基準） |

ブラウザの API 呼び出しには同一オリジンの `/api/*` を使い、環境変数をブラウザへ公開しません。

## 本番実行・検証

```sh
npm ci
npm test       # 型チェック、クライアント/サーバービルド、HTTP/WebSocket 結合テスト
npm start     # ビルド済み成果物を起動
```

`npm run check` は型チェックのみ、`npm run build` は型チェックと本番ビルドです。
`npm start` は開発用の Vite / tsx をロードせずに実行できます。
依存関係を変更した場合は `package-lock.json` もコミットしてください。

Docker Compose は本番イメージ内のビルド成果物を使います。
フロントエンドのソースコードを `/app` にマウントしないため、変更後は再ビルドしてください。

```sh
# リポジトリのルートから実行
docker compose up -d --build frontend nginx
```

コンテナは非 root ユーザーで動作し、画像ディレクトリを読み取り専用でマウントします。
画像には Node ユーザーが読み取れるパーミッションが必要です。
画像配信は ETag / Last-Modified、HEAD、Range に対応し、MEDIA_ROOT 外のファイルや、外部を参照する symlink は配信しません。
`/api/ws/status` の URL と状態通知形式は従来と共通です。
終了シグナルで HTTP / WebSocket を閉じ、異常起動は stderr と終了コード 1 で通知します。
DEBUG / INFO は stdout、WARN / ERROR は stderr に出力します。

## 構成の根拠

- [Node.js Release Working Group](https://github.com/nodejs/Release): 最新の Current リリース（26.12.1）を使用し、Docker・CI・ローカル開発のバージョンを統一。
- [Vite SSR](https://vite.dev/guide/ssr) / [Preact SSR](https://preactjs.com/guide/v10/server-side-rendering/): 既存 Preact UI を再利用し、開発用 middleware と本番ビルドを分離。
- [npm ci](https://docs.npmjs.com/cli/v11/commands/npm-ci/): ロックファイルから再現可能なインストールを実施。
- [Docker Node.js guide](https://docs.docker.com/guides/nodejs/): ビルドと本番依存を分離し、非 root ユーザーで実行。
- [Express production practices](https://expressjs.com/en/advanced/best-practice-performance.html): 本番モードとリバースプロキシを利用。
- [Vercel Web Interface Guidelines](https://vercel.com/design/guidelines): URL の検索条件を SSR と hydration で共有。
