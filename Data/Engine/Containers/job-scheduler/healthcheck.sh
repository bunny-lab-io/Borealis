#!/bin/sh
set -eu

exec /usr/local/bin/borealis-api-backend-go job-scheduler-healthcheck
