# Redis Key browser

Goal: Let existing SQL workspace users browse/read Redis keys instead of seeing an empty SQL database tree.
Architecture: Dedicated read-only Redis panel for Redis-bound panes, bounded RESP2 reader, two authenticated DAS endpoints with existing SELECT grants per logical DB. No arbitrary command execution. Credentials stay server-side; reuse stored optional ACL/TLS settings. SCAN count is a hint and no returned keys are silently discarded. Binary keys use base64 IDs; binary values display base64. Values/chunks and collection pages are bounded; overlarge RESP responses fail before allocation.

API (POST, /api/v1/das):
- redis/scan: {instance_id, db:0, pattern:'*', cursor:'0', count:100} -> {keys:[{key,key_base64,type,ttl_seconds}], next_cursor:'0', has_more:false}.
- redis/value: {instance_id,db:0,key_base64,cursor:'0',offset:0,count:100} -> {key,key_base64,type,ttl_seconds,size,exists, value:{encoding:'utf8'|'base64',data:'',truncated:false}, rows:[{field?,value,index?,score?,id?}],next_cursor:'0',next_offset:0,has_more:false,message:''}. value only for string; row.value uses the same {encoding,data,truncated} shape; hash row.field is string with optional field_base64, set/zset row.value, list row.index. Stream preview optional explicit unsupported message if not implemented.

- [x] Failing tests for bounded protocol, scan cursors/binary keys, paged value types and authorization.
- [x] Implement read-only connection/client, DAS services/forms/routes/audit.
- [x] Add Redis panel with DB index, pattern/cursor paging, key metadata, value preview; integrate current instance/tab lifecycle and hide SQL-only controls/tree for Redis.
- [x] Validate backend tests, browser flows, production frontend and Linux build; document behavior/limits.

Validation: Redis reader and DAS Go tests passed; five frontend state tests passed; mocked browser flows verified Redis scanning/detail/paging, SQL switching and tab isolation; production frontend build passed. Linux compile checked separately. No live Redis server was used.
