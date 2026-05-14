# Docker deployment

Single-container deployment. Server uses git as its storage backend, so the
runtime image bundles git and exposes /data as a named volume.

## Quick start

```
cd docker
cp .env.example .env
# edit .env: set CTXHUB_API_KEY to a real secret

docker compose up -d
docker compose logs -f
```

Verify:

```
curl http://localhost:8080/health
# {"status":"ok"}
```

## Behind nginx (recommended for production)

The container only speaks plain HTTP on :8080. Terminate TLS in nginx (or
your cloud LB) and reverse-proxy to localhost:8080.

```nginx
server {
  listen 443 ssl http2;
  server_name ctxhub.yourcompany.com;
  ssl_certificate     /etc/letsencrypt/live/ctxhub.yourcompany.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/ctxhub.yourcompany.com/privkey.pem;

  client_max_body_size 50M;   # large for big page batches

  location / {
    proxy_pass         http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;
  }
}
```

## Data

The named volume `ctxhub_data` holds one git working repository per
`repo_id` (e.g. `/data/chompchat/`, `/data/acme-api/`).

Backup is rsync of that volume:

```
docker run --rm -v ctxhub_data:/src -v $(pwd):/backup alpine \
  tar -czf /backup/ctxhub-backup-$(date +%F).tar.gz -C /src .
```

Or use docker volume inspect to find the host path and back up directly.

## Updating

```
git pull
docker compose build --no-cache
docker compose up -d
```

The `/data` volume persists across rebuilds.
