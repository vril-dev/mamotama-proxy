# mamotama example: API Gateway (Rate-limit focused)

This example protects a JSON API and applies stricter limits on auth endpoints.

## Start

```bash
cd examples/api-gateway
./setup.sh
docker compose up -d --build
```

- API base URL: `http://localhost:${CORAZA_PORT:-19093}/v1`
- Coraza API: `http://localhost:${CORAZA_PORT:-19093}/mamotama-api/status`

## Smoke tests

```bash
curl -i "http://localhost:19093/v1/health"
curl -i -X POST "http://localhost:19093/v1/auth/login" -H 'content-type: application/json' -d '{"username":"demo","password":"demo"}'
```

Rate-limit check (expect `429` after repeated calls):

```bash
for i in $(seq 1 12); do
  curl -s -o /dev/null -w "%{http_code}\n" -X POST "http://localhost:19093/v1/auth/login" -H 'content-type: application/json' -d '{"username":"demo","password":"demo"}'
done
```
