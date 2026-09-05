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
cp .env.example .env   # fill in AGENT_TOKEN
cd web && npm install && npm run build && cd ..
docker compose up --build
```

Open http://localhost:8080.

## Deploying

Run the hub on one machine, Postgres and the hub binary together (or
use the provided `docker compose` setup), behind a reverse proxy such
as Caddy for TLS. Build the frontend once and point `-static` at
`web/dist`.

```sh
export AGENT_TOKEN=<a long random string>
export DATABASE_URL=postgres://user:pass@host:5432/uptime?sslmode=disable
./quorum serve -config services.yaml -static web/dist -addr :8080
```

Then copy the same binary to as many checker machines as you want and
run each one in agent mode.

```sh
export AGENT_TOKEN=<same token as the hub>
./quorum agent -hub https://status.example.com -agent-id <unique-name>
```

`-agent-id` defaults to the machine's hostname when left out. The hub
can double as one of the checkers too, there's no requirement that
they run on separate boxes.

To add, remove, or change a service, edit `services.yaml` on the hub
and restart it. Agents pick up the change on their own within 5
minutes.

`GET /health` reports whether the hub can reach Postgres, for load
balancers and container health checks.

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

`ping` checks shell out to the system `ping` binary instead of opening
a raw ICMP socket, which needs `iputils` installed and `CAP_NET_RAW`
available in the container. The provided Dockerfile and compose file
already handle both.

Old check rows get pruned once a day with a plain `DELETE` on rows
older than 90 days. Fine at this scale. If the table ever grows large
enough for that delete to hurt, partition it by month instead.

There's no rate limiting on the public API. Add one at the reverse
proxy if it ever needs protecting from abusive traffic.

## License

MIT, see [LICENSE](LICENSE).
