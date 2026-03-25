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

- `data/conf/config.json` uses `paths.crs_setup_file=rules/crs-setup-high-paranoia.conf`.
- `tx.blocking_paranoia_level` and `tx.detection_paranoia_level` are set to `2`.
- Login endpoint `/wp-login.php` has stricter rate limits.
