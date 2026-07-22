# Proxy API for smogonstats.eu.cc

A fast, caching reverse proxy built with Fastify to serve Smogon usage stats efficiently. 
Features Brotli compression, strict SSRF protections, and robust caching (RAM & Edge) to absolutely minimize bandwidth.

## Development

To install dependencies and start the local server:
```bash
npm install
node server.js
```

## Deployment

This API is designed to be hosted on a Linux instance and managed using PM2.

```bash
npm install
pm2 start server.js --name "proxy-api"
```

## Credits

- **[Fastify](https://fastify.dev/)**: For the high-performance web framework.
- **[Smogon](https://www.smogon.com/stats/)**: For providing the raw competitive Pokémon usage statistics data.

## License

This project is licensed under the [MIT License](LICENSE).
