#!/usr/bin/env bash
# teardown.sh — Stop or completely remove the Forge ADP platform.
#
# Usage:
#   ./teardown.sh          # Stop all running processes and containers
#   ./teardown.sh --purge  # Stop everything AND remove all data and artifacts

set -euo pipefail

PURGE=false
for arg in "$@"; do
  [[ "$arg" == "--purge" ]] && PURGE=true
done

echo "==> Stopping Go services..."
pkill -f "cmd/orchestrator"       2>/dev/null || true
pkill -f "cmd/registry"           2>/dev/null || true
pkill -f "cmd/policy-engine"      2>/dev/null || true
pkill -f "cmd/adapters"           2>/dev/null || true

echo "==> Stopping Python agent dispatchers..."
pkill -f "forge_agents.dispatcher" 2>/dev/null || true

echo "==> Stopping Docker containers..."
docker-compose -f docker-compose.dev.yml down ${PURGE:+"-v"} 2>/dev/null || true

if [ "$PURGE" = true ]; then
  echo "==> Removing Docker images..."
  docker rmi postgres:16 redis:7-alpine minio/minio 2>/dev/null || true
  docker rmi forge/orchestrator:v0.1.0 forge/agents:v0.1.0 2>/dev/null || true

  echo "==> Cleaning build artifacts..."
  make clean

  echo "==> Removing Python virtual environment..."
  cd pkg/agents && poetry env remove --all 2>/dev/null || true
  cd - > /dev/null

  echo ""
  echo "All data and artifacts removed."
  echo "To delete the project directory: rm -rf $(pwd)"
else
  echo ""
  echo "Platform stopped. Run with --purge to also remove all data and artifacts."
fi
