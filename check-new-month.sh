#!/bin/bash
# /home/ubuntu/proxy-api/check-new-month.sh

set -eo pipefail

# Resolve the absolute directory of the script dynamically
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &> /dev/null && pwd)"
mkdir -p "$PROJECT_DIR/logs"

# Source environment variables if .env exists
if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  source "$PROJECT_DIR/.env"
  set +a
fi

PORT=${PORT:-9000}

# 1. Get current default month from running proxy-api, with database fallback
CURRENT_KNOWN_MONTH=$(curl -s --connect-timeout 5 "http://127.0.0.1:$PORT/api/v3/init" | grep -o '"defaultMonth":"[^"]*"' | cut -d'"' -f4 || true)
if [ -z "$CURRENT_KNOWN_MONTH" ]; then
  CURRENT_KNOWN_MONTH=$(psql -U ubuntu -d smogon_stats -t -c "SELECT month FROM usage_stats ORDER BY month DESC LIMIT 1;" 2>/dev/null | xargs || true)
fi

# 2. Fetch the raw HTML from Smogon and extract the latest month directory
SMOGON_ROOT_HTML=$(curl -s --connect-timeout 10 "https://www.smogon.com/stats/" || true)
LATEST_SMOGON_MONTH=$(echo "$SMOGON_ROOT_HTML" | grep -o 'href="20[0-9][0-9]-[0-1][0-9]/"' | cut -d'"' -f2 | tr -d '/' | sort -V | tail -n 1 || true)

# 3. Ensure both variables fetched successfully
if [ -n "$CURRENT_KNOWN_MONTH" ] && [ -n "$LATEST_SMOGON_MONTH" ]; then
  if [ "$CURRENT_KNOWN_MONTH" != "$LATEST_SMOGON_MONTH" ]; then
    # Verify that the month directory on Smogon actually contains uploaded files before starting
    MONTH_DIR_HTML=$(curl -s --connect-timeout 10 "https://www.smogon.com/stats/$LATEST_SMOGON_MONTH/" || true)
    FILE_COUNT=$(echo "$MONTH_DIR_HTML" | grep -cE 'href="[a-zA-Z0-9_-]+\.(txt|txt\.gz)"' || true)

    if [ "$FILE_COUNT" -lt 10 ]; then
      echo "$(date): New month directory $LATEST_SMOGON_MONTH detected, but only found $FILE_COUNT files. Waiting for upload to complete..." >> "$PROJECT_DIR/logs/sync.log"
      exit 0
    fi

    echo "$(date): New month detected: $LATEST_SMOGON_MONTH with $FILE_COUNT files (Current: $CURRENT_KNOWN_MONTH). Starting sync..." >> "$PROJECT_DIR/logs/sync.log"

    # Step A: Compile backend population & warmup binaries
    cd "$PROJECT_DIR"
    go build -o populate-all-bin ./cmd/populate-all
    go build -o warmup-bin ./cmd/warmup

    # Step B: Run data population scripts
    echo "$(date): Populating usage stats..." >> "$PROJECT_DIR/logs/sync.log"
    ./populate-all-bin usage >> "$PROJECT_DIR/logs/usage.log" 2>&1
    echo "$(date): Populating format stats..." >> "$PROJECT_DIR/logs/sync.log"
    ./populate-all-bin format-stats >> "$PROJECT_DIR/logs/format.log" 2>&1
    echo "$(date): Populating leads stats..." >> "$PROJECT_DIR/logs/sync.log"
    ./populate-all-bin leads >> "$PROJECT_DIR/logs/leads.log" 2>&1
    echo "$(date): Populating metagame stats..." >> "$PROJECT_DIR/logs/sync.log"
    ./populate-all-bin metagame >> "$PROJECT_DIR/logs/metagame.log" 2>&1
    echo "$(date): Populating viability stats..." >> "$PROJECT_DIR/logs/sync.log"
    ./populate-all-bin viability >> "$PROJECT_DIR/logs/viability.log" 2>&1

    # Step C: Invalidate backend cache
    INV_STATUS=$(curl -s -o "$PROJECT_DIR/logs/invalidate.log" -w "%{http_code}" -X POST -H "X-Admin-Token: $ADMIN_TOKEN" "http://127.0.0.1:$PORT/_internal/invalidate-months" || true)
    if [ "$INV_STATUS" != "200" ]; then
      echo "$(date): Warning: Cache invalidation returned HTTP $INV_STATUS. Restarting proxy-api..." >> "$PROJECT_DIR/logs/sync.log"
      sudo systemctl restart proxy-api || true
      sleep 2
    else
      echo "$(date): Cache invalidation successful." >> "$PROJECT_DIR/logs/sync.log"
    fi

    # Step D: Warm up cache on localhost
    echo "$(date): Warming up cache..." >> "$PROJECT_DIR/logs/sync.log"
    ./warmup-bin >> "$PROJECT_DIR/logs/warmup.log" 2>&1 || true

    # Step E: Build frontend SSG locally ("on here")
    FRONTEND_DIR="/home/ubuntu/smogon-stats"
    if [ -d "$FRONTEND_DIR" ]; then
      echo "$(date): Rebuilding frontend SSG locally..." >> "$PROJECT_DIR/logs/sync.log"
      (cd "$FRONTEND_DIR" && npm run build) >> "$PROJECT_DIR/logs/frontend-build.log" 2>&1 || echo "$(date): Warning: Local frontend build failed." >> "$PROJECT_DIR/logs/sync.log"
    fi

    # Step F: Trigger frontend rebuild on Cloudflare
    if [ -n "$CLOUDFLARE_HOOK_URL" ]; then
      echo "$(date): Triggering Cloudflare deployment hook..." >> "$PROJECT_DIR/logs/sync.log"
      curl -s -X POST "$CLOUDFLARE_HOOK_URL" >> "$PROJECT_DIR/logs/deploy.log" 2>&1
    else
      echo "Error: CLOUDFLARE_HOOK_URL environment variable is not set" >> "$PROJECT_DIR/logs/deploy.log"
    fi

    echo "$(date): Sync and deployment complete for $LATEST_SMOGON_MONTH" >> "$PROJECT_DIR/logs/sync.log"
  fi
fi
