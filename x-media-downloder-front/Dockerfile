# syntax=docker/dockerfile:1.7
FROM denoland/deno:latest AS builder

WORKDIR /app

COPY deno.json .
COPY fresh.gen.ts .
COPY . .

RUN --mount=type=cache,target=/deno-dir deno task build

FROM denoland/deno:latest AS runtime

WORKDIR /app
COPY --from=builder /app /app

EXPOSE 8000

CMD ["deno", "run", "-A", "--unstable", "main.ts"]
