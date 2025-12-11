import { type PageProps } from "$fresh/server.ts";
import Layout from "../islands/Layout.tsx";

export default function App({ Component, route }: PageProps) {
  return (
    <html>
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>X Media Downloader</title>
        <link rel="stylesheet" href="/styles.css" />
      </head>
      <body>
        <Layout route={route}>
          <Component />
        </Layout>
      </body>
    </html>
  );
}
