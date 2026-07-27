# Proxy API for smogonstats.eu.cc

A fast, caching reverse proxy and API backend built in **Go** to serve Smogon usage stats efficiently. 
Features strict SSRF protections, graceful shutdown, and robust caching (`BigCache`) to minimize bandwidth and database queries. The backend operates on the optimized v3 API layer to serve highly condensed JSON payloads (stripping expensive string formatting overhead). It also includes the necessary data populator scripts to scrape Smogon stats directly into PostgreSQL.

## Requirements
- Go 1.20+
- PostgreSQL instance running locally on `/var/run/postgresql` with a `smogon_stats` database and `ubuntu` user (or set `DATABASE_URL`).

## Development & Building

The project includes a `Makefile` to easily build the API server and all the data scrapers.

To compile all binaries:
```bash
make build
```

To run the local proxy server:
```bash
make run
```
*Note: The proxy runs on port `9000` by default. This can be overridden with the `PORT` environment variable.*

## Data Population

This repository includes 5 standalone tools to scrape and populate the PostgreSQL database from the raw Smogon text files:

- `populate-usage-stats-bin`
- `populate-format-stats-bin`
- `populate-leads-bin`
- `populate-metagame-bin`
- `populate-viability-bin`

To build and run all populator scripts sequentially:
```bash
make populate
```

## Deployment

This API is designed to be hosted on a Linux instance. To ensure the proxy runs forever and automatically restarts on system boot or crash, a `systemd` service file is provided.

1. Copy the service file to the systemd directory:
```bash
sudo cp proxy-api.service /etc/systemd/system/
```
2. Reload systemd, enable the service to start on boot, and start it:
```bash
sudo systemctl daemon-reload
sudo systemctl enable proxy-api
sudo systemctl start proxy-api
```
3. Check the status or view logs:
```bash
sudo systemctl status proxy-api
sudo journalctl -u proxy-api -f
```

## Credits

- **[Go](https://go.dev/)**: For the high-performance standard library and concurrency primitives.
- **[Smogon](https://www.smogon.com/stats/)**: For providing the raw competitive Pokémon usage statistics data.
- **[pgx](https://github.com/jackc/pgx)**: For the fast PostgreSQL driver.
- **[BigCache](https://github.com/allegro/bigcache)**: For the high-performance concurrent local cache.

## License

This project is licensed under the [MIT License](LICENSE).
