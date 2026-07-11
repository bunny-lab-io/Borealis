#!/bin/sh
set -eu

exec /usr/local/bin/borealis-api-backend-go site-worker-orchestrator-healthcheck
