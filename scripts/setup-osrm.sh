#!/usr/bin/env bash
# One-time OSRM setup: download a regional OpenStreetMap extract and run
# the extract → partition → customize preprocessing that osrm-routed needs.
# Output lands in ./osrm-data as region.osrm*, which docker-compose mounts.
#
# Usage:
#   ./scripts/setup-osrm.sh [PBF_URL]
#
# Default is Southern-zone India (covers Bengaluru). Pick another region
# from https://download.geofabrik.de/ and pass its .osm.pbf URL.
set -euo pipefail

PBF_URL="${1:-http://download.geofabrik.de/asia/india/southern-zone-latest.osm.pbf}"
DATA_DIR="$(cd "$(dirname "$0")/.." && pwd)/osrm-data"
PBF_FILE="region.osm.pbf"
BASE="region"

mkdir -p "$DATA_DIR"
cd "$DATA_DIR"

if [[ ! -f "$PBF_FILE" ]]; then
  echo ">> Downloading map extract:"
  echo "   $PBF_URL"
  wget -O "$PBF_FILE" "$PBF_URL"
else
  echo ">> $PBF_FILE already present, skipping download."
fi

echo ">> extract (this is the slow step; needs a few GB of RAM for large regions)"
docker run --rm -t -v "${DATA_DIR}:/data" osrm/osrm-backend \
  osrm-extract -p /opt/car.lua "/data/${PBF_FILE}"

echo ">> partition"
docker run --rm -t -v "${DATA_DIR}:/data" osrm/osrm-backend \
  osrm-partition "/data/${BASE}.osrm"

echo ">> customize"
docker run --rm -t -v "${DATA_DIR}:/data" osrm/osrm-backend \
  osrm-customize "/data/${BASE}.osrm"

echo ""
echo ">> Done. ${DATA_DIR}/${BASE}.osrm is ready."
echo "   Uncomment the osrm service in docker-compose.yml and run: docker compose up"
