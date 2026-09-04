# Docker and LAN deployment

LocalRouter's supported container deployment is Linux host networking. The
loopback listener keeps the console and maintenance surfaces local to the host,
while an explicit second listener exposes only Service-Token-authenticated
consumer routes to a private LAN.

## Security model

| Listener | Default | Routes |
|---|---:|---|
| Local operator | `127.0.0.1:8317` | console, `/local/api`, `/manage/mcp`, and all consumer routes |
| LAN service | operator-selected private IP on port `8318` | `/v1`, `/v1beta`, `/p`, `/w`, `/mcp`, `/agent`, discovery and sanitized docs |

The LAN listener never registers the console, `/local/status`, `/local/api`, or
`/manage/mcp`. Every callable LAN route requires a Service Token. Discovery and
sanitized documentation are readable so an authorized Agent can learn the
contract before authenticating. Browser origins are denied unless they exactly
match `LOCAL_GATEWAY_LAN_ALLOWED_ORIGINS`.

LAN HTTP is appropriate only on a trusted private network. Put a reviewed TLS
reverse proxy or private overlay in front before crossing an untrusted network.
Do not forward the loopback operator listener.

## Fresh Docker deployment

Docker Engine and Docker Compose are required. Find the host's private LAN
address, copy the example, and replace its placeholder address:

```bash
cp packaging/docker/localrouter.env.example .env
${EDITOR:-vi} .env
docker compose up --build -d
```

The Compose file deliberately uses `network_mode: host`. This preserves access
to existing host-loopback adapters and keeps the console at
`http://127.0.0.1:8317/`. Other LAN devices use the configured private address:

```text
http://192.168.1.10:8318/.well-known/localrouter.json
http://192.168.1.10:8318/v1
http://192.168.1.10:8318/mcp
```

Create a distinct Service Token for each device or Agent in the local console.
Never copy the administrator credential to a LAN client. Configure the host
firewall to allow port 8318 only from the intended private subnet.

Compose derives `LOCAL_GATEWAY_LAN_PUBLIC_BASE_URL` from the same private host
address. Graph workflows use this operator-owned value for callback URLs and
never trust a client's `Host` header. A native deployment bound to a specific
private IP can derive the same URL; an all-interface listener must configure it
explicitly before accepting graph workflows.

Check the deployment without revealing any Token:

```bash
docker compose ps
curl --fail http://127.0.0.1:8317/healthz
curl --fail http://192.168.1.10:8318/healthz
curl --fail http://192.168.1.10:8318/.well-known/localrouter.json
```

`docker compose down` preserves the four named volumes. Do not add `--volumes`
unless deleting all container configuration, Tokens, SQLite data, workflows,
drafts, and release history is explicitly intended.

## Reusing an existing native installation

Never let the systemd service and the container open the same SQLite and state
directories concurrently. Back up the config, data, and state directories,
then stop the native service before starting the bind-mounted container.

Set the four `LOCALROUTER_*_DIR` values in `.env`, then run:

```bash
systemctl --user stop localrouter.service
docker compose -f compose.yaml -f packaging/docker/compose.bind.yaml up --build -d
```

If verification fails, stop the container before restarting the native service:

```bash
docker compose -f compose.yaml -f packaging/docker/compose.bind.yaml down
systemctl --user start localrouter.service
```

External-readonly pool sources and other private files outside the four XDG
directories require explicit read-only bind mounts at the paths declared by the
operator's private Pack. They are never copied into the image. Registration,
OAuth consent, CAPTCHA, payment, and credential refresh remain owned by their
external maintainers.

## Browser clients

Command-line and Agent clients do not send an `Origin` header. A browser app
must be added explicitly with an exact scheme, host, and port:

```text
LOCALROUTER_LAN_ALLOWED_ORIGINS=https://agent-ui.home.arpa,http://192.168.1.20:3000
```

Wildcards, URL paths, credentials, queries, and fragments are rejected. This
setting grants browser access only to consumer routes and does not expose the
console or maintenance APIs.

Remote `lr` clients must opt in before a non-loopback URL is accepted:

```bash
export LOCALROUTER_ALLOW_LAN=true
export LOCALROUTER_BASE_URL=http://192.168.1.10:8318
export LOCALROUTER_API_TOKEN_FILE=/private/path/to/this-agent-token
lr status
lr tree
```

Before reading the mode-`0600` Token file, `lr` fetches discovery without a
credential and requires `scope=lan-service` plus unavailable maintenance. It
also requires the discovery and MCP URLs to share the selected service origin;
the maintenance URL remains loopback-only.

## Verification

The deterministic container acceptance test builds the real image, starts it
on isolated dynamic ports and volumes, verifies route isolation and file modes,
stops it with SIGTERM, recreates it, and checks Token persistence:

```bash
make docker-test
```

It does not contact a real or paid provider. Multi-device physical LAN access,
firewall policy, TLS termination, and provider-backed calls remain separate
deployment acceptance layers.
