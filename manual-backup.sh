#!/bin/bash
echo "Triggering manual cache backup..."
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" http://127.0.0.1:9000/_internal/backup
echo ""
