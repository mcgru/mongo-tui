#!/usr/bin/env bash
# Starts the auth+TLS MongoDB standalone used by the post-M6 hardening tests.
set -euo pipefail

cd "$(dirname "$0")"

certs="certs"
mkdir -p "$certs"

if [[ ! -f "$certs/ca.pem" || ! -f "$certs/server.pem" ]]; then
    openssl req -x509 -newkey rsa:2048 -nodes \
        -keyout "$certs/ca.key" -out "$certs/ca.pem" -days 1 \
        -subj "/CN=mongo-tui-test-ca" \
        -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1

    openssl req -new -newkey rsa:2048 -nodes \
        -keyout "$certs/server.key" -out "$certs/server.csr" \
        -subj "/CN=localhost" \
        -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1

    printf '%s\n' "subjectAltName=DNS:localhost,IP:127.0.0.1" >"$certs/server.ext"
    openssl x509 -req -in "$certs/server.csr" \
        -CA "$certs/ca.pem" -CAkey "$certs/ca.key" -CAcreateserial \
        -out "$certs/server.crt" -days 1 -sha256 \
        -extfile "$certs/server.ext" >/dev/null 2>&1

    cat "$certs/server.key" "$certs/server.crt" >"$certs/server.pem"
    chmod 600 "$certs/server.pem" "$certs/server.key" "$certs/ca.key"
fi

docker compose up -d

mongo() {
    docker compose exec -T mongo mongosh --quiet --tls \
        --tlsCAFile /certs/ca.pem --port 57018 "$@"
}

log() {
    printf '%s\n' "$*" >&2
}

for _ in $(seq 1 60); do
    if (exec 3<>/dev/tcp/127.0.0.1/57018) 2>/dev/null; then
        exec 3>&-
        break
    fi
    sleep 1
done

# The localhost exception allows creating the first user; after that every
# command must authenticate.
mongo --eval 'db.getSiblingDB("admin").createUser({user:"root",pwd:"rootpass",roles:["root"]})' >/dev/null 2>&1 \
    || log "root user already exists"

# The user becomes visible to authenticating connections asynchronously.
for _ in $(seq 1 20); do
    if mongo -u root -p rootpass --authenticationDatabase admin \
        --eval 'db.adminCommand({ping:1}).ok' 2>/dev/null | grep -qx '1'; then
        break
    fi
    sleep 1
done

mongo -u root -p rootpass --authenticationDatabase admin --eval '
const app = db.getSiblingDB("app");
try { app.createCollection("items"); } catch (e) {}
app.items.replaceOne({_id: 1}, {_id: 1, name: "Ann"}, {upsert: true});
try { app.createUser({user: "app", pwd: "apppass", roles: [{role: "readWrite", db: "app"}]}); } catch (e) {}
try { app.createUser({user: "reader", pwd: "readerpass", roles: [{role: "read", db: "app"}]}); } catch (e) {}
' >/dev/null

printf '%s\n' "mongo auth+tls standalone is ready on 127.0.0.1:57018"
