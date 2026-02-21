# syntax=docker/dockerfile:1.7
FROM denoland/deno:latest

WORKDIR /app

COPY deno.json .
COPY fresh.gen.ts .
COPY . .

RUN --mount=type=cache,target=/deno-dir deno task build

EXPOSE 8000

CMD ["deno", "run", "-A", "--unstable", "main.ts"]
