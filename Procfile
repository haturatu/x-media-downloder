web: FLASK_APP=app.py flask run --host=0.0.0.0 --port=8888 --reload
queue-api: cd queue && go run ./cmd/queue-service --mode=api
queue-worker: cd queue && go run ./cmd/queue-service --mode=worker
