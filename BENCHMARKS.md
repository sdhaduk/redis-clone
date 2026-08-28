# Benchmark Results: redis-clone vs Redis 8.10.0

Head-to-head performance comparison using `redis-benchmark`, run against both servers on the same machine under identical conditions.

## Test environment

| | |
|---|---|
| Hardware | Apple M4 Max, 36 GB RAM |
| OS | macOS 26.5.1 |
| Real Redis | 8.10.0 (Homebrew), `--port 6380 --save '' --appendonly no` |
| redis-clone | built from source with Go 1.26.3, default flags (AOF off), port 6379 |
| Benchmark tool | redis-benchmark 8.10.0 |
| Date | 2026-08-28 |

Methodology: both servers restarted fresh before the run, persistence disabled on both (except the AOF test), datasets empty at start. Each measured run is preceded by an unrecorded 10k-request warm-up run against each server.

---

## Test 1: Baseline single-client latency

**What it measures:** pure per-command round-trip cost with no concurrency — client sends one command, waits for the reply, sends the next. Isolates the full request path: socket read → RESP decode → dispatch → execute → RESP encode → socket write.

**Command:**
```bash
redis-benchmark -p <port> -t get,set -n 50000 -c 1
```

**Results:**

| Metric | redis-clone | Redis 8.10.0 | clone vs Redis |
|---|---|---|---|
| SET throughput | 48,780 req/s | 58,343 req/s | 84% |
| GET throughput | 49,020 req/s | 60,024 req/s | 82% |
| SET latency avg / p50 / p99 (ms) | 0.018 / 0.023 / 0.031 | 0.013 / 0.015 / 0.023 | — |
| GET latency avg / p50 / p99 (ms) | 0.018 / 0.023 / 0.031 | 0.013 / 0.015 / 0.023 | — |
| SET max latency (ms) | 0.199 | 0.095 | — |
| GET max latency (ms) | 0.111 | 0.151 | — |

**Observations:** at a single connection the clone runs at ~82–84% of real Redis throughput, with a ~5µs higher average round trip (18µs vs 13µs). The gap is per-command fixed overhead — the clone's path includes two Go channel hops (connection goroutine → commander goroutine → back) that Redis doesn't have. Tail latency (p99) is 31µs vs 23µs; both distributions are extremely tight at this load.

<details>
<summary>Raw output: redis-clone</summary>

```
====== SET ======
  50000 requests completed in 1.02 seconds
  1 parallel clients
  3 bytes payload

Summary:
  throughput summary: 48780.49 requests per second
  latency summary (msec):
          avg       min       p50       p95       p99       max
        0.018     0.008     0.023     0.023     0.031     0.199

====== GET ======
  50000 requests completed in 1.02 seconds
  1 parallel clients
  3 bytes payload

Summary:
  throughput summary: 49019.61 requests per second
  latency summary (msec):
          avg       min       p50       p95       p99       max
        0.018     0.008     0.023     0.023     0.031     0.111
```
</details>

<details>
<summary>Raw output: Redis 8.10.0</summary>

```
====== SET ======
  50000 requests completed in 0.86 seconds
  1 parallel clients
  3 bytes payload

Summary:
  throughput summary: 58343.06 requests per second
  latency summary (msec):
          avg       min       p50       p95       p99       max
        0.013     0.008     0.015     0.023     0.023     0.095

====== GET ======
  50000 requests completed in 0.83 seconds
  1 parallel clients
  3 bytes payload

Summary:
  throughput summary: 60024.01 requests per second
  latency summary (msec):
          avg       min       p50       p95       p99       max
        0.013     0.008     0.015     0.023     0.023     0.151
```
</details>

---

## Test 2: Concurrency sweep

**What it measures:** how throughput scales as concurrent connections grow. In the clone, every connection's commands funnel into a single commander goroutine over one channel — this test looks for the ceiling that design imposes. (Real Redis executes commands single-threaded too, so it faces the same kind of ceiling, just with different plumbing.)

**Command:**
```bash
redis-benchmark -p <port> -t get -n 100000 -c {1,10,50,200,500} --csv
```

**Results (GET):**

