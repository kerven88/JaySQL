backup tools layout

Place database client binaries next to `JaySQL` or `backup-agent` under a `tools/` directory.

Recommended layout:

```text
tools/
  mysql/
    5.7/
      bin/
        mysql
        mysqldump
      lib/
        (shared libraries from ldd)
    8.0/
      bin/
        mysql
        mysqldump
      lib/
        (shared libraries from ldd)
  postgres/
    9.6/
      bin/
        pg_dump
        pgbackrest
        pg_restore
        psql
      lib/
        (shared libraries from ldd)
    10/
      bin/
        pg_dump
        pgbackrest
        pg_restore
        psql
      lib/
        (shared libraries from ldd)
    11/
      ...
    12/
      ...
    13/
      ...
    14/
      ...
    15/
      ...
    16/
      ...
```

Resolution order:

1. `GOINSIGHT_BACKUP_TOOL_HOME`
2. `<executable_dir>/tools`
3. `<executable_dir>/backup-tools`
4. current working directory variants
5. system `PATH`

The runtime picks the closest matching version first and falls back to the remaining bundled versions.
