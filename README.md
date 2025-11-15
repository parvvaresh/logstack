Here’s an updated, drop-in README you can paste into your repo. It reflects everything we fixed: Loki config gotchas, healthchecks, permissions, Promtail labels, and the JSON logger change for Go.

---

# LogStack — Go + Promtail + Loki + Grafana

Dockerized logging stack that collects container logs, parses them, and shows them in Grafana with useful charts.

**Stack:**
Go service (app) → Promtail (collect & label) → Loki (store & index labels) → Grafana (Explore/Dashboards)

---

## TL;DR (Quickstart)

```bash
# From repo root (logstack/)
# 0) (optional) remove the old compose 'version:' line if you still have it
sed -i '/^version:/d' docker-compose.yml 2>/dev/null || true

# 1) fix data dirs permissions (Grafana=472, Loki=10001)
sudo mkdir -p ./data/grafana ./data/loki
sudo chown -R 472:472 ./data/grafana
sudo chown -R 10001:10001 ./data/loki
sudo chmod -R u+rwX ./data/grafana ./data/loki

# 2) build & up
docker compose up -d --build

# 3) generate test logs
curl -i http://localhost:8080/
curl -i "http://localhost:8080/work?task=etl"
curl -i "http://localhost:8080/work?task=fail_job"

# 4) open Grafana
# http://localhost:3000  (admin / admin)
# Import the JSON dashboard (Dashboards → Import) or use Explore with the queries below
```

---

## Directory layout

```
logstack/
├─ docker-compose.yml
├─ go-service/
│  ├─ go.mod
│  ├─ main.go
│  └─ Dockerfile
├─ promtail/
│  └─ config.yml
├─ loki/
│  └─ config.yml          # optional custom config; see “Loki config options”
└─ grafana/
   └─ provisioning/
      ├─ datasources/
      │  └─ datasource.yml
      └─ dashboards/
         └─ dashboard.yml  # loader; you can drop exported JSON dashboards here
```

---

## Compose (recommended shape)

Key points we use now:

* **No `version:`** (Compose v2 ignores it)
* Loki uses **built-in** `local-config.yaml` for maximum compatibility
* **Healthcheck** on Loki; Promtail/Grafana wait until Loki is healthy
* All services on a **bridge** network named `loki`

```yaml
name: logstack

services:
  loki:
    image: grafana/loki:3.1.1
    command: -config.file=/etc/loki/local-config.yaml
    container_name: loki
    volumes:
      - ./data/loki:/loki
    ports:
      - "3100:3100"
    networks: [loki]
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:3100/ready || exit 1"]
      interval: 5s
      timeout: 3s
      retries: 20

  promtail:
    image: grafana/promtail:3.1.1
    command: -config.file=/etc/promtail/config.yml
    container_name: promtail
    volumes:
      - ./promtail/config.yml:/etc/promtail/config.yml:ro
      - /var/lib/docker/containers:/var/lib/docker/containers:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
    depends_on:
      loki:
        condition: service_healthy
    networks: [loki]

  grafana:
    image: grafana/grafana:11.3.0
    container_name: grafana
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=admin
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning
      - ./data/grafana:/var/lib/grafana
    ports:
      - "3000:3000"
    depends_on:
      loki:
        condition: service_healthy
    networks: [loki]

  app:
    build:
      context: ./go-service
    image: logstack/go-log-service:latest
    container_name: app
    environment:
      - LOG_LEVEL=info
      - ADDR=:8080
    ports:
      - "8080:8080"
    networks: [loki]

networks:
  loki:
    driver: bridge
```

> If something else is using host port **3000**, either free it (`sudo fuser -k 3000/tcp`) or change the mapping to `"3001:3000"`.

---

## Promtail config (labels + JSON parsing)

`promtail/config.yml`:

```yaml
server:
  http_listen_port: 9080
  grpc_listen_port: 0

positions:
  filename: /tmp/positions.yaml

clients:
  - url: http://loki:3100/loki/api/v1/push

scrape_configs:
  - job_name: containers
    docker_sd_configs:
      - host: unix:///var/run/docker.sock
        refresh_interval: 5s

    relabel_configs:
      - source_labels: ['__meta_docker_container_id']
        target_label: '__path__'
        replacement: '/var/lib/docker/containers/$1/$1-json.log'
      - source_labels: ['__meta_docker_container_label_com_docker_compose_service']
        target_label: 'service'
      - source_labels: ['__meta_docker_container_name']
        regex: '/(.*)'
        target_label: 'container'

    pipeline_stages:
      - docker: {}
      - json:
          expressions:
            level: level
            request_id: request_id
            method: method
            path: path
            status: status
            duration_ms: duration_ms
            component: component
      - labels:
          level:
          service:
          container:
          component:
```