| Clients | clone req/s | Redis req/s | clone vs Redis | clone p50 / p99 (ms) | Redis p50 / p99 (ms) |
|---|---|---|---|---|---|
| 1 | 48,193 | 59,952 | 80% | 0.023 / 0.031 | 0.015 / 0.023 |
| 10 | 131,752 | 228,311 | 58% | 0.055 / 0.087 | 0.031 / 0.047 |
| 50 | 134,590 | 238,663 | 56% | 0.207 / 0.319 | 0.111 / 0.207 |
| 200 | 136,054 | 226,244 | 60% | 0.767 / 1.479 | 0.423 / 0.831 |
| 500 | 134,228 | 224,719 | 60% | 1.895 / 7.471 | 1.047 / 7.951 |

**Observations:** both servers hit their throughput ceiling by 10 clients and stay flat from there — the clone at ~135k req/s, Redis at ~225–240k. The clone holds ~60% of Redis's throughput at every concurrency level, and throughput doesn't degrade even at 500 connections; latency grows linearly with queue depth instead, exactly as expected for a saturated single-consumer pipeline. Notably, tail latency at 500 clients is a wash (clone p99 7.5ms vs Redis 8.0ms). The ceiling is the serialized command path: one commander goroutine plus a channel round-trip per command. Since GET work itself is trivial, most of the per-command budget is channel handoff and scheduler wakeups — batching replies or sharding the store would be the levers here.

<details>
<summary>Raw CSV: redis-clone</summary>

```
clients=1
"GET","48192.77","0.018","0.008","0.023","0.023","0.031","0.127"
clients=10
"GET","131752.31","0.050","0.016","0.055","0.071","0.087","0.399"
clients=50
"GET","134589.50","0.204","0.024","0.207","0.239","0.319","2.911"
clients=200
"GET","136054.42","0.772","0.288","0.767","0.895","1.479","1.703"
clients=500
"GET","134228.19","2.042","0.648","1.895","2.767","7.471","8.775"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

<details>
<summary>Raw CSV: Redis 8.10.0</summary>

```
clients=1
"GET","59952.04","0.013","0.008","0.015","0.023","0.023","0.383"
clients=10
"GET","228310.50","0.028","0.008","0.031","0.039","0.047","0.143"
clients=50
"GET","238663.48","0.112","0.040","0.111","0.127","0.207","0.407"
clients=200
"GET","226244.34","0.454","0.152","0.423","0.711","0.831","1.439"
clients=500
"GET","224719.11","1.195","0.440","1.047","1.239","7.951","12.175"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

---

## Test 3: Value-size sweep

**What it measures:** where time goes as payloads grow. Small values stress protocol parsing and dispatch overhead; large values stress RESP encoding, memory copies, and allocator/GC behavior.

**Command:**
```bash
redis-benchmark -p <port> -t set,get -n 20000 -c 50 -d {64,1024,10240,102400} --csv
```

**Results:**

| Value size | clone SET / GET req/s | Redis SET / GET req/s | clone vs Redis | clone p99 SET (ms) | Redis p99 SET (ms) |
|---|---|---|---|---|---|
| 64 B | 127,389 / 137,931 | 240,964 / 229,885 | 53% / 60% | 0.791 | 0.127 |
| 1 KB | 132,450 / 134,228 | 240,964 / 238,095 | 55% / 56% | 0.527 | 0.135 |
| 10 KB | 109,890 / 123,457 | 217,391 / 210,526 | 51% / 59% | 1.367 | 0.143 |
| 100 KB | 42,463 / 49,628 | 86,207 / 77,821 | 49% / 64% | 3.431 | 0.407 |

**Observations:** the throughput ratio stays remarkably flat — the clone runs at roughly 50–60% of Redis at every payload size, so per-byte handling isn't disproportionately worse than per-command handling. Both servers hold their small-value ceiling through 1KB and fall off a cliff at 100KB, where the workload turns bandwidth-bound (Redis at 86k × 100KB ≈ 8.6 GB/s through loopback). The real divergence is in tail latency: clone p99 grows from 0.8ms to 3.4ms across the sweep while Redis stays under 0.5ms throughout — the signature of large-allocation churn and GC pressure on big payloads rather than a slow hot path.

<details>
<summary>Raw CSV: redis-clone</summary>

