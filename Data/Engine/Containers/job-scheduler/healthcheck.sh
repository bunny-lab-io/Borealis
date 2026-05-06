#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

export PYTHONPATH="/opt/Borealis:/opt/Borealis/Data/Engine:/opt/Borealis/Data/Agent:${PYTHONPATH:-}"
exec python -m Data.Engine.services.task_scheduler.healthcheck
