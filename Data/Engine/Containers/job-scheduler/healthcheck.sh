#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

exec /usr/local/bin/borealis-api-backend-go job-scheduler-healthcheck