```
size=64
"SET","127388.53","0.251","0.056","0.207","0.567","0.791","0.887"
"GET","137931.03","0.201","0.064","0.199","0.239","0.327","0.815"
size=1024
"SET","132450.34","0.215","0.040","0.207","0.335","0.527","0.727"
"GET","134228.19","0.208","0.040","0.207","0.247","0.463","1.063"
size=10240
"SET","109890.11","0.370","0.016","0.247","0.887","1.367","2.431"
"GET","123456.79","0.250","0.024","0.223","0.519","0.655","1.047"
size=102400
"SET","42462.85","1.102","0.040","0.959","2.543","3.431","5.743"
"GET","49627.79","0.674","0.032","0.567","1.639","2.311","4.047"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

<details>
<summary>Raw CSV: Redis 8.10.0</summary>

```
size=64
"SET","240963.86","0.109","0.040","0.111","0.127","0.127","0.151"
"GET","229885.06","0.122","0.056","0.111","0.231","0.319","0.407"
size=1024
"SET","240963.86","0.110","0.048","0.111","0.127","0.135","0.223"
"GET","238095.23","0.112","0.048","0.111","0.127","0.135","0.279"
size=10240
"SET","217391.30","0.121","0.040","0.127","0.135","0.143","0.271"
"GET","210526.31","0.125","0.064","0.127","0.143","0.151","0.335"
size=102400
"SET","86206.90","0.306","0.056","0.311","0.367","0.407","0.679"
"GET","77821.02","0.147","0.064","0.151","0.167","0.191","0.711"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

---

## Test 4: Data-structure commands

**What it measures:** list/set/hash write and pop performance. LPUSH/RPUSH also grow their target list continuously during the run (to ~100k elements), so this doubles as a test of how each structure behaves as it gets large.

**Command:**
```bash
redis-benchmark -p <port> -t lpush,rpush,lpop,sadd,hset -n 100000 -c 50
```

**Results:**

| Command | clone req/s | Redis req/s | clone vs Redis | clone avg / p99 (ms) | Redis avg / p99 (ms) |
|---|---|---|---|---|---|
| LPUSH | 6,607 | 207,039 | **3%** | 7.556 / 14.455 | 0.138 / 0.343 |
| RPUSH | 133,511 | 228,833 | 58% | 0.209 / 0.567 | 0.121 / 0.303 |
| LPOP | 137,931 | 230,415 | 60% | 0.197 / 0.263 | 0.120 / 0.319 |
| SADD | 136,612 | 232,558 | 59% | 0.198 / 0.263 | 0.117 / 0.279 |
| HSET | 137,174 | 237,530 | 59% | 0.198 / 0.271 | 0.198 / 0.287 |

**Observations:** four of five commands sit at the usual ~60% of Redis — but LPUSH craters to 3%, running 20x slower than the clone's own RPUSH and getting worse as the list grows. The cause is algorithmic, not overhead: the clone's list is a Go slice, and LPUSH prepends by copying the entire existing list behind the new elements (`append(new_list, ent.List...)`), making each LPUSH O(n) in list length — O(n²) across the benchmark as the list grows to 100k elements. Redis avoids this with a doubly-linked structure (quicklist) where both ends are O(1). This is the first result in the suite where the gap comes from data-structure choice rather than dispatch overhead.

<details>
<summary>Raw CSV: redis-clone</summary>

```
"LPUSH","6607.20","7.556","0.136","7.775","13.383","14.455","16.799"
"RPUSH","133511.34","0.209","0.032","0.199","0.239","0.567","1.735"
"LPOP","137931.03","0.197","0.088","0.199","0.231","0.263","0.519"
"SADD","136612.02","0.198","0.088","0.199","0.231","0.263","0.679"
"HSET","137174.22","0.198","0.056","0.199","0.231","0.271","0.591"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

<details>
<summary>Raw CSV: Redis 8.10.0</summary>

```
"LPUSH","207039.33","0.138","0.056","0.111","0.263","0.343","0.415"
"RPUSH","228832.95","0.121","0.064","0.111","0.207","0.303","0.439"
"LPOP","230414.75","0.120","0.048","0.111","0.207","0.319","0.407"
"SADD","232558.14","0.117","0.040","0.111","0.183","0.279","0.455"
"HSET","237529.69","0.114","0.048","0.111","0.151","0.287","0.463"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

---

## Test 5: Sorted sets (skiplist)

**What it measures:** ZADD insert performance into a growing sorted set (random scores and members, ~63k distinct members by end of run), then ZRANGE reading 100-element slices from it. This exercises the clone's skiplist implementation directly.

**Commands:**
```bash
redis-benchmark -p <port> -n 100000 -c 50 -r 100000 zadd zbench __rand_int__ member:__rand_int__
redis-benchmark -p <port> -n 20000 -c 50 zrange zbench 0 99
```

