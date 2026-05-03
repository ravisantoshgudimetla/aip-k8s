# Garbage Collection

The garbage collection (GC) engine is a background service in the AIP controller that cleans up terminal `AgentRequest` records and their associated `AuditRecord` objects. It ensures cluster stability by protecting etcd from unbounded storage growth.

Only terminal-phase `AgentRequests` (`Completed`, `Failed`, `Denied`, `Expired`) are eligible for deletion. Active requests (`Pending`, `Approved`, `Executing`, `AwaitingVerdict`) are never touched.

---

## Quick start

By default, the GC engine is disabled. To protect your cluster from etcd pressure with a 14-day retention window, use the following minimal configuration.

> [!WARNING]
> Dry-run mode is enabled by default (`--gc-dry-run=true`). You must explicitly set it to `false` for any records to be deleted.

### Helm configuration

```yaml
# values.yaml — minimal safe configuration
gc:
  enabled: true
  dryRun: false
  hardTTL: 14d
  safetyMinCount: 10
```

### CLI flags

```bash
/manager \
  --gc-enabled=true \
  --gc-dry-run=false \
  --gc-hard-ttl=14d \
  --gc-safety-min-count=10
```

---

## Deletion decision logic

For every terminal `AgentRequest`, the GC worker executes the following logic in order:

1. **Eligible?**: Only `Completed`, `Failed`, `Denied`, and `Expired` phases are considered.
2. **Hard TTL override**: If age is greater than or equal to `--gc-hard-ttl`, delete unconditionally. The hard TTL always wins to protect etcd.
3. **Cascade delete**: Associated `AuditRecord` objects (linked via `spec.agentRequestRef`) are deleted before the `AgentRequest` itself to avoid orphaned records.

---

## Safe rollout procedure

To enable garbage collection safely in a production cluster:

1. **Enable dry-run**: Set `gc.enabled=true` and `gc.dryRun=true`.
2. **Observe metrics**: Monitor the `aip_gc_objects_skipped_total{reason="dry_run"}` metric. This shows you exactly how many objects would be deleted.
3. **Disable dry-run**: Once you are confident in the configuration, set `gc.dryRun=false`.

---

## Observability

The GC engine emits Prometheus metrics to the controller's metrics endpoint (default `:8080/metrics`).

| Metric | Labels | Description |
|---|---|---|
| `aip_gc_objects_deleted_total` | `resource`, `reason` | Total records purged. Reason: `hard_ttl`. |
| `aip_gc_objects_skipped_total` | `resource`, `reason` | Records skipped. Reasons: `dry_run`, `safety_valve`. |
| `aip_gc_scan_duration_seconds` | `resource` | Histogram of the time taken for a full GC scan cycle. |
| `aip_gc_scan_objects_evaluated_total` | `resource` | Total records read from etcd during the scan. |

### Useful queries

**Deletion rate (objects/sec):**
```promql
rate(aip_gc_objects_deleted_total[5m])
```

**Records skipped due to dry-run:**
```promql
rate(aip_gc_objects_skipped_total{reason="dry_run"}[5m])
```

---

## Safety mechanisms

- **Safety valve**: The GC engine will skip its cycle entirely if the total number of terminal `AgentRequest` objects in the cluster is below `--gc-safety-min-count` (default: 10). This prevents catastrophic data loss in case of misconfiguration.
- **Dry-run by default**: The default state of the engine is to log its intent without performing any deletions.
- **Rate limiting**: Deletions are throttled to `--gc-delete-rate-per-sec` (default: 100) to prevent overwhelming the Kubernetes API server or the underlying etcd.
- **Leader only**: GC logic only runs on the leader instance of the controller.

> [!WARNING]
> Disabling dry-run mode causes permanent, irreversible deletion of terminal AgentRequest records. Verify with dry-run=true first and confirm metrics before setting dryRun=false.

---

## Configuration reference

| Flag | Default | Description |
|---|---|---|
| `--gc-enabled` | `false` | Enable the GC engine background loop. |
| `--gc-interval` | `1h` | Time between GC scan cycles. |
| `--gc-dry-run` | `true` | If true, log deletions without acting. |
| `--gc-hard-ttl` | `0` | Forced deletion age for terminal AgentRequests. `0` disables the hard-TTL deletion rule only — scans still run. To stop GC entirely, use `--gc-enabled=false`. |
| `--gc-delete-rate-per-sec` | `100` | Max object deletions per second (token bucket). |
| `--gc-page-size` | `500` | Objects per list page during GC scan. |
| `--gc-safety-min-count` | `10` | Skip GC if terminal object count is below this threshold. |
