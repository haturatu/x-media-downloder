# X Media Downloader
<img width="1366" height="666" alt="image" src="https://github.com/user-attachments/assets/f7cc12b3-f7bd-499d-859c-92257f61efc8" />

X (旧Twitter) からメディアをダウンロードするためのツールです。

## 最新機能（2026-02時点）

- UI/UXをPC・スマホ両対応に再構成
- Danbooru風のクラシックなダークテーマに調整
- ダウンロードタスクのCeleryステータス表示ページを追加（`/download-status`）
- ユーザ単位削除、画像単位削除を追加
- `Download Media` のショートカットを追加（`Ctrl+Enter` / Macは `Cmd+Enter`）
- 保存先ディレクトリを `MEDIA_ROOT` で変更可能に対応

## セットアップ

1.  リポジトリをクローンします:
    ```bash
    git clone https://github.com/haturatu/x-media-downloder.git
    cd x-media-downloder
    ```
2.  必要なPythonパッケージをインストールします:
    ```bash
    pip install -r requirements.txt
    ```

## 実行

アプリケーションを起動するには、以下のコマンドを実行します。

```bash
honcho start
```

このコマンドは `Procfile` に基づいて、以下の2つのプロセスを同時に起動します。

-   **Webサーバー**: `http://localhost:8888` でアクセス可能なフロントエンドを提供します。
-   **バックグラウンドワーカー**: URLからのメディアダウンロードを非同期で処理します。

これにより、ダウンロード処理が完了するのを待つことなく、すぐに次のダウンロードリクエストを送ることができます。

## 使い方

### ダウンロード

- サイドバーの `Downloader` にURLを貼り付けて `Download Media`
- ショートカット:
  - Windows/Linux: `Ctrl+Enter`
  - macOS: `Cmd+Enter`

### タスクステータス確認

- `Tasks` メニュー、またはサイドバーの `View Celery Status` から確認
- ページ: `/download-status`
- 表示内容:
  - Queue Depth
  - 実行中/完了/失敗タスク数
  - タスクごとの状態、進捗、保存/スキップ件数

### 削除機能

- ユーザ単位削除:
  - `/users` 一覧の `Delete` ボタン
  - `/users/{username}` の `Delete User` ボタン
  - ユーザ配下の画像と関連タグを削除
- 画像単位削除:
  - 画像モーダルの `Delete Image` ボタン
  - 対象画像ファイルと関連タグを削除

### autotaggerによる自動タグ付け

このアプリケーションは、[autotagger](https://github.com/haturatu/autotagger) サービスを利用して、ダウンロードした画像を自動的にタグ付けすることができます。

1.  `autotagger`のリポジトリの指示に従って、サービスをセットアップし、実行します。
2.  `x-media-downloder`ディレクトリに`.env`ファイルを作成します。
3.  `.env`ファイルに以下の行を追加します:
    ```
    AUTOTAGGER=true
    AUTOTAGGER_URL=http://localhost:5000/evaluate
    MEDIA_ROOT=downloaded_images
    ```

### 保存先ディレクトリの変更

`MEDIA_ROOT` で画像保存先ディレクトリを変更できます。

- 例: `MEDIA_ROOT=/data/x-media`
- 未指定時の既定値: `downloaded_images`

Docker利用時は、`MEDIA_ROOT` のパスがコンテナ内で見えるように `volumes` 設定も合わせて変更してください。

また、「Autotagger Reload」機能を使用することで、既存のすべてのメディアに対して一括でタグ付けを行うことができます。

### x-status-getによる一括ダウンロード

[x-status-get](https://github.com/haturatu/x-status-get) ブラウザ拡張機能を使用することで、タイムラインから取得したツイートのメディアを一括で保存し、タグ付けすることができます。

1.  お使いのブラウザに`x-status-get`拡張機能をインストールします。
2.  拡張機能を使ってXのタイムラインからツイートデータを収集します。
3.  収集したデータを利用して、このアプリケーションでメディアの一括ダウンロードとタグ付けができます。

## API（追加/更新）

- `POST /api/download`: ダウンロードタスクをキュー投入
- `GET /api/download`: Celeryタスクの最新ステータス一覧を取得
- `DELETE /api/users`: ユーザ単位削除（body: `{ "username": "..." }`）
- `DELETE /api/images`: 画像単位削除（body: `{ "filepath": "user/tweet/file.jpg" }`）
