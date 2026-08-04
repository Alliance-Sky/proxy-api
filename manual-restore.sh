#!/bin/bash
echo "Triggering manual cache restore..."
curl -X POST -H "X-Admin-Token: super_secret_admin_token" http://127.0.0.1:9000/_internal/restore
echo ""
