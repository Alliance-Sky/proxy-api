#!/bin/bash
# /home/ubuntu/proxy-api/check-new-month.sh

# Get the current default month from the running proxy-api
CURRENT_KNOWN_MONTH=$(curl -s http://127.0.0.1:9000/api/v3/init | grep -o '"defaultMonth":"[^"]*"' | cut -d'"' -f4)

# Fetch the raw HTML from Smogon and extract the latest month directory
LATEST_SMOGON_MONTH=$(curl -s https://www.smogon.com/stats/ | grep -o 'href="20[0-9][0-9]-[0-1][0-9]/"' | cut -d'"' -f2 | tr -d '/' | sort | tail -n 1)

# Ensure both variables actually fetched successfully to prevent false triggers
if [ "$CURRENT_KNOWN_MONTH" != "" ] && [ "$LATEST_SMOGON_MONTH" != "" ]; then
  if [ "$CURRENT_KNOWN_MONTH" != "$LATEST_SMOGON_MONTH" ]; then
    echo "$(date): New month detected: $LATEST_SMOGON_MONTH (Current: $CURRENT_KNOWN_MONTH). Starting sync..."
    
    # 1. Compile and trigger backend population scripts
    cd /home/ubuntu/proxy-api
    go build -o populate-usage-stats-bin ./cmd/populate-usage-stats
    go build -o populate-format-stats-bin ./cmd/populate-format-stats
    go build -o populate-leads-bin ./cmd/populate-leads
    go build -o populate-metagame-bin ./cmd/populate-metagame
    go build -o populate-viability-bin ./cmd/populate-viability
    
    ./populate-usage-stats-bin >> /home/ubuntu/proxy-api/logs/usage.log 2>&1
    ./populate-format-stats-bin >> /home/ubuntu/proxy-api/logs/format.log 2>&1
    ./populate-leads-bin >> /home/ubuntu/proxy-api/logs/leads.log 2>&1
    ./populate-metagame-bin >> /home/ubuntu/proxy-api/logs/metagame.log 2>&1
    ./populate-viability-bin >> /home/ubuntu/proxy-api/logs/viability.log 2>&1
    
    # 2. Invalidate backend cache
    curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" http://127.0.0.1:9000/_internal/invalidate-months >> /home/ubuntu/proxy-api/logs/invalidate.log 2>&1
    
    # 3. Trigger frontend SSG rebuild on Cloudflare Pages
    if [ -n "$CLOUDFLARE_HOOK_URL" ]; then
      curl -X POST "$CLOUDFLARE_HOOK_URL" >> /home/ubuntu/proxy-api/logs/deploy.log 2>&1
    else
      echo "Error: CLOUDFLARE_HOOK_URL environment variable is not set" >> /home/ubuntu/proxy-api/logs/deploy.log
    fi
    
    echo "$(date): Sync and deployment complete for $LATEST_SMOGON_MONTH"
  fi
fi
