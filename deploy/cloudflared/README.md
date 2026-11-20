# The Cloudflare tunnel

`cloudflared` gives the host `https://<host>` with no open ports: it dials out to Cloudflare, and Cloudflare sends the hostname's requests back down that connection (DESIGN 10.1; SP 9.1). The tunnel is managed in the Cloudflare dashboard, so its routes live there and the container needs only the token in `config/tunnel.env`.

## What the tunnel needs

One public hostname, and nothing else:

| Field | Value |
|---|---|
| Hostname | `<host>`, the one in `base_url` |
| Service | `http://pairtimed:8080` |
| Path | empty |

`pairtimed` is the compose service name, which resolves on the network compose makes for the two containers, and 8080 is `listen` in `pairtime.toml`. HTTP inside that network is fine: `cloudflared` terminates TLS at Cloudflare's edge and the hop to `pairtimed` never leaves the host.

Never route a hostname or path to port 9091. That is `ops_listen`, with `/readyz` and `/metrics`, and it listens on 127.0.0.1 inside the `pairtimed` container, so a route to it would fail anyway. Leave Cloudflare Access off for `<host>`: devices, browsers and `probe.yml` all reach it without a Cloudflare login.

`ingress.json` is the same route as the body of Cloudflare's API call for a dashboard-managed tunnel, for setting it without the dashboard:

```sh
curl -fsS -X PUT \
  "https://api.cloudflare.com/client/v4/accounts/$ACCOUNT_ID/cfd_tunnel/$TUNNEL_ID/configurations" \
  -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" -H 'Content-Type: application/json' \
  --data "$(sed "s/<host>/$HOST/" ingress.json)"
```

## Checking it

After `docker compose up -d`, `docker compose logs cloudflared` should show four connections registered, and from anywhere:

```sh
curl -fsS https://<host>/healthz
```

`pairtimed doctor` makes the same request from inside the server container, and `probe.yml` from GitHub's network (deploy/runbook.md, steps 8 and 9).
