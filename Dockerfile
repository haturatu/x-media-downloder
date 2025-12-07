FROM python:3.9-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends tini

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .
RUN getent group nobody || groupadd nobody
RUN chown -R nobody:nobody /app
USER nobody
ENTRYPOINT ["tini", "--"]

# The command to run the application will be specified in docker-compose.yml
