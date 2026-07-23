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

function parseStats(text) {
  const lines = text.split('\n');
  const data = [];
  
  let startIndex = 0;
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith(' + ')) continue;
    if (lines[i].includes('Rank') && lines[i].includes('Pokemon')) continue;
    if (lines[i].trim().startsWith('|')) {
      startIndex = i;
      break;
    }
  }

  for (let i = startIndex; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line.startsWith('+') || line === '') continue; 
    
    const parts = line.split('|').map(p => p.trim());
    if (parts.length >= 5) {
      const usagePercent = parts[3];
      if (parseFloat(usagePercent) > 0) {
        data.push({
          pokemon: parts[2],
          usagePercent: parseFloat(usagePercent)
        });
      }
    }
  }
  
  return data;
}

async function run() {
  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  await client.connect();

  console.log("Creating usage_stats table if it doesn't exist...");
  await client.query(`
    CREATE TABLE IF NOT EXISTS usage_stats (
      id SERIAL PRIMARY KEY,
      month VARCHAR(20),
      format VARCHAR(50),
      rating VARCHAR(20),
      pokemon VARCHAR(100),
      usage_percent REAL,
      UNIQUE(month, format, rating, pokemon)
    )
  `);

  console.log("Fetching existing usage records to skip...");
  const res = await client.query('SELECT DISTINCT month, format, rating FROM usage_stats');
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
    INSERT INTO usage_stats (month, format, rating, pokemon, usage_percent) 
    SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::text[], $5::real[])
    ON CONFLICT (month, format, rating, pokemon) DO NOTHING
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
      
      await Promise.all(batch.map(async (file) => {
        const nameWithoutExt = file.replace('.txt', '');
        const lastDash = nameWithoutExt.lastIndexOf('-');
        const format = nameWithoutExt.substring(0, lastDash);
        const rating = nameWithoutExt.substring(lastDash + 1);
        
        const key = `${month}_${format}_${rating}`;
        if (existingKeys.has(key)) return;
        
        console.log(`Fetching ${month} ${format} ${rating}...`);
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
          const data = parseStats(text);
          
          const count = data.length;
          if (count > 0) {
            const monthsArr = Array(count).fill(month);
            const formatsArr = Array(count).fill(format);
            const ratingsArr = Array(count).fill(rating);
            const pokemonsArr = data.map(item => item.pokemon);
            const usagesArr = data.map(item => item.usagePercent);
            
            await client.query(insertQuery, [monthsArr, formatsArr, ratingsArr, pokemonsArr, usagesArr]);
          }
          
          console.log(`Inserted usage stats for ${count} pokemon in ${key}.`);
          existingKeys.add(key);
        } catch (e) {
          console.error(`Error parsing text or inserting for ${targetUrl}:`, e.message);
        }
      }));
    }
  }

  console.log("Finished populating usage stats.");
  await client.end();
}

run().catch(console.error);
