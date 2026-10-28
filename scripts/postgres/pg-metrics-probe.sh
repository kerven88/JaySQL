#!/usr/bin/env bash
#
# pg-metrics-probe.sh —— PostgreSQL 监控指标一次性探针
#
# 用途：不依赖 JaySQL，用 psql 直接跑一遍《PostgreSQL 监控指标》附件定义的全部指标，
#       用于上线前对账 / 现场排查（"平台上显示的值" vs "库里真实的值"）。
#
# 适用版本：PostgreSQL 10 – 18（pg_wal_lsn_diff、pg_stat_replication.replay_lag、
#           pg_stat_activity.backend_xid/backend_xmin 均为 10 起可用）
# 权限要求：采集账号需具备 pg_monitor 角色（或 superuser）：
#           GRANT pg_monitor TO <monitor_user>;
#
# 用法：
#   PGHOST=192.168.1.99 PGPORT=5432 PGUSER=postgres PGPASSWORD=xxx ./pg-metrics-probe.sh
#   ./pg-metrics-probe.sh --interval 10       # 采样两次（间隔 10 秒），输出每秒速率
#   ./pg-metrics-probe.sh --psql /usr/local/pgsql/bin/psql
#
# 输出：TSV（指标名 / 值 / 单位 / 类型）。counter 行在未指定 --interval 时输出累计值。
#
set -euo pipefail

PSQL_BIN="${PSQL:-psql}"
INTERVAL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --interval) INTERVAL="${2:?--interval 需要秒数}"; shift 2 ;;
    --psql)     PSQL_BIN="${2:?--psql 需要可执行文件路径}"; shift 2 ;;
    -h|--help)  sed -n '2,25p' "$0"; exit 0 ;;
    *)          echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

if ! command -v "$PSQL_BIN" >/dev/null 2>&1; then
  echo "找不到 psql（当前设置为 $PSQL_BIN），可用 --psql 指定路径或把 psql 加入 PATH" >&2
  exit 2
fi

