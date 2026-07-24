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

function parseMetagame(text) {
  const lines = text.split('\n');
  const playstyles = {};
  let stalliness = 0;
  
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed.includes('Stalliness (mean:')) {
      const match = trimmed.match(/Stalliness \(mean:\s*([-\d.]+)\)/);
      if (match) stalliness = parseFloat(match[1]);
    } else if (trimmed.includes('%') && trimmed.includes('.')) {
      const playstyleMatch = trimmed.match(/^([a-z]+)\.+([0-9.]+)%$/);
      if (playstyleMatch) {
        playstyles[playstyleMatch[1]] = parseFloat(playstyleMatch[2]);
      }
    }
  }
  
  return { stalliness, playstyles };
}

async function run() {
  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  await client.connect();

  console.log("Fetching existing metagame records to skip...");
  const res = await client.query('SELECT month, format, rating FROM metagame_stats');
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
    INSERT INTO metagame_stats (month, format, rating, stalliness, playstyles) 
    SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::float[], $5::jsonb[])
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
      monthHtml = await fetchHtml(`${BASE_URL}${month}/metagame/`);
    } catch (e) {
      console.log(`Failed to fetch metagame directory for ${month}, skipping.`);
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
      
      const monthArr = [];
      const formatArr = [];
      const ratingArr = [];
      const stallinessArr = [];
      const playstylesArr = [];
      
      await Promise.all(batch.map(async (file) => {
        const nameWithoutExt = file.replace('.txt', '');
        const lastDash = nameWithoutExt.lastIndexOf('-');
        const format = nameWithoutExt.substring(0, lastDash);
        const rating = nameWithoutExt.substring(lastDash + 1);
        
        const key = `${month}_${format}_${rating}`;
        if (existingKeys.has(key)) return;
        
        console.log(`Fetching ${month} ${format} ${rating}...`);
        try {
          const txt = await fetchHtml(`${BASE_URL}${month}/metagame/${file}`);
          const parsed = parseMetagame(txt);
          if (Object.keys(parsed.playstyles).length > 0) {
            monthArr.push(month);
            formatArr.push(format);
            ratingArr.push(rating);
            stallinessArr.push(parsed.stalliness);
            playstylesArr.push(JSON.stringify(parsed.playstyles));
          }
        } catch (e) {
          console.error(`Error processing ${file}: ${e.message}`);
        }
      }));
      
      if (monthArr.length > 0) {
        try {
          await client.query(insertQuery, [monthArr, formatArr, ratingArr, stallinessArr, playstylesArr]);
          console.log(`Inserted metagame stats for ${monthArr.length} formats.`);
        } catch (e) {
          console.error(`DB Insert Error: ${e.message}`);
        }
      }
    }
  }

  console.log('Finished populating metagame stats.');
  await client.end();
}

run().catch(console.error);
