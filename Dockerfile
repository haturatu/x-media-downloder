# syntax=docker/dockerfile:1.7
FROM python:3.9-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends tini curl

COPY requirements.txt .
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt

COPY . .
ENTRYPOINT ["tini", "--"]

# The command to run the application will be specified in docker-compose.yml
