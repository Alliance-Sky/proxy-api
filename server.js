const fastify = require('fastify')({ logger: true })
const cors = require('@fastify/cors')
const compress = require('@fastify/compress')
const { LRUCache } = require('lru-cache')
const zlib = require('zlib')
const util = require('util')
const fs = require('fs')
const brotliCompress = util.promisify(zlib.brotliCompress)

const movesetCache = new LRUCache({
  maxSize: 3 * 1024 * 1024 * 1024, // 3GB limit
  sizeCalculation: (value, key) => {
    return value.body ? value.body.length + key.length + 1024 : 1024;
  },
  allowStale: false,
});

const dbCache = new LRUCache({
  maxSize: 1.5 * 1024 * 1024 * 1024, // 1.5GB limit
  sizeCalculation: (value, key) => {
    return Buffer.byteLength(JSON.stringify(value)) + key.length + 1024;
  },
  allowStale: false,
});

let totalInboundBytes = 0;
let totalOutboundBytes = 0;
let lastSavedInbound = 0;
let lastSavedOutbound = 0;
const STATS_FILE = 'proxy-api-stats.db';

if (fs.existsSync(STATS_FILE)) {
  try {
    const stats = JSON.parse(fs.readFileSync(STATS_FILE, 'utf8'));
    totalInboundBytes = stats.inboundBytes || 0;
    totalOutboundBytes = stats.outboundBytes || 0;
    lastSavedInbound = totalInboundBytes;
    lastSavedOutbound = totalOutboundBytes;
  } catch (e) {
    console.error('Failed to load stats backup:', e.message);
  }
}

setInterval(() => {
  if (totalInboundBytes !== lastSavedInbound || totalOutboundBytes !== lastSavedOutbound) {
    fs.writeFile(STATS_FILE, JSON.stringify({
      inboundBytes: totalInboundBytes,
      outboundBytes: totalOutboundBytes
    }), (err) => {
      if (!err) {
        lastSavedInbound = totalInboundBytes;
        lastSavedOutbound = totalOutboundBytes;
      }
    });
  }
}, 10 * 60 * 1000);

const rateLimit = require('@fastify/rate-limit')

fastify.register(rateLimit, {
  max: 200,
  timeWindow: '1 minute',
  allowList: ['127.0.0.1', 'localhost'],
  keyGenerator: (req) => req.headers['x-forwarded-for'] || req.ip,
  errorResponseBuilder: (req, context) => ({
    statusCode: 429,
    error: 'Too Many Requests',
    message: 'Rate limit exceeded (max 200 req/min). Bulk automated scraping is disabled.'
  })
})

fastify.register(cors, {
  origin: '*'
})

