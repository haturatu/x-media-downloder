import { Head } from "../utils/runtime.tsx";

export default function Error404() {
  return (
    <>
      <Head><title>404 - Page not found</title></Head>
      <div class="page-panel">
        <h2 class="page-title">404 - Page not found</h2>
        <p>The page you were looking for doesn’t exist.</p>
        <a href="/">Go back home</a>
      </div>
    </>
  );
}
