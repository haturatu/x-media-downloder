# syntax=docker/dockerfile:1.7
FROM denoland/deno:latest AS builder

WORKDIR /app
ENV DENO_DIR=/deno-dir

COPY deno.json .
COPY fresh.gen.ts .
COPY . .

# Prime all runtime/dev imports into cache during build.
RUN deno cache -A dev.ts main.ts
RUN deno task build

FROM denoland/deno:latest AS runtime

WORKDIR /app
ENV DENO_DIR=/deno-dir
COPY --from=builder /app /app
COPY --from=builder /deno-dir /deno-dir

EXPOSE 8000

CMD ["deno", "run", "-A", "--unstable", "main.ts"]
