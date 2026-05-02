FROM python:3.12-slim

COPY requirements.txt /app/requirements.txt

COPY validate_spotify_uris.py /app/validate_spotify_uris.py

# .yamllint is copied from root by `make build-config-tester` (single source of truth)
COPY .yamllint /app/.yamllint

WORKDIR /app/

RUN pip install --no-cache-dir yamllint -r requirements.txt