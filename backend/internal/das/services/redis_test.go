package services

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin/binding"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/pkg/redisreader"
)

type redisFixture struct {
	read func(string, ...string) (redisreader.Value, error)
	many func([][]string) ([]redisreader.Value, error)
}

func (f redisFixture) Read(cmd string, args ...string) (redisreader.Value, error) {
	return f.read(cmd, args...)
}
func (f redisFixture) ReadMany(cmd [][]string) ([]redisreader.Value, error) { return f.many(cmd) }
func redisText(s string) redisreader.Value                                  { return redisreader.Value{Kind: '$', Text: s} }
func redisArray(v ...redisreader.Value) redisreader.Value {
	return redisreader.Value{Kind: '*', Array: v}
}
func redisMetadata(kind string, ttl int64) []redisreader.Value {
	return []redisreader.Value{{Kind: '+', Text: kind}, {Kind: ':', Integer: ttl}}
}
func TestRedisScanCursorAndBinaryKey(t *testing.T) {
	raw := "a\x00\xff"
	fixture := redisFixture{read: func(cmd string, args ...string) (redisreader.Value, error) {
		if cmd != "SCAN" || args[0] != "18446744073709551615" || args[2] != "user:*" {
			t.Fatalf("%s %v", cmd, args)
		}
		return redisArray(redisText("12"), redisArray(redisText(raw), redisText(""))), nil
	}, many: func(cmd [][]string) ([]redisreader.Value, error) {
		if cmd[0][1] != raw || cmd[2][1] != "" {
			t.Fatal(cmd)
		}
		return append(redisMetadata("string", -1), redisMetadata("hash", 30)...), nil
	}}
	result, err := scanRedisKeys(fixture, forms.RedisScanForm{Pattern: "user:*", Cursor: "18446744073709551615"})
	if err != nil || !result.HasMore || len(result.Keys) != 2 || result.NextCursor != "12" {
		t.Fatalf("%+v %v", result, err)
	}
	if result.Keys[0].KeyBase64 != base64.StdEncoding.EncodeToString([]byte(raw)) || result.Keys[1].KeyBase64 != "" {
		t.Fatal(result.Keys)
	}
	fixture.read = func(string, ...string) (redisreader.Value, error) {
		return redisArray(redisText("99"), redisArray()), nil
	}
	empty, err := scanRedisKeys(fixture, forms.RedisScanForm{})
	if err != nil || !empty.HasMore || len(empty.Keys) != 0 {
		t.Fatal(empty, err)
	}
}
func TestRedisPagedValueTypes(t *testing.T) {
	cases := []struct {
		kind     string
		response redisreader.Value
		size     int64
	}{{"string", redisText("\xff\x00"), 100000}, {"list", redisArray(redisText("item")), 2}, {"hash", redisArray(redisText("7"), redisArray(redisText("field"), redisText("value"))), 10}, {"set", redisArray(redisText("7"), redisArray(redisText("member"))), 10}, {"zset", redisArray(redisText("7"), redisArray(redisText("member"), redisText("+inf"))), 10}}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			fixture := redisFixture{many: func([][]string) ([]redisreader.Value, error) { return redisMetadata(tc.kind, -1), nil }, read: func(cmd string, args ...string) (redisreader.Value, error) {
				if strings.HasSuffix(cmd, "LEN") || cmd == "SCARD" || cmd == "ZCARD" {
					return redisreader.Value{Kind: ':', Integer: tc.size}, nil
				}
				return tc.response, nil
			}}
			result, err := readRedisValue(fixture, "test", forms.RedisValueForm{Count: 1})
			if err != nil || !result.Exists || !result.HasMore {
				t.Fatal(result, err)
			}
			if tc.kind == "string" {
				if result.Value.Encoding != "base64" || !result.Value.Truncated || result.NextOffset != 2 {
					t.Fatal(result)
				}
			} else if len(result.Rows) != 1 {
				t.Fatal(result)
			}
		})
	}
}
func TestRedisWholeDatabaseGrants(t *testing.T) {
	db := setupDataGrantTestDB(t)
	scope := testScope()
	scope.IsSuperuser = true
	if ok, err := hasWholeDatabaseGrant(scope, "redis-inst", "0", DataActionSelect); err != nil || ok {
		t.Fatal("ungranted admin allowed", ok, err)
	}
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: "redis-inst", Schema: "0", Effect: DataEffectAllow})
	if ok, err := hasWholeDatabaseGrant(scope, "redis-inst", "0", DataActionSelect); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := hasWholeDatabaseGrant(scope, "redis-inst", "1", DataActionSelect); err != nil || ok {
		t.Fatal("cross DB access", ok, err)
	}
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelTable, InstanceID: "redis-inst", Schema: "0", Table: "secret", Effect: DataEffectDeny})
	if ok, err := hasWholeDatabaseGrant(scope, "redis-inst", "0", DataActionSelect); err != nil || ok {
		t.Fatal("narrow deny bypassed by scan", ok, err)
	}
}

