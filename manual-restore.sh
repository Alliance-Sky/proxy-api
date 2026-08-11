#!/bin/bash
echo "Triggering manual cache restore..."
PORT=${PORT:-9000}
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" http://127.0.0.1:$PORT/_internal/restore
echo ""
