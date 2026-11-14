#!/usr/bin/env bash
# Starts the MongoDB 8.0 replica set used by the live adapter tests.
set -euo pipefail

cd "$(dirname "$0")"

docker compose up -d

ready() {
    docker compose exec -T mongo mongosh --quiet --port 57017 \
        --eval 'db.adminCommand({ping: 1}).ok' 2>/dev/null | grep -qx '1'
}

log() {
    printf '%s\n' "$*" >&2
}

for _ in $(seq 1 60); do
    if ready; then
        break
    fi
    sleep 1
done

if ! ready; then
    log "mongod did not start"
    docker compose logs --tail 40 mongo >&2
    exit 1
fi

docker compose exec -T mongo mongosh --quiet --port 57017 --eval '
try {
  rs.status();
} catch (e) {
  rs.initiate({_id: "rs0", members: [{_id: 0, host: "127.0.0.1:57017"}]});
}
'

for _ in $(seq 1 60); do
    if docker compose exec -T mongo mongosh --quiet --port 57017 \
        --eval 'db.hello().isWritablePrimary' 2>/dev/null | grep -qx 'true'; then
        log "mongo replica set rs0 is ready on 127.0.0.1:57017"
        exit 0
    fi
    sleep 1
done

log "replica set did not elect a primary"
docker compose logs --tail 60 mongo >&2
exit 1