func readRedisRequestForTest(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
		if err != nil {
			return nil, err
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		args = append(args, string(data[:size]))
	}
	return args, nil
}
func TestRedisScanServiceAuthorizationAndStoredCredentials(t *testing.T) {
	db := setupDataGrantTestDB(t)
	seedInstanceGrantActor(t, db)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	commands := make(chan []string, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for _, reply := range []string{"+OK\r\n", "*2\r\n$1\r\n0\r\n*1\r\n$3\r\na\x00b\r\n", "+string\r\n", ":-1\r\n"} {
			args, err := readRedisRequestForTest(reader)
			if err != nil {
				return
			}
			commands <- args
			conn.Write([]byte(reply))
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := db.Exec("CREATE TABLE IF NOT EXISTS insight_instances (instance_id TEXT,hostname TEXT,port INTEGER,user TEXT,password TEXT,db_type TEXT,extra_config TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("DELETE FROM insight_instances")
	if err := db.Exec("INSERT INTO insight_instances VALUES ('redis-inst','127.0.0.1',?,'','saved-secret','Redis','{}')", port).Error; err != nil {
		t.Fatal(err)
	}
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelDatabase, InstanceID: "redis-inst", Schema: "0", Effect: DataEffectAllow})
	result, err := ScanRedisKeys(context.Background(), "tester", forms.RedisScanForm{InstanceID: "redis-inst"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Keys) != 1 || result.Keys[0].KeyBase64 != "YQBi" {
		t.Fatal(result)
	}
	auth := <-commands
	if len(auth) != 2 || auth[0] != "AUTH" || auth[1] != "saved-secret" {
		t.Fatal(auth)
	}
	<-done
	if _, err := ScanRedisKeys(context.Background(), "tester", forms.RedisScanForm{InstanceID: "redis-inst", DB: 1}); !errors.Is(err, ErrNoDataGrant) {
		t.Fatalf("ungranted DB not denied before network: %v", err)
	}
}
func TestRedisReadEmptyBase64KeyForm(t *testing.T) {
	key := ""
	form := forms.RedisValueForm{InstanceID: "redis-inst", KeyBase64: &key}
	if err := binding.Validator.ValidateStruct(form); err != nil {
		t.Fatal(err)
	}
	form.KeyBase64 = nil
	if err := binding.Validator.ValidateStruct(form); err == nil {
		t.Fatal("missing Key accepted")
	}
}

func TestRedisValueResponseKeepsEmptyFieldAndZeroIndex(t *testing.T) {
	raw, err := json.Marshal(RedisValueRow{Field: redisFieldPointer(""), Index: redisIndexPointer(0), Value: redisData("\x00")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"field":""`) || !strings.Contains(string(raw), `"index":0`) || !strings.Contains(string(raw), `"encoding":"base64"`) {
		t.Fatal(string(raw))
	}
}
func TestRedisDisappearingValueDoesNotRepeatOffset(t *testing.T) {
	for _, kind := range []string{"string", "list"} {
		fixture := redisFixture{many: func([][]string) ([]redisreader.Value, error) { return redisMetadata(kind, -1), nil }, read: func(cmd string, args ...string) (redisreader.Value, error) {
			if cmd == "STRLEN" || cmd == "LLEN" {
				return redisreader.Value{Kind: ':', Integer: 20}, nil
			}
			if kind == "string" {
				return redisText(""), nil
			}
			return redisArray(), nil
		}}
		value, err := readRedisValue(fixture, "expired", forms.RedisValueForm{})
		if err != nil || value.HasMore {
			t.Fatalf("%s: %+v %v", kind, value, err)
		}
	}
}
