# X Media Downloader

X (旧Twitter) からメディアをダウンロードするためのツールです。

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

### autotaggerによる自動タグ付け

このアプリケーションは、[autotagger](https://github.com/haturatu/autotagger) サービスを利用して、ダウンロードした画像を自動的にタグ付けすることができます。

1.  `autotagger`のリポジトリの指示に従って、サービスをセットアップし、実行します。
2.  `x-media-downloder`ディレクトリに`.env`ファイルを作成します。
3.  `.env`ファイルに以下の行を追加します:
    ```
    AUTOTAGGER=true
    AUTOTAGGER_URL=http://localhost:5000/evaluate
    ```

また、「Autotagger Reload」機能を使用することで、既存のすべてのメディアに対して一括でタグ付けを行うことができます。

### x-status-getによる一括ダウンロード

[x-status-get](https://github.com/haturatu/x-status-get) ブラウザ拡張機能を使用することで、タイムラインから取得したツイートのメディアを一括で保存し、タグ付けすることができます。

1.  お使いのブラウザに`x-status-get`拡張機能をインストールします。
2.  拡張機能を使ってXのタイムラインからツイートデータを収集します。
3.  収集したデータを利用して、このアプリケーションでメディアの一括ダウンロードとタグ付けができます。
