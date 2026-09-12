# Context HTTPS proxy setup

The API listens on loopback port 8401. Public browser access requires a proxy.
The dedicated unit here loads only the Context site, without importing unrelated
sites from `/etc/caddy/sites-enabled`. It owns ports 80 and 443 and must not run
alongside another proxy on those ports.

Run the following from a checkout on the server using an administrator account.
First confirm `caddy` and `caddy-api` are inactive and ports 80/443 are unused:

```sh
systemctl is-active caddy caddy-api
ss -ltn
```

If another web server is active, integrate the site into that proxy instead of
starting this unit. Confirm the Cloudflare DNS origin for `context-api.integ.life`
points to this server and inbound TCP 80/443 is allowed. Use Full (strict) TLS
with a valid origin certificate; Caddy automatically obtains a certificate when
domain validation can reach the server.

```sh
sudo install -m 0644 deploy/production/context-api.caddy /etc/caddy/event-context.Caddyfile
sudo install -m 0644 deploy/production/event-context-proxy.service /etc/systemd/system/event-context-proxy.service
sudo -u caddy caddy validate --config /etc/caddy/event-context.Caddyfile --adapter caddyfile
sudo systemctl daemon-reload
sudo systemctl enable --now event-context-proxy.service
sudo journalctl -u event-context-proxy -n 50 --no-pager
curl --fail --show-error https://context-api.integ.life/healthz
curl --fail --show-error -i -X OPTIONS https://context-api.integ.life/v1/auth/register \
  -H 'Origin: https://context.integ.life' \
  -H 'Access-Control-Request-Method: POST' \
  -H 'Access-Control-Request-Headers: content-type'
```

Expected results: health HTTP 200 with `{"status":"ok"}`, preflight HTTP 204
with `Access-Control-Allow-Origin: https://context.integ.life`. Then retry
registration in the frontend. Do not consider the incident resolved until public
checks and registration succeed.

If startup fails, inspect the journal for certificate, firewall, or port conflicts.
To roll back this new proxy, run `sudo systemctl disable --now event-context-proxy`.
The backend continues to run locally. Normal backend releases preserve proxy
configuration; changes to this proxy require separate administrator installation.
