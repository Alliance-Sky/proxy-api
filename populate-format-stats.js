const { Client } = require('pg');
const { fetch } = require('undici');

const BASE_URL = 'https://www.smogon.com/stats/';

async function fetchHtml(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`Failed to fetch ${url}`);
  return await res.text();
}

function parseLinks(html) {
  const links = [];
  const regex = /<a href="([^"]+)">/g;
  let match;
  while ((match = regex.exec(html)) !== null) {
    let href = match[1];
    if (href !== '../' && href !== '/') {
      links.push(href.replace(/\/$/, ''));
    }
  }
  return links;
}

function parseTotalBattles(text) {
  const match = text.match(/Total battles:\s*(\d+)/i);
  return match ? parseInt(match[1], 10) : 0;
}

async function run() {
  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  await client.connect();

  console.log("Creating format_stats table if it doesn't exist...");
  await client.query(`
    CREATE TABLE IF NOT EXISTS format_stats (
      id SERIAL PRIMARY KEY,
      month VARCHAR(20),
      format VARCHAR(100),
      rating VARCHAR(20),
      total_battles INTEGER,
      UNIQUE(month, format, rating)
    )
  `);

  console.log("Fetching existing format_stats records to skip...");
  const res = await client.query('SELECT DISTINCT month, format, rating FROM format_stats');
  const existingKeys = new Set(res.rows.map(r => `${r.month}_${r.format}_${r.rating}`));

  console.log("Fetching months from Smogon...");
  const rootHtml = await fetchHtml(BASE_URL);
  const months = parseLinks(rootHtml)
    .filter(f => {
      const match = f.match(/^(\d{4})-\d{2}$/);
      if (!match) return false;
      const year = parseInt(match[1], 10);
      return year >= 2014 && year <= 2026;
    })
    .reverse();
  
  const insertQuery = `
    INSERT INTO format_stats (month, format, rating, total_battles) 
    SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::integer[])
    ON CONFLICT (month, format, rating) DO NOTHING
  `;

  let currentYear = null;

  for (const month of months) {
    const year = month.substring(0, 4);
    if (currentYear && currentYear !== year) {
      console.log(`Finished processing year ${currentYear}. Waiting for 10 seconds...`);
      await new Promise(r => setTimeout(r, 10000));
    }
    currentYear = year;

    console.log(`\nProcessing month: ${month}`);
    let monthHtml;
    try {
      monthHtml = await fetchHtml(`${BASE_URL}${month}/`);
    } catch (e) {
      console.log(`Failed to fetch root directory for ${month}, skipping.`);
      continue;
    }
    
    const txtFiles = parseLinks(monthHtml).filter(f => f.endsWith('.txt'));
    
    const formatRatings = new Map();
    txtFiles.forEach(file => {
      const nameWithoutExt = file.replace('.txt', '');
      const lastDash = nameWithoutExt.lastIndexOf('-');
      if (lastDash === -1) return;
      const format = nameWithoutExt.substring(0, lastDash);
      const rating = nameWithoutExt.substring(lastDash + 1);
      
      if (!formatRatings.has(format)) formatRatings.set(format, []);
      formatRatings.get(format).push(rating);
    });
    
    const targetFiles = [];
    formatRatings.forEach((ratings, format) => {
      const topRatings = ratings.sort((a, b) => Number(a) - Number(b)).slice(-2);
      topRatings.forEach(rating => {
        targetFiles.push(`${format}-${rating}.txt`);
      });
    });
    
    const CONCURRENCY = 15;
    for (let i = 0; i < targetFiles.length; i += CONCURRENCY) {
      const batch = targetFiles.slice(i, i + CONCURRENCY);
      
      const insertData = { months: [], formats: [], ratings: [], battles: [] };
      
      await Promise.all(batch.map(async (file) => {
        const nameWithoutExt = file.replace('.txt', '');
        const lastDash = nameWithoutExt.lastIndexOf('-');
        const format = nameWithoutExt.substring(0, lastDash);
        const rating = nameWithoutExt.substring(lastDash + 1);
        
        const key = `${month}_${format}_${rating}`;
        if (existingKeys.has(key)) return;
        
        console.log(`Fetching battles for ${month} ${format} ${rating}...`);
        const targetUrl = `${BASE_URL}${month}/${file}`;
        
        let retries = 3;
        let textRes;
        while (retries > 0) {
          try {
            textRes = await fetch(targetUrl);
            if (textRes.ok) break;
          } catch (e) {}
          retries--;
          await new Promise(r => setTimeout(r, 1000));
        }
        
        if (!textRes || !textRes.ok) {
          console.error(`Failed to fetch text: ${targetUrl}`);
          return;
        }
        
        try {
          const text = await textRes.text();
          const totalBattles = parseTotalBattles(text);
          
          if (totalBattles > 0) {
            insertData.months.push(month);
            insertData.formats.push(format);
            insertData.ratings.push(rating);
            insertData.battles.push(totalBattles);
          }
          existingKeys.add(key);
        } catch (e) {
          console.error(`Error parsing text for ${targetUrl}:`, e.message);
        }
      }));
      
      if (insertData.months.length > 0) {
        await client.query(insertQuery, [
          insertData.months, 
          insertData.formats, 
          insertData.ratings, 
          insertData.battles
        ]);
        console.log(`Inserted ${insertData.months.length} format stats.`);
      }
    }
  }

  console.log("Finished populating format stats.");
  await client.end();
}

run().catch(console.error);