# 一条 SQL 取回全部指标：CTE 分片 + UNION ALL 出长表，避免多次往返。
# 输出列：指标名 / 值 / 单位 / 类型（gauge=瞬时值，counter=累计计数器）
read -r -d '' PROBE_SQL <<'SQL' || true
WITH act AS (
    SELECT
        COUNT(*)                                                                                    AS total_session,
        COUNT(*) FILTER (WHERE state = 'active')                                                     AS active_session,
        COUNT(*) FILTER (WHERE state = 'idle in transaction')                                        AS idle_in_transaction,
        COUNT(*) FILTER (WHERE wait_event IS NOT NULL AND backend_type = 'client backend')          AS waiting_connection,
        COUNT(*) FILTER (WHERE state = 'idle in transaction' AND now() - state_change > interval '1 second')  AS idle_trx_1s,
        COUNT(*) FILTER (WHERE state = 'idle in transaction' AND now() - state_change > interval '3 seconds') AS idle_trx_3s,
        COUNT(*) FILTER (WHERE state = 'idle in transaction' AND now() - state_change > interval '5 seconds') AS idle_trx_5s,
        COUNT(*) FILTER (WHERE state = 'active' AND now() - query_start > interval '1 second')      AS long_query_1s,
        COUNT(*) FILTER (WHERE state = 'active' AND now() - query_start > interval '3 seconds')     AS long_query_3s,
        COUNT(*) FILTER (WHERE state = 'active' AND now() - query_start > interval '5 seconds')     AS long_query_5s
    FROM pg_stat_activity
), db AS (
    SELECT
        COALESCE(SUM(xact_commit), 0)   AS xact_commit,
        COALESCE(SUM(xact_rollback), 0) AS xact_rollback,
        COALESCE(SUM(deadlocks), 0)     AS deadlocks,
        COALESCE(SUM(tup_inserted), 0)  AS tup_inserted,
        COALESCE(SUM(tup_updated), 0)   AS tup_updated,
        COALESCE(SUM(tup_deleted), 0)   AS tup_deleted,
        COALESCE(SUM(tup_fetched), 0)   AS tup_fetched,
        COALESCE(SUM(tup_returned), 0)  AS tup_returned
    FROM pg_stat_database
    WHERE datname NOT IN ('template0', 'template1')
), slot_logical AS (
    SELECT MAX(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)) AS bytes
    FROM pg_replication_slots WHERE slot_type = 'logical'
), slot_all AS (
    SELECT MAX(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS bytes
    FROM pg_replication_slots
), rep AS (
    SELECT
        MAX(EXTRACT(EPOCH FROM replay_lag))            AS replay_lag_s,
        MAX(pg_wal_lsn_diff(sent_lsn, replay_lsn))     AS sent_lag_bytes
    FROM pg_stat_replication
), swell AS (
    WITH oldest AS (
        SELECT min(xact_start) AS m FROM pg_stat_activity
        WHERE backend_xid IS NOT NULL OR backend_xmin IS NOT NULL
    ), oldest_2pc AS (
        SELECT min(prepared) AS m FROM pg_prepared_xacts
    )
    SELECT EXTRACT(EPOCH FROM (now() - least(oldest.m, oldest_2pc.m))) AS s FROM oldest, oldest_2pc
), xid AS (
    SELECT COALESCE(MAX(age(datfrozenxid)), 0) AS max_age FROM pg_database
)
SELECT 'logical_rep_latency_in_mb', COALESCE(round((SELECT bytes FROM slot_logical) / 1024.0 / 1024, 2)::text, 'N/A'), 'MB', 'gauge'
UNION ALL SELECT 'max_slot_wal_delay_in_mb',   COALESCE(round((SELECT bytes FROM slot_all)     / 1024.0 / 1024, 2)::text, 'N/A'), 'MB', 'gauge'
UNION ALL SELECT 'avg_replay_delay',           COALESCE(round((SELECT replay_lag_s FROM rep)::numeric, 3)::text, 'N/A'),  's',  'gauge'
UNION ALL SELECT 'avg_sent_delay',             COALESCE(round((SELECT sent_lag_bytes FROM rep) / 1024.0 / 1024, 2)::text, 'N/A'), 'MB', 'gauge'
UNION ALL SELECT 'long_idle_in_transaction_1s', COALESCE((SELECT idle_trx_1s FROM act)::text, 'N/A'),  'count', 'gauge'
UNION ALL SELECT 'long_idle_in_transaction_3s', COALESCE((SELECT idle_trx_3s FROM act)::text, 'N/A'),  'count', 'gauge'
UNION ALL SELECT 'long_idle_in_transaction_5s', COALESCE((SELECT idle_trx_5s FROM act)::text, 'N/A'),  'count', 'gauge'
UNION ALL SELECT 'long_query_1s',               COALESCE((SELECT long_query_1s FROM act)::text, 'N/A'), 'count', 'gauge'
UNION ALL SELECT 'long_query_3s',               COALESCE((SELECT long_query_3s FROM act)::text, 'N/A'), 'count', 'gauge'
UNION ALL SELECT 'long_query_5s',               COALESCE((SELECT long_query_5s FROM act)::text, 'N/A'), 'count', 'gauge'
UNION ALL SELECT 'swell_time',                  COALESCE(round((SELECT s FROM swell)::numeric, 1)::text, 'N/A'), 's', 'gauge'
UNION ALL SELECT 'xact_commit',                 COALESCE((SELECT xact_commit FROM db)::text, 'N/A'),   'count', 'counter'
UNION ALL SELECT 'xact_rollback',               COALESCE((SELECT xact_rollback FROM db)::text, 'N/A'), 'count', 'counter'
UNION ALL SELECT 'deadlocks',                   COALESCE((SELECT deadlocks FROM db)::text, 'N/A'),     'count', 'counter'
UNION ALL SELECT 'tup_inserted',                COALESCE((SELECT tup_inserted FROM db)::text, 'N/A'),  'rows',  'counter'
UNION ALL SELECT 'tup_updated',                 COALESCE((SELECT tup_updated FROM db)::text, 'N/A'),   'rows',  'counter'
UNION ALL SELECT 'tup_deleted',                 COALESCE((SELECT tup_deleted FROM db)::text, 'N/A'),   'rows',  'counter'
UNION ALL SELECT 'tup_fetched',                 COALESCE((SELECT tup_fetched FROM db)::text, 'N/A'),   'rows',  'counter'
UNION ALL SELECT 'tup_returned',                COALESCE((SELECT tup_returned FROM db)::text, 'N/A'),  'rows',  'counter'
UNION ALL SELECT 'active_session',              COALESCE((SELECT active_session FROM act)::text, 'N/A'),      'count', 'gauge'
UNION ALL SELECT 'total_session',               COALESCE((SELECT total_session FROM act)::text, 'N/A'),       'count', 'gauge'
UNION ALL SELECT 'idle_in_transaction',         COALESCE((SELECT idle_in_transaction FROM act)::text, 'N/A'), 'count', 'gauge'
UNION ALL SELECT 'waiting_connection',          COALESCE((SELECT waiting_connection FROM act)::text, 'N/A'),  'count', 'gauge'
UNION ALL SELECT 'max_age',                     COALESCE((SELECT max_age FROM xid)::text, 'N/A'),             'xid',   'gauge'
ORDER BY 1
SQL

run_probe() {
  "$PSQL_BIN" -X -A -t -F "$(printf '\t')" -v ON_ERROR_STOP=1 -c "$PROBE_SQL" "${PSQL_DBNAME_OVERRIDE[@]}" 2>&1
}

if [[ -z "${PGDATABASE:-}" ]]; then
  PSQL_DBNAME_OVERRIDE=(-d "${PGDATABASE_DEFAULT:-postgres}")
else
  PSQL_DBNAME_OVERRIDE=()
fi

if [[ "$INTERVAL" == "0" ]]; then
  echo -e "# 指标\t值\t单位\t类型     # counter 行为累计值，加 --interval N 可换算每秒速率"
  run_probe
  exit 0
fi

s1=$(mktemp); s2=$(mktemp)
trap 'rm -f "$s1" "$s2"' EXIT
run_probe >"$s1"
sleep "$INTERVAL"
run_probe >"$s2"

echo -e "# 指标\t每秒速率\t单位\t类型     # 采样间隔 ${INTERVAL}s"
awk -F'\t' -v interval="$INTERVAL" '
  NR==FNR { v1[$1]=$2; next }
  {
    name=$1; v2=$2; unit=$3; kind=$4
    if (name=="xact_commit")   { c2=v2 }
    if (name=="xact_rollback") { r2=v2 }
    if (kind=="counter") {
      if (v1[name]=="N/A" || v2=="N/A") { printf "%s\tN/A\t%s\tcounter\n", name, unit; next }
      printf "%s\t%.3f\t%s\tper_sec\n", name, (v2-v1[name])/interval, (unit=="rows" ? "rows/s" : "count/s")
    } else {
      printf "%s\t%s\t%s\tgauge\n", name, v2, unit
    }
  }
  END {
    # tps = (commit2 + rollback2 - commit1 - rollback1) / interval，附件口径
    if (v1["xact_commit"]!="N/A" && v1["xact_rollback"]!="N/A" && c2!="N/A" && r2!="N/A") {
      printf "tps\t%.3f\tcount/s\tper_sec\n", ((c2 + r2) - (v1["xact_commit"] + v1["xact_rollback"])) / interval
    }
  }
' "$s1" "$s2"
