set -euo pipefail

NODE="${1:-192.168.53.102}"
USER="${2:-hs-ops}"
PASS="${3:-Hsmap@2026}"
SRC_DIR="${4:-/tmp/goinsight-tools}"
DEST_DIR="/usr/local/goinsight/tools"

tar_path="/tmp/goinsight-tools.tar.gz"

if [ ! -d "$SRC_DIR" ]; then
  echo "src dir not found: $SRC_DIR" >&2
  exit 1
fi

tar -czf "$tar_path" -C "$SRC_DIR" .

sshpass -p "$PASS" scp -o PubkeyAuthentication=no -o PasswordAuthentication=yes -o PreferredAuthentications=password -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$tar_path" "${USER}@${NODE}:/tmp/goinsight-tools.tar.gz"

sshpass -p "$PASS" ssh -tt -o PubkeyAuthentication=no -o PasswordAuthentication=yes -o PreferredAuthentications=password -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "${USER}@${NODE}" "printf '%s\n' '$PASS' | sudo -S mkdir -p '$DEST_DIR' && printf '%s\n' '$PASS' | sudo -S tar -xzf /tmp/goinsight-tools.tar.gz -C '$DEST_DIR' && printf '%s\n' '$PASS' | sudo -S find '$DEST_DIR' -type f -perm -u+x -o -type f -name 'pg_dump' -o -type f -name 'pg_restore' -o -type f -name 'psql' -o -type f -name 'mysql' -o -type f -name 'mysqldump' | head -n 20"

echo "installed to: ${USER}@${NODE}:${DEST_DIR}"
