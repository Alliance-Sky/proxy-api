# proxy-api

A Go backend and reverse proxy for [smogonstats.eu.cc](https://smogonstats.eu.cc).

It serves Smogon usage stats endpoints, caches queries using BigCache, stores data in PostgreSQL, and generates dynamic sitemaps.

---

## Overview and Architecture

- API Engine: Built in Go using Chi router and pgx PostgreSQL driver.
- Data Storage: PostgreSQL database (`smogon_stats`) holding usage, leads, format stats, metagame, and viability data.
- Caching: In-memory caching with BigCache to reduce database load.
- Endpoints: Exposes v3 API routes (`/api/v3/init`, `/api/v3/stats`, `/api/v3/details`, `/api/v3/trend`, `/api/v3/format-stats`, `/api/v3/metagame`), dynamic `/sitemap.xml`, and reverse-proxy capabilities.
- Scrapers and Populators: Go binaries that fetch and parse raw text files from Smogon directly into PostgreSQL.

---

## Requirements

- Go 1.20 or higher
- PostgreSQL running locally or via `DATABASE_URL`
- `ADMIN_TOKEN` environment variable for securing internal endpoints and bypassing rate limits
- `CLOUDFLARE_HOOK_URL` environment variable for triggering frontend deployments

---

## Development and Building

Compile all 8 binaries (API server, cache tools, and scrapers):

```bash
make build
```

Run the API server locally:

```bash
make run
```

The server listens on port `9000` by default. Override it with the `PORT` environment variable.
The cache state is automatically backed up to `cache-backup.bin` every 24 hours.

---

## Data Population and Utilities

Build and run all populator scripts to scrape and import Smogon statistics into PostgreSQL:

```bash
make populate
```

Included binaries:
- `populate-usage-stats-bin`
- `populate-format-stats-bin`
- `populate-leads-bin`
- `populate-metagame-bin`
- `populate-viability-bin`
- `preload-dbcache-bin` (Hydrates cache directly from database)
- `warmup-bin` (Warms cache via concurrent HTTP requests)

---

## Deployment (systemd)

To run the proxy continuously on Linux:

```bash
sudo cp proxy-api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable proxy-api
sudo systemctl start proxy-api
```

Check service status or logs:

```bash
sudo systemctl status proxy-api
sudo journalctl -u proxy-api -f
```

---

## Credits

- Go: Standard library and concurrency primitives.
- Smogon: Raw competitive Pokémon usage statistics.
- pgx: PostgreSQL driver.
- BigCache: Concurrent in-memory cache.

## License

This project is licensed under the [MIT License](LICENSE).