> On macOS/Windows (Docker Desktop), direct bind of `/var/lib/docker/containers` won’t work the same way because Docker runs in a VM. Prefer Linux host for this setup.

---

## Go service logging (make it JSON!)

In `go-service/main.go`, make sure the logger outputs **pure JSON** (so `| json` in LogQL can extract fields):

```go
// Replace the ConsoleWriter line with this:
log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
```

Rebuild & run:

```bash
docker compose build app --no-cache
docker compose up -d app
```

Test requests:

```bash
curl -i http://localhost:8080/
curl -i "http://localhost:8080/work?task=etl"
curl -i "http://localhost:8080/work?task=fail_job"
```

---

## Grafana

* URL: `http://localhost:3000` (admin/admin → change password)
* Datasource provisioning points to Loki. Use **Explore** first, then build dashboards.

**Useful LogQL:**

```logql
{service="app"}                     # all app logs
{service="app"} | json | level="error"
{service="app"} | json | component="http"
sum(count_over_time({service="app"} | json [1m]))
quantile_over_time(0.95, {service="app"} | json | component="http" | unwrap duration_ms [5m])
sum(count_over_time({service="app"} | json | status=~"5.." [1m]))
```

**If `service="app"` is empty**, try `{container="app"}`; it means the `service` label wasn’t created yet (check Promtail relabels).

**Dashboard import:** Dashboards → Import → paste the JSON I provided earlier. Pick datasource “Loki”.

---

## Loki config options (optional)

If you prefer a custom Loki config instead of `local-config.yaml`, place your file at `loki/config.yml` and change the `command` to:

```yaml
loki:
  image: grafana/loki:3.1.1
  command: -config.file=/etc/loki/config.yml
  volumes:
    - ./loki/config.yml:/etc/loki/config.yml:ro
    - ./data/loki:/loki
  ...
```

A safe baseline for Loki 3.x:

```yaml
auth_enabled: false
server:
  http_listen_port: 3100
  grpc_listen_port: 9096
common:
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
storage_config:
  filesystem:
    directory: /loki/chunks
  boltdb_shipper:
    active_index_directory: /loki/index
    cache_location: /loki/boltdb-cache
schema_config:
  configs:
    - from: 2024-01-01
      store: boltdb-shipper
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h
compactor:
  working_directory: /loki/boltdb-shipper-compactor
limits_config:
  retention_period: 168h
```

> Be careful with YAML keys/indent. Wrong keys (e.g., `shared_store` in the wrong section, or `common.path` instead of `path_prefix`) will prevent Loki from starting.

---

## Troubleshooting

**Compose warns:** `the attribute version is obsolete`
→ Remove the top `version:` line (we already do).

**Grafana says `/var/lib/grafana` is not writable**

```bash
sudo chown -R 472:472 ./data/grafana
sudo chmod -R u+rwX ./data/grafana
docker compose up -d grafana
```

**Promtail error:** `no such host loki`

* Make sure Loki is up and healthy:

  ```bash
  docker compose ps loki
  docker compose logs -f loki
  ```
* Test from inside Promtail:

  ```bash
  docker compose exec promtail sh -lc 'getent hosts loki; wget -qO- http://loki:3100/ready || echo FAIL'
  ```
* Ensure both services are on the same network and `depends_on.healthcheck` is present.

**Port already in use (3000/8080/3100)**

* Free the port:

  ```bash
  sudo fuser -k 3000/tcp
  ```

  or change mapping (e.g., `3001:3000`).

**No data in dashboards**

* Check Explore queries:

  * `{service="app"}` or `{container="app"}`
  * If using JSON fields, include `| json`.
* Make sure Go logs are **JSON** (see section above).
* Generate test requests again.

---

## Production notes

* Move Loki storage to object store (S3/GCS/MinIO), enable HA ring, raise `replication_factor`.
* Keep labels **low-cardinality** (`service`, `level`, `component`). Avoid `request_id`/`user_id` as labels.
* Hide Loki/Grafana behind a reverse proxy & SSO; don’t expose Loki publicly.
* Increase `limits_config.ingestion_rate_mb` if you see 429s.

---

## License

MIT (or your preferred license).