fastify.register(compress, {
  global: true,
  encodings: ['br', 'gzip', 'deflate'],
  brotliOptions: {
    params: {
      [zlib.constants.BROTLI_PARAM_QUALITY]: 5,
      [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT,
      [zlib.constants.BROTLI_PARAM_LGWIN]: 24
    }
  },
  zlibOptions: {
    level: 5,
  }
})

fastify.addHook('onSend', (request, reply, payload, done) => {
  if (request.query && request.query.url && payload) {
    if (typeof payload === 'string') {
      totalOutboundBytes += Buffer.byteLength(payload);
    } else if (Buffer.isBuffer(payload)) {
      totalOutboundBytes += payload.length;
    } else if (typeof payload.length === 'number') {
      totalOutboundBytes += payload.length;
    }
  }
  done(null, payload);
})

const { Client } = require('pg');

async function restoreCacheFromPg() {
  const client = new Client({ database: 'smogon-stats-backup', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    await client.query(`
      CREATE TABLE IF NOT EXISTS proxy_cache (
        url TEXT PRIMARY KEY,
        status_code INTEGER,
        headers JSONB,
        body BYTEA
      );
    `);
    
    await client.query(`
      CREATE TABLE IF NOT EXISTS db_cache (
        cache_key TEXT PRIMARY KEY,
        body BYTEA
      );
    `);
    
    console.time("restore_cache");
    console.log("Restoring cache from PostgreSQL...");
    const res = await client.query('SELECT url, status_code, headers, body FROM proxy_cache');
    let count = 0;
    for (const row of res.rows) {
      movesetCache.set(row.url, {
        statusCode: row.status_code,
        headers: row.headers,
        body: row.body
      });
      count++;
    }
    console.log(`Successfully restored ${count} moveset items from PostgreSQL backup.`);
    
    const dbRes = await client.query('SELECT cache_key, body FROM db_cache');
    let dbCount = 0;
    for (const row of dbRes.rows) {
      dbCache.set(row.cache_key, row.body);
      dbCount++;
    }
    console.log(`Successfully restored ${dbCount} DB items from PostgreSQL backup.`);
    console.timeEnd("restore_cache");
  } catch (err) {
    console.error("Failed to restore cache from PG:", err.message);
  } finally {
    await client.end().catch(()=>{});
  }
}

async function backupCacheToPg() {
  const client = new Client({ database: 'smogon-stats-backup', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    await client.query('BEGIN');
    await client.query('TRUNCATE TABLE proxy_cache');
    await client.query('TRUNCATE TABLE db_cache');
    
    const count = movesetCache.size;
    if (count > 0) {
      const urlsArr = [];
      const statusesArr = [];
      const headersArr = [];
      const bodiesArr = [];
      
      for (const [url, data] of movesetCache.entries()) {
        urlsArr.push(url);
        statusesArr.push(data.statusCode);
        headersArr.push(data.headers);
        bodiesArr.push(data.body);
      }
      
      const insertQuery = `
        INSERT INTO proxy_cache (url, status_code, headers, body) 
        SELECT * FROM UNNEST($1::text[], $2::integer[], $3::jsonb[], $4::bytea[])
      `;
      
      await client.query(insertQuery, [urlsArr, statusesArr, headersArr, bodiesArr]);
    }
    
    const dbCount = dbCache.size;
    if (dbCount > 0) {
      const keysArr = [];
      const dbBodiesArr = [];
      
      for (const [key, body] of dbCache.entries()) {
        keysArr.push(key);
        dbBodiesArr.push(body);
      }
      
      const insertDbQuery = `
        INSERT INTO db_cache (cache_key, body) 
        SELECT * FROM UNNEST($1::text[], $2::bytea[])
      `;
      
      await client.query(insertDbQuery, [keysArr, dbBodiesArr]);
    }
    
    await client.query('COMMIT');
    console.log(`Backed up ${count} moveset items and ${dbCount} DB items to PostgreSQL.`);
  } catch (err) {
    console.error("Failed to backup cache to PG:", err.message);
    await client.query('ROLLBACK').catch(()=>{});
  } finally {
    await client.end().catch(()=>{});
  }
}

// Automatically backup cache every 7 days to preserve SSD lifespan
setInterval(backupCacheToPg, 7 * 24 * 60 * 60 * 1000);

fastify.post('/_internal/backup', async (req, reply) => {
  if (req.ip !== '127.0.0.1') return reply.status(403).send({error: 'Forbidden'});
  await backupCacheToPg();
  return { success: true, message: 'Manual backup complete' };
});

fastify.post('/_internal/restore', async (req, reply) => {
  if (req.ip !== '127.0.0.1') return reply.status(403).send({error: 'Forbidden'});
  await restoreCacheFromPg();
  return { success: true, message: 'Manual restore complete' };
});


fastify.get('/api/months', async (req, reply) => {
  const cacheKey = 'months_list';
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=3600, s-maxage=3600');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT DISTINCT month FROM usage_stats ORDER BY month DESC');
    const data = result.rows.map(row => row.month);
    await client.end();
    
    const compressed = await brotliCompress(Buffer.from(JSON.stringify(data)), {
      params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 6, [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT }
    });
    dbCache.set(cacheKey, compressed);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=3600, s-maxage=3600');
    return reply.send(compressed);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/api/formats', async (req, reply) => {
  const { month } = req.query;
  if (!month) return reply.status(400).send({ error: 'Missing parameters' });
  
  const cacheKey = `formats_${month}`;
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT DISTINCT format, rating FROM usage_stats WHERE month = $1', [month]);
    
    const formatsMap = {};
    for (const row of result.rows) {
      if (!formatsMap[row.format]) formatsMap[row.format] = [];
      formatsMap[row.format].push(row.rating);
    }
    
    // Sort and keep top 2 ratings like original api.js
    const data = {};
    Object.keys(formatsMap).sort().forEach(format => {
      data[format] = formatsMap[format]
        .sort((a, b) => Number(a) - Number(b))
        .slice(-2);
    });
    
    await client.end();
    
    const compressed = await brotliCompress(Buffer.from(JSON.stringify(data)), {
      params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 6, [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT }
    });
    dbCache.set(cacheKey, compressed);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(compressed);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/api/viability', async (req, reply) => {
  const { month, format, rating } = req.query;
  if (!month || !format || !rating) {
    return reply.status(400).send({ error: 'Missing parameters' });
  }
  
  const cacheKey = `viability_${month}_${format}_${rating}`;
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT pokemon, viability FROM viability_stats WHERE month = $1 AND format = $2 AND rating = $3', [month, format, rating]);
    const data = {};
    for (const row of result.rows) {
      data[row.pokemon] = row.viability;
    }
    await client.end();
    
    const compressed = await brotliCompress(Buffer.from(JSON.stringify(data)), {
      params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 6, [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT }
    });
    dbCache.set(cacheKey, compressed);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(compressed);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/api/usage', async (req, reply) => {
  const { month, format, rating } = req.query;
  if (!month || !format || !rating) {
    return reply.status(400).send({ error: 'Missing parameters' });
  }
  
  const cacheKey = `usage_${month}_${format}_${rating}`;
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT pokemon, usage_percent FROM usage_stats WHERE month = $1 AND format = $2 AND rating = $3 ORDER BY usage_percent DESC', [month, format, rating]);
    const data = [];
    let rank = 1;
    for (const row of result.rows) {
      data.push({ rank: rank++, pokemon: row.pokemon, usagePercent: row.usage_percent + '%' });
    }
    await client.end();
    
    const compressed = await brotliCompress(Buffer.from(JSON.stringify(data)), {
      params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 6, [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT }
    });
    dbCache.set(cacheKey, compressed);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(compressed);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/api/leads', async (req, reply) => {
  const { month, format, rating } = req.query;
  if (!month || !format || !rating) {
    return reply.status(400).send({ error: 'Missing parameters' });
  }
  
  const cacheKey = `leads_${month}_${format}_${rating}`;
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT pokemon, lead_percent FROM leads_stats WHERE month = $1 AND format = $2 AND rating = $3 ORDER BY lead_percent DESC', [month, format, rating]);
    const data = [];
    let rank = 1;
    for (const row of result.rows) {
      data.push({ rank: rank++, pokemon: row.pokemon, leadPercent: row.lead_percent + '%' });
    }
    await client.end();
    
    const compressed = await brotliCompress(Buffer.from(JSON.stringify(data)), {
      params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 6, [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT }
    });
    dbCache.set(cacheKey, compressed);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(compressed);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/api/metagame', async (req, reply) => {
  const { month, format, rating } = req.query;
  if (!month || !format || !rating) {
    return reply.status(400).send({ error: 'Missing parameters' });
  }
  
  const cacheKey = `metagame_${month}_${format}_${rating}`;
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT stalliness, playstyles FROM metagame_stats WHERE month = $1 AND format = $2 AND rating = $3', [month, format, rating]);
    const data = result.rows.length > 0 ? { stalliness: result.rows[0].stalliness, playstyles: result.rows[0].playstyles } : { stalliness: 0, playstyles: {} };
    await client.end();
    
    const compressed = await brotliCompress(Buffer.from(JSON.stringify(data)), {
      params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 6, [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT }
    });
    dbCache.set(cacheKey, compressed);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('content-encoding', 'br');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(compressed);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/api/format-stats', async (req, reply) => {
  const { month, format, rating } = req.query;
  if (!month || !format || !rating) {
    return reply.status(400).send({ error: 'Missing parameters' });
  }
  
  const cacheKey = `format_stats_${month}_${format}_${rating}`;
  if (dbCache.has(cacheKey)) {
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(dbCache.get(cacheKey));
  }

  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  try {
    await client.connect();
    const result = await client.query('SELECT total_battles FROM format_stats WHERE month = $1 AND format = $2 AND rating = $3', [month, format, rating]);
    const data = { totalBattles: result.rows.length > 0 ? result.rows[0].total_battles : 0 };
    await client.end();
    
    const buffer = Buffer.from(JSON.stringify(data));
    dbCache.set(cacheKey, buffer);
    
    reply.header('content-type', 'application/json; charset=utf-8');
    reply.header('cache-control', 'public, max-age=2592000, s-maxage=2592000, immutable');
    return reply.send(buffer);
  } catch (err) {
    req.log.error(err);
    await client.end().catch(()=>{});
    return reply.status(500).send({ error: 'Database error' });
  }
});

fastify.get('/', async (req, reply) => {
  const targetUrl = req.query.url;
  
  if (!targetUrl) {
    return reply.send({
      inboundBytes: totalInboundBytes,
      outboundBytes: totalOutboundBytes,
      movesetCacheItems: movesetCache.size,
      dbCacheItems: dbCache.size
    });
  }

  if (!targetUrl.startsWith('https://www.smogon.com/stats/')) {
    return reply.status(403).send({ error: 'Access denied: You can only proxy Smogon stats URLs.' });
  }

  const isBrowser = req.headers['origin'] || 
                    req.headers['sec-fetch-mode'] || 
                    req.headers['sec-fetch-dest'] || 
                    req.headers['sec-ch-ua'] ||
                    (req.headers['user-agent'] && req.headers['user-agent'].includes('Mozilla'));
                    
  if (!isBrowser) {
    return reply.status(403).send({ error: 'Access denied: This proxy only works in a browser' })
  }

  const cached = movesetCache.get(targetUrl)
  if (cached) {
    req.log.info(`Cache hit for ${targetUrl}`)
    
    for (const [key, value] of Object.entries(cached.headers)) {
      reply.header(key, value)
    }
    reply.status(cached.statusCode)
    return reply.send(cached.body)
  }

  try {
    const response = await fetch(targetUrl, {
      redirect: 'follow',
      headers: {
        'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36'
      }
    })

    const statusCode = response.status;

    const responseBody = await response.arrayBuffer();
    const buffer = Buffer.from(responseBody);
    
    totalInboundBytes += buffer.length;

    const passThroughHeaders = ['content-type', 'etag', 'last-modified']
    const savedHeaders = {}
    
    for (const h of passThroughHeaders) {
      const val = response.headers.get(h)
      if (val) {
        reply.header(h, val)
        savedHeaders[h] = val
      }
    }
    
    if (statusCode >= 200 && statusCode < 300) {
      const isDirectory = targetUrl.endsWith('/');
      if (isDirectory) {
        savedHeaders['cache-control'] = 'public, max-age=60, s-maxage=60';
      } else {
        savedHeaders['cache-control'] = 'public, max-age=31536000, s-maxage=31536000, immutable';
      }
      reply.header('cache-control', savedHeaders['cache-control']);
      
      const compressedBuffer = await brotliCompress(buffer, {
        params: { 
          [zlib.constants.BROTLI_PARAM_QUALITY]: 6,
          [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT,
          [zlib.constants.BROTLI_PARAM_LGWIN]: 24
        }
      });
      
      savedHeaders['content-encoding'] = 'br';
      
      if (!isDirectory && targetUrl.includes('/moveset/')) {
        movesetCache.set(targetUrl, {
          statusCode,
          headers: savedHeaders,
          body: compressedBuffer
        })
      }
      
      reply.status(statusCode)
      reply.header('content-encoding', 'br')
      return reply.send(compressedBuffer)
    }
    
    reply.status(statusCode)
    return reply.send(buffer)
  } catch (err) {
    req.log.error(err)
    return reply.status(500).send({ error: 'Failed to fetch the target URL' })
  }
})

const start = async () => {
  try {
    restoreCacheFromPg().catch(err => fastify.log.error(err));
    await fastify.listen({ port: 9000, host: '0.0.0.0' })
  } catch (err) {
    fastify.log.error(err)
    process.exit(1)
  }
}

start()
