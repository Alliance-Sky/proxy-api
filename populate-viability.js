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

async function run() {
  const client = new Client({ database: 'smogon_stats', host: '/var/run/postgresql', user: 'ubuntu' });
  await client.connect();

  console.log("Fetching existing records from DB to skip...");
  const res = await client.query('SELECT DISTINCT month, format, rating FROM viability_stats');
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
    INSERT INTO viability_stats (month, format, rating, pokemon, viability) 
    SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::text[], $5::jsonb[])
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
    let chaosHtml;
    try {
      chaosHtml = await fetchHtml(`${BASE_URL}${month}/chaos/`);
    } catch (e) {
      console.log(`Failed to fetch chaos directory for ${month}, skipping.`);
      continue;
    }
    
    const chaosFiles = parseLinks(chaosHtml).filter(f => f.endsWith('.json'));
    
    const formatRatings = new Map();
    chaosFiles.forEach(file => {
      const nameWithoutExt = file.replace('.json', '');
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
        targetFiles.push(`${format}-${rating}.json`);
      });
    });
    
    const CONCURRENCY = 15;
    for (let i = 0; i < targetFiles.length; i += CONCURRENCY) {
      const batch = targetFiles.slice(i, i + CONCURRENCY);
      
      await Promise.all(batch.map(async (file) => {
        const nameWithoutExt = file.replace('.json', '');
        const lastDash = nameWithoutExt.lastIndexOf('-');
        const format = nameWithoutExt.substring(0, lastDash);
        const rating = nameWithoutExt.substring(lastDash + 1);
        
        const key = `${month}_${format}_${rating}`;
        if (existingKeys.has(key)) return;
        
        console.log(`Fetching ${month} ${format} ${rating}...`);
        const targetUrl = `${BASE_URL}${month}/chaos/${file}`;
        
        let retries = 3;
        let jsonRes;
        while (retries > 0) {
          try {
            jsonRes = await fetch(targetUrl);
            if (jsonRes.ok) break;
          } catch (e) {}
          retries--;
          await new Promise(r => setTimeout(r, 1000));
        }
        
        if (!jsonRes || !jsonRes.ok) {
          console.error(`Failed to fetch JSON: ${targetUrl}`);
          return;
        }
        
        try {
          const text = await jsonRes.text();
          const data = JSON.parse(text);
          
          const validPokemon = Object.keys(data.data || {}).filter(p => data.data[p]['Viability Ceiling']);
          const count = validPokemon.length;
          
          if (count > 0) {
            const monthsArr = Array(count).fill(month);
            const formatsArr = Array(count).fill(format);
            const ratingsArr = Array(count).fill(rating);
            const viabilitiesArr = validPokemon.map(p => JSON.stringify(data.data[p]['Viability Ceiling']));
            
            await client.query(insertQuery, [monthsArr, formatsArr, ratingsArr, validPokemon, viabilitiesArr]);
          }
          
          console.log(`Inserted viability for ${count} pokemon in ${key}.`);
          existingKeys.add(key);
        } catch (e) {
          console.error(`Error parsing JSON or inserting for ${targetUrl}:`, e.message);
        }
      }));
    }
  }

  console.log("Finished populating viability stats.");
  await client.end();
}

run().catch(console.error);
