# mamotama example: WordPress (High Paranoia)

This example puts mamotama in front of WordPress and enables CRS with higher paranoia.

## Start

```bash
cd examples/wordpress
./setup.sh
docker compose up -d --build
```

- WordPress URL: `http://localhost:${CORAZA_PORT:-19092}`
- Coraza API: `http://localhost:${CORAZA_PORT:-19092}/mamotama-api/status`

## Notes

- `WAF_CRS_SETUP_FILE=rules/crs-setup-high-paranoia.conf` is used.
- `tx.blocking_paranoia_level` and `tx.detection_paranoia_level` are set to `2`.
- Login endpoint `/wp-login.php` has stricter rate limits.
