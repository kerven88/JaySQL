set -euo pipefail

OUT_DIR="${1:-/tmp/goinsight-tools}"

MYSQL_VERSIONS=("5.7" "8.0")
PG_VERSIONS=("9.6" "10" "11" "12" "13" "14" "15" "16")

mkdir -p "$OUT_DIR"

copy_with_deps() {
  local image="$1"
  local bin_path="$2"
  local out_bin_dir="$3"
  local out_lib_dir="$4"

  mkdir -p "$out_bin_dir" "$out_lib_dir"

  local cid
  cid="$(docker create "$image" sh -lc "sleep 600")"
  docker cp "$cid":"$bin_path" "$out_bin_dir"/

  local deps
  deps="$(docker run --rm "$image" sh -lc "ldd '$bin_path' 2>/dev/null | awk '{print \\$3}' | grep '^/' | sort -u" || true)"
  if [ -n "$deps" ]; then
    while IFS= read -r dep; do
      [ -z "$dep" ] && continue
      docker cp "$cid":"$dep" "$out_lib_dir"/ || true
    done <<< "$deps"
  fi

  docker rm -f "$cid" >/dev/null
}

echo "== build mysql tools =="
for v in "${MYSQL_VERSIONS[@]}"; do
  image="mysql:${v}"
  base="$OUT_DIR/mysql/${v}"
  copy_with_deps "$image" "/usr/bin/mysql" "$base/bin" "$base/lib"
  copy_with_deps "$image" "/usr/bin/mysqldump" "$base/bin" "$base/lib"
done

echo "== build postgres tools =="
for v in "${PG_VERSIONS[@]}"; do
  image="postgres:${v}"
  base="$OUT_DIR/postgres/${v}"

  bindir="$(docker run --rm "$image" sh -lc 'pg_config --bindir' 2>/dev/null || true)"
  if [ -z "$bindir" ]; then
    bindir="/usr/lib/postgresql/${v}/bin"
  fi
  copy_with_deps "$image" "${bindir}/pg_dump" "$base/bin" "$base/lib"
  copy_with_deps "$image" "${bindir}/pg_restore" "$base/bin" "$base/lib"
  copy_with_deps "$image" "${bindir}/psql" "$base/bin" "$base/lib"
done

echo "== done =="
echo "output: $OUT_DIR"