**Results:**

| Command | clone req/s | Redis req/s | clone vs Redis | clone p50 / p95 / p99 (ms) | Redis p50 / p95 / p99 (ms) |
|---|---|---|---|---|---|
| ZADD | 132,450 | 248,139 | 53% | 0.207 / 0.247 / 0.311 | 0.111 / 0.135 / 0.263 |
| ZRANGE 0 99 | 71,942 | 118,343 | 61% | 0.327 / 1.647 / 2.183 | 0.215 / 0.263 / 0.295 |

**Observations:** ZADD lands at 132k req/s — the same ~135k ceiling seen on GET, SET, SADD, and HSET. That means skiplist insertion isn't the bottleneck at all; even with ~63k members, inserts are absorbed inside the fixed dispatch budget. ZRANGE holds 61% of Redis on throughput, but its latency distribution is bimodal: p50 is a healthy 0.33ms while p95 jumps to 1.6ms (Redis stays flat at 0.26ms). Each reply carries 100 members, so the spikes point at reply-encoding allocations for multi-element arrays (and the GC they feed) rather than skiplist traversal, which would slow every request uniformly.

<details>
<summary>Raw CSV: redis-clone</summary>

```
"zadd zbench __rand_int__ member:__rand_int__","132450.33","0.208","0.056","0.207","0.247","0.311","0.631"
"zrange zbench 0 99","71942.45","0.479","0.120","0.327","1.647","2.183","4.471"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

<details>
<summary>Raw CSV: Redis 8.10.0</summary>

```
"zadd zbench __rand_int__ member:__rand_int__","248138.95","0.112","0.040","0.111","0.135","0.263","0.431"
"zrange zbench 0 99","118343.20","0.218","0.112","0.215","0.263","0.295","0.471"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>

---

## Test 6: AOF persistence cost

**What it measures:** the throughput cost of append-only-file durability at each fsync policy, on both servers. `everysec` flushes to disk once a second off the hot path; `always` makes every write wait for an fsync before replying.

**Commands:**
```bash
# servers restarted per phase with matching AOF flags:
#   clone: ./redis-clone -appendonly -appendfsync {everysec|always}
#   redis: redis-server --port 6380 --save '' --appendonly yes --appendfsync {everysec|always}
redis-benchmark -p <port> -t set -n 100000 -c 50 --csv   # everysec (100k requests)
redis-benchmark -p <port> -t set -n 10000 -c 50 --csv    # always (10k requests; fsync-bound)
```

**Results (SET, 50 clients):**

| Fsync policy | clone req/s | Redis req/s | clone vs Redis | clone avg latency (ms) | Redis avg latency (ms) |
|---|---|---|---|---|---|
| none (baseline, test 2/4) | ~135,000 | ~230,000 | ~59% | 0.2 | 0.1 |
| everysec | 133,156 | 254,453 | 52% | 0.209 | 0.109 |
| always | 235 | 3,135 | **8%** | 211.970 | 15.746 |

**Observations:** `everysec` is effectively free on both servers — the clone lost nothing versus its no-persistence baseline, confirming its background flush stays off the command path. `always` is where the architectures diverge hard: the clone collapses to 235 req/s (570x below its baseline, 13x slower than Redis's own `always` mode) with 212ms average latency. The numbers imply the clone performs one synchronous fsync per command inside the serialized commander loop (~235 fsyncs/s ≈ one 4ms fsync each, with 50 clients queued behind it). Redis in `always` mode instead does group commit: it batches all commands that arrived during one event-loop iteration into a single write+fsync, so with 50 concurrent clients one fsync can cover up to 50 commands — same durability guarantee, an order of magnitude more throughput. Batching concurrent writers into a shared fsync is the classic fix here.

<details>
<summary>Raw CSV</summary>

```
everysec, 100k requests:
clone: "SET","133155.80","0.209","0.016","0.207","0.247","0.447","2.487"
redis: "SET","254452.92","0.109","0.056","0.111","0.127","0.199","0.463"

always, 10k requests:
clone: "SET","235.30","211.970","5.496","201.087","256.255","389.375","443.391"
redis: "SET","3134.80","15.746","7.064","15.879","21.231","25.791","30.879"
(columns: test, rps, avg, min, p50, p95, p99, max latency ms)
```
</details>
