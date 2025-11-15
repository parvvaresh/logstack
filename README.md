# LogStack — Go + Promtail + Loki + Grafana

End-to-end, dockerized logging stack that shows beautifully parsed, filterable logs in Grafana.
The stack includes:

* **Go service (app)** – emits structured logs and simple HTTP endpoints
* **Promtail** – discovers Docker containers, parses logs, adds labels
* **Loki** – indexes labels and stores log chunks
* **Grafana** – explores logs and builds dashboards

---

## Contents

* [Architecture](#architecture)
* [Directory layout](#directory-layout)
* [Prerequisites](#prerequisites)
* [Quick start](#quick-start)
* [Services](#services)
* [Configuration](#configuration)

  * [Loki (`loki/config.yml`)](#loki-lokiconfigyml)
  * [Promtail (`promtail/config.yml`)](#promtail-promtailconfigyml)
  * [Grafana provisioning](#grafana-provisioning)
* [Using Grafana / LogQL examples](#using-grafana--logql-examples)
* [Generate test logs](#generate-test-logs)
* [Production notes](#production-notes)
* [Troubleshooting](#troubleshooting)
* [FAQ](#faq)
* [License](#license)

---

## Architecture

```
Go app (stdout JSON logs)
           │
           ▼
      Docker JSON logs
           │ (discover + parse + label)
           ▼
        Promtail ──────► Loki (index labels + store chunks)
                                    │
                                    ▼
                                 Grafana
```

* All services communicate on an isolated Docker **bridge** network named `loki`.
* Only Grafana (3000), Go app (8080) and Loki (3100) are published to the host via `ports:`.

---

## Directory layout

```
logstack/
├─ docker-compose.yml
├─ go-service/
│  ├─ go.mod
│  ├─ main.go
│  └─ Dockerfile
├─ loki/
│  └─ config.yml
├─ promtail/
│  └─ config.yml
└─ grafana/
   └─ provisioning/
      ├─ datasources/
      │  └─ datasource.yml
      └─ dashboards/
         └─ dashboard.yml   # (loader exists; add your JSON dashboards here)
```

---

## Prerequisites

* Docker 24+ and Docker Compose plugin
* Linux is recommended. On macOS/Windows (Docker Desktop), Promtail’s bind-mount of
  `/var/lib/docker/containers` may not work the same due to the VM layer—see **Troubleshooting**.

---

## Quick start

```bash
# From the repo root (logstack/)
docker compose up -d --build
```

* **Grafana**: [http://localhost:3000](http://localhost:3000) (user: `admin`, pass: `admin`)
* **Go app**: [http://localhost:8080](http://localhost:8080)
* **Loki API**: [http://localhost:3100](http://localhost:3100)

> Tip: Change Grafana’s default password on first login (⚙️ → Users & access).

---

## Services

### 1) Go app (`app`)

* Builds from `./go-service` and exposes port `8080`.
* Emits structured logs (via `zerolog`) designed for Promtail → Loki.
* Endpoints:

  * `GET /` → simple hello
  * `GET /work?task=...` → simulates work; `fail_*` triggers 500 + error log
  * `GET /healthz` → 200 OK for probes
* Env:

  * `LOG_LEVEL=info` (set to `debug|info|warn|error`)
  * `ADDR=:8080`

### 2) Promtail (`promtail`)

* Discovers Docker containers using the Docker socket.
* Reads Docker JSON logs from `/var/lib/docker/containers/*/*-json.log`.
* Parses JSON, assigns labels like `service`, `container`, `level`, `component`.
* Pushes to Loki at `http://loki:3100/loki/api/v1/push`.

### 3) Loki (`loki`)

* Stores logs as compressed chunks and indexes only **labels** (cost-efficient).
* Single-node, filesystem storage, 7-day retention by default (configurable).

### 4) Grafana (`grafana`)

* Pre-provisioned **Loki** datasource.
* Use **Explore** to query logs with **LogQL**; create dashboards as needed.
* Data persisted under `./data/grafana`.

---

## Configuration

### Loki (`loki/config.yml`)

Key points (single-node dev/stage friendly):

* `server.http_listen_port: 3100` – Loki HTTP API
* `common.storage.filesystem` – local disk storage under `/loki`
* `schema_config` – `boltdb-shipper` + `v13` schema
* `limits_config` – ingestion throttles, reject old samples (> 7 days)
* `table_manager.retention_period: 168h` – 7-day retention

> Increase retention and switch to S3/GCS/MinIO for production. Raise `replication_factor` and use a distributed ring for HA.

### Promtail (`promtail/config.yml`)

* `docker_sd_configs` for auto-discovering running containers
* `pipeline_stages`:

  * `docker` – unwraps Docker log metadata
  * `json` – extracts fields (`level`, `request_id`, `component`, `method`, `path`, `status`, `duration_ms`)
  * `labels` – exposes selected fields as labels for Loki

> Keep labels low-cardinality (`service`, `level`, `component`). Avoid labeling high-cardinality fields like `request_id` or `user_id`.

### Grafana provisioning

* `grafana/provisioning/datasources/datasource.yml` sets the **Loki** datasource as default.
* `grafana/provisioning/dashboards/dashboard.yml` loads dashboards from the same folder.
  Drop any exported dashboard JSONs there to auto-import.

---

## Using Grafana / LogQL examples

Open **Explore** (left sidebar) → select **Loki**:

**All app logs**

```
{service="app"}
```

**Only errors**

```
{service="app"} | json | level="error"
```

**HTTP request logs**

```
{service="app"} | json | component="http"
```

**Count logs per level (make a time-series panel)**

```
sum(count_over_time({service="app"} | json | level!="" [1m])) by (level)
```

**Filter by request_id**

```
{service="app"} | json | request_id="PUT-YOUR-ID"
```

---

## Generate test logs

```bash
# Hello
curl -i http://localhost:8080/

# Simulated successful work (info/warn)
curl -i "http://localhost:8080/work?task=etl"

# Simulated failure (error + HTTP 500)
curl -i "http://localhost:8080/work?task=fail_job"
```

---

## Production notes

* **Persistence**

  * Loki data: `./data/loki`
  * Grafana data: `./data/grafana`
* **Security**

  * Change Grafana admin password immediately.
  * Don’t publish Loki to the internet; keep it internal (remove `ports:` or firewall).
  * Consider putting Grafana behind SSO/reverse proxy (OAuth, OIDC).
* **Label strategy**

  * Labels must be low-cardinality. Prefer `service`, `level`, `component`.
* **Scaling**

  * Loki: move to object storage (S3/GCS/MinIO), enable HA ring, raise `replication_factor`.
  * Promtail: run per-node; shard if needed.
* **Health checks (optional)**

  * Add `healthcheck:` to services for stricter start/depends logic.

---

## Troubleshooting

**Promtail can’t read Docker logs (macOS/Windows Docker Desktop)**
Docker Desktop runs inside a VM; direct bind-mount of `/var/lib/docker/containers` may not work. Options:

* Run on Linux host, **or**
* Run Promtail on the VM/host that actually has Docker’s container log files, **or**
* Switch to `journald`/file scrapes if applicable.

**I don’t see parsed JSON fields in Grafana**
Make sure the Go app emits **JSON** logs. If you changed the logger to a console writer, switch back to JSON:

```go
// in main.go, prefer pure JSON output:
log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
```

Then `promtail`’s `json` stage will extract fields properly.

**HTTP 429 from Loki**
You’re hitting ingestion limits. Increase in `loki/config.yml`:

```yaml
limits_config:
  ingestion_rate_mb: 50
  ingestion_burst_size_mb: 100
```

**High cardinality / “max_streams_per_user” exceeded**
Remove/avoid labels that explode in variety (e.g., request_id, user_id).

**Verify network membership**
Check which containers are attached to the `loki` network:

```bash
docker network inspect <project>_loki
```

---

## FAQ

**Why Loki instead of ELK?**
Loki only indexes **labels**, not full log text—dramatically cheaper and simpler at scale, with tight Grafana integration.

**Can I add alerts?**
Yes. Add Alertmanager and Loki ruler rules, then point `ruler.alertmanager_url` to your Alertmanager service.

**How do I change retention?**
Edit `table_manager.retention_period` (e.g., `720h` for 30 days) and ensure storage capacity.

---


