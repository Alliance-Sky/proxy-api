const fastify = require('fastify')({ logger: true })
const cors = require('@fastify/cors')
const compress = require('@fastify/compress')
const { LRUCache } = require('lru-cache')
const zlib = require('zlib')
const util = require('util')
const fs = require('fs')
const brotliCompress = util.promisify(zlib.brotliCompress)

const cache = new LRUCache({
  maxSize: 4 * 1024 * 1024 * 1024, // 4GB limit
  sizeCalculation: (value, key) => {
    return value.body ? value.body.length + key.length + 1024 : 1024;
  },
  allowStale: false,
})

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

fastify.register(cors, {
  origin: ['https://smogonstats.eu.cc', 'https://www.smogonstats.eu.cc', 'http://localhost:5173']
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

fastify.post('/_internal/backup', async (req, reply) => {
  if (req.ip !== '127.0.0.1') return reply.status(403).send({error: 'Forbidden'});
  try {
    const fd = fs.openSync('cache_backup.bin', 'w');
    let count = 0;
    for (const [url, data] of cache.entries()) {
      const urlBuf = Buffer.from(url);
      const headersBuf = Buffer.from(JSON.stringify(data.headers));
      
      const meta = Buffer.alloc(14);
      meta.writeUInt32LE(urlBuf.length, 0);
      meta.writeUInt16LE(data.statusCode, 4);
      meta.writeUInt32LE(headersBuf.length, 6);
      meta.writeUInt32LE(data.body.length, 10);
      
      fs.writeSync(fd, meta);
      fs.writeSync(fd, urlBuf);
      fs.writeSync(fd, headersBuf);
      fs.writeSync(fd, data.body);
      count++;
    }
    fs.closeSync(fd);
    return { success: true, count };
  } catch (err) {
    req.log.error(err);
    return reply.status(500).send({ error: err.message });
  }
});

fastify.post('/_internal/restore', async (req, reply) => {
  if (req.ip !== '127.0.0.1') return reply.status(403).send({error: 'Forbidden'});
  try {
    if (!fs.existsSync('cache_backup.bin')) {
      return reply.status(404).send({ error: 'Backup file not found' });
    }
    const fd = fs.openSync('cache_backup.bin', 'r');
    let count = 0;
    const meta = Buffer.alloc(14);
    while (true) {
      const bytesRead = fs.readSync(fd, meta, 0, 14, null);
      if (bytesRead === 0) break;
      
      const urlLen = meta.readUInt32LE(0);
      const statusCode = meta.readUInt16LE(4);
      const headersLen = meta.readUInt32LE(6);
      const bodyLen = meta.readUInt32LE(10);
      
      const dataBuf = Buffer.alloc(urlLen + headersLen + bodyLen);
      fs.readSync(fd, dataBuf, 0, dataBuf.length, null);
      
      let offset = 0;
      const url = dataBuf.toString('utf8', offset, offset + urlLen);
      offset += urlLen;
      
      const headers = JSON.parse(dataBuf.toString('utf8', offset, offset + headersLen));
      offset += headersLen;
      
      const body = Buffer.from(dataBuf.subarray(offset, offset + bodyLen));
      
      cache.set(url, { statusCode, headers, body });
      count++;
    }
    fs.closeSync(fd);
    return { success: true, count };
  } catch (err) {
    req.log.error(err);
    return reply.status(500).send({ error: err.message });
  }
});

fastify.get('/', async (req, reply) => {
  const targetUrl = req.query.url;
  
  if (!targetUrl) {
    return reply.send({
      inboundBytes: totalInboundBytes,
      outboundBytes: totalOutboundBytes,
      cacheItems: cache.size
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

  const cached = cache.get(targetUrl)
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
          [zlib.constants.BROTLI_PARAM_QUALITY]: 5,
          [zlib.constants.BROTLI_PARAM_MODE]: zlib.constants.BROTLI_MODE_TEXT,
          [zlib.constants.BROTLI_PARAM_LGWIN]: 24
        }
      });
      
      savedHeaders['content-encoding'] = 'br';
      
      if (!isDirectory) {
        cache.set(targetUrl, {
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
    await fastify.listen({ port: 9000, host: '0.0.0.0' })
  } catch (err) {
    fastify.log.error(err)
    process.exit(1)
  }
}

start()
