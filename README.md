# quorum

Self-hosted status page with real quorum voting. A service only shows
**down** once every checker machine agrees it's down, so one flaky
network link on one machine never flips the public page.

## How it works

`services.yaml` lists what to check by hand. There's no admin UI and
no login for the status page itself, since it's meant to be public.

The **hub** (`quorum serve`) reads that file, stores results in
Postgres, and serves three things: `GET /api/status` (public JSON),
`GET /internal/targets` and `POST /internal/results` (used by agents,
behind a bearer token), and the built frontend if `-static` points at
`web/dist`.

Each **agent** (`quorum agent`) pulls the target list from the hub,
checks every service on its own interval, and posts results back. It
re-fetches the target list every 5 minutes, so a config change on the
hub reaches all agents without redeploying them.

A service reads as up if at least one agent's latest check succeeded,
down if every agent that reported recently failed, and unknown if
nothing has come in for a while.

## Run it locally

```sh
cp .env.example .env && chmod 600 .env   # fill in AGENT_TOKEN and POSTGRES_PASSWORD
cd web && npm install && npm run build && cd ..
docker compose up --build
```

Open http://localhost:8080.

## Deploying

Every push to `main` builds and publishes a Docker image to
`ghcr.io/bruhdows/quorum:latest` (also tagged with the commit's short
SHA). I run the hub off the compose setup in this repo: hub, Postgres,
and a local agent together, so the page never reads "unknown" on a
single box.

```sh
cp .env.example .env && chmod 600 .env   # fill in both secrets, hex only (see below)
docker compose up -d --build
```

Compose pins `image: ghcr.io/bruhdows/quorum:latest` next to `build: .`,
so later upgrades are just `docker compose pull && docker compose up -d`.
While the repo is private, pulling needs `docker login ghcr.io` with a
token that has `read:packages`.

`POSTGRES_PASSWORD` gets interpolated into `DATABASE_URL`, so keep it to
`openssl rand -hex` output. Anything with `@ / : ? # &` in it breaks the
connection string.

The hub speaks plain HTTP on :8080. Mine sits behind a Cloudflare
tunnel, but any reverse proxy you already run works. Rate limiting and
WAF live there too, the hub does none of that itself.

### Cloudflare tunnel

Point an ingress rule at the hub and leave everything else alone:

```yaml
ingress:
  - hostname: status.example.com
    service: http://localhost:8080
  - service: http_status:404
```

Three things I learned the hard way. cloudflared phones home over
outbound UDP 7844, so that needs to be open. Bot Fight Mode challenges
the agents (they are plain Go HTTP clients, no browser to pass the
check), so keep it off this hostname or skip `/internal/*` in your WAF
rules. And caching needs no rules at all: the API sends its own
`Cache-Control`, Cloudflare honors origin headers, and the authed agent
endpoints are never edge cached.

### Standalone hub without compose

`docker run` only fits when Postgres already lives somewhere else. The
image has no database in it:

```sh
docker pull ghcr.io/bruhdows/quorum:latest
docker run -d --restart unless-stopped -p 8080:8080 \
  -e AGENT_TOKEN=<a long random string> \
  -e DATABASE_URL=postgres://user:pass@host:5432/uptime?sslmode=disable \
  -v $(pwd)/services.yaml:/app/services.yaml:ro \
  ghcr.io/bruhdows/quorum:latest
```

From source instead: run Postgres and the hub binary side by side,
behind whatever proxy terminates your TLS. Build the frontend once and
point `-static` at `web/dist`.

```sh
export AGENT_TOKEN=<a long random string>
export DATABASE_URL=postgres://user:pass@host:5432/uptime?sslmode=disable
./quorum serve -config services.yaml -static web/dist -addr :8080
```

### Extra checkers (agents)

Copy the binary to each checker machine and run it in agent mode:

```sh
export AGENT_TOKEN=<same token as the hub>
./quorum agent -hub https://status.example.com -agent-id <unique-name>
```

Same thing via Docker (no Postgres, no config mount on these boxes):

```sh
docker run -d --restart unless-stopped \
  --cap-add NET_RAW \
  -e AGENT_TOKEN=<same token as the hub> \
  ghcr.io/bruhdows/quorum:latest \
  agent -hub https://status.example.com -agent-id <unique-name>
```

`--cap-add NET_RAW` is for `ping` checks, which need it inside the
container. Compose already sets it on its bundled agent. Leave out
`-agent-id` and the machine's hostname is used. The hub happily doubles
as a checker, boxes don't have to be separate.

Edit `services.yaml` on the hub and restart it to add, remove, or change
a service. Agents notice on their own within 5 minutes.

`GET /health` answers whether the hub can reach Postgres. Point load
balancers at it. It also backs the Dockerfile `HEALTHCHECK` and the
compose healthcheck.

### Backups and upgrades

History lives in Postgres, so that is the thing to back up. A `backup`
service in compose dumps it nightly-ish (whenever you run it) and keeps
14 days of dumps in `./backups`:

```sh
docker compose --profile backup up backup
```

Cron it and forget it:

```sh
0 3 * * * cd /opt/quorum && docker compose --profile backup up backup
```

Restore means stopping everything that writes, then playing one dump
back. The `./backups` folder is mounted into Postgres read-only for
exactly this:

```sh
docker compose stop hub agent
docker compose exec postgres pg_restore -U uptime -d uptime -c /backups/uptime-YYYY-MM-DD.dump
docker compose start hub agent
```

Upgrades are `docker compose pull && docker compose up -d`. The hub
creates the schema on start, so there are no migrations to run. One
exception: a major Postgres bump (18 to 19, someday) can't reuse the old
data directory. Dump first, delete the `pgdata` volume, let the new
Postgres start empty, restore the dump. Rotating `AGENT_TOKEN` means
restarting the hub and every agent together, since they all compare
against the same string.

## Alerts

The hub watches the live quorum status and posts to Discord when something
goes down and when it comes back. Env var preferred, so the secret stays
out of the config file (it also accepts `alerts.discord_webhook_url` in
`services.yaml`):

```sh
export DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
```

```yaml
alerts:
  cooldown_minutes: 30    # one alert per service per window while flapping;
                          # recoveries always go out immediately
  check_interval_seconds: 30
  notify_unknown: false   # also alert when agents stop reporting
```

A restart never re-announces the current state. The first poll after boot
only sets the baseline. Failed sends get logged and nothing else, alerting
never takes down the hub.

## Tuning

Top-level keys in `services.yaml` (restart the hub to apply):

| Key                | Default | Meaning                                                   |
| ------------------ | ------- | --------------------------------------------------------- |
| `retention_days`   | 90      | History kept; the strip, uptime %, and pruning all use it |
| `stale_multiplier` | 3       | A check counts as recent for Nx its own interval          |

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

Frontend dev server, pointed at a hub running locally on :8080:

```sh
cd web && npm run dev
```

## Known trade-offs

`ping` checks shell out to the system `ping` binary instead of opening a
raw ICMP socket. That wants `iputils` and `CAP_NET_RAW` in the container.
The Dockerfile and compose file handle both, which is why the agent
service carries that odd `cap_add`.

Old rows get pruned once a day with a plain `DELETE` past
`retention_days`. Fine at this scale. If the table ever outgrows that,
partition by month instead.

The public API answers from short-lived in-memory caches (status holds
5s, uptime a minute, detail 10s), so page loads don't each hit Postgres.
Anything stricter, rate limiting or WAF, lives at the proxy. The hub
trusts no headers and keeps no per-client state, so there is nothing
there to spoof.

## License

MIT, see [LICENSE](LICENSE).
