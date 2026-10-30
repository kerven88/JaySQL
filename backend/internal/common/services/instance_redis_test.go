package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin/binding"
	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func readRedisTestCommand(reader *bufio.Reader) ([]string, error) {
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
func redisProbeServer(t *testing.T, commands chan []string, failAuth bool) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			args, err := readRedisTestCommand(reader)
			if err != nil {
				return
			}
			commands <- args
			if args[0] == "AUTH" {
				if failAuth {
					fmt.Fprint(conn, "-WRONGPASS invalid username-password pair\r\n")
				} else {
					fmt.Fprint(conn, "+OK\r\n")
				}
			} else {
				fmt.Fprint(conn, "+PONG\r\n")
				return
			}
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", address.Port
}
func TestRedisProbeAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, user, password string
		wantAuth             int
	}{{"none", "", "", 0}, {"password", "", "p@ss", 2}, {"acl", "app", "p@ss", 3}, {"acl-nopass", "app", "", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			commands := make(chan []string, 4)
			host, port := redisProbeServer(t, commands, false)
			report, err := testConnectionMode(context.Background(), ProbeModeLightweight, "Redis", host, port, tc.user, tc.password, nil)
			if err != nil || report == nil {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			cmd := <-commands
			if tc.wantAuth > 0 {
				if cmd[0] != "AUTH" || len(cmd) != tc.wantAuth || cmd[len(cmd)-1] != tc.password {
					t.Fatal(cmd)
				}
				cmd = <-commands
			}
			if len(cmd) != 1 || cmd[0] != "PING" {
				t.Fatal(cmd)
			}
		})
	}
	commands := make(chan []string, 4)
	host, port := redisProbeServer(t, commands, true)
	_, err := testConnection(context.Background(), "Redis", host, port, "app", "very-secret", nil)
	if err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("unsafe auth result: %v", err)
	}
}
func TestRedisProbeTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			time.Sleep(300 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = testConnection(ctx, "Redis", "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "", "", nil)
	if err == nil {
		t.Fatal("stalled Redis probe succeeded")
	}
}
func TestRedisInstanceFormValidation(t *testing.T) {
	form := forms.AdminCreateInstancesForm{Hostname: "redis.example.com", Port: 6379, DbType: models.EnumType("Redis"), Environment: 1, Remark: "redis-test"}
	if err := binding.Validator.ValidateStruct(form); err != nil {
		t.Fatal(err)
	}
	form.DbType = "MySQL"
	if err := binding.Validator.ValidateStruct(form); err == nil {
		t.Fatal("MySQL credentials unexpectedly optional")
	}
}
func TestNormalizeRedisExtraConfig(t *testing.T) {
	got, err := normalizeInstanceExtraConfig("Redis", map[string]any{"redis_tls": true}, "create")
	if err != nil || !strings.Contains(string(got), `"redis_tls":true`) {
		t.Fatalf("%s %v", got, err)
	}
}

func TestRedisEditProbePreservesCredentialsSafely(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE insight_instances (id INTEGER PRIMARY KEY, hostname TEXT, port INTEGER, user TEXT, password TEXT, db_type TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	oldDB := global.App.DB
	global.App.DB = db
	t.Cleanup(func() { global.App.DB = oldDB })
	commands := make(chan []string, 4)
	host, port := redisProbeServer(t, commands, false)
	if err := db.Exec("INSERT INTO insight_instances (id,hostname,port,user,password,db_type) VALUES (1,?,?,?,?,?)", host, port, "app", "saved-secret", "Redis").Error; err != nil {
		t.Fatal(err)
	}
	service := AdminTestInstanceConnectionByPayloadService{C: context.Background(), ID: 1, DbType: "Redis", Hostname: host, Port: port, User: "app"}
	if _, err := service.Run(); err != nil {
		t.Fatal(err)
	}
	if command := <-commands; len(command) != 3 || command[2] != "saved-secret" {
		t.Fatal(command)
	}
	denied := AdminTestInstanceConnectionByPayloadService{C: context.Background(), ID: 1, DbType: "Redis", Hostname: "other.example.com", Port: port, User: "app"}
	if _, err := denied.Run(); err == nil || strings.Contains(err.Error(), "saved-secret") {
		t.Fatalf("saved credential destination guard: %v", err)
	}
	commands = make(chan []string, 4)
	host, port = redisProbeServer(t, commands, false)
	cleared := AdminTestInstanceConnectionByPayloadService{C: context.Background(), ID: 1, DbType: "Redis", Hostname: host, Port: port, ClearPassword: true}
	if _, err := cleared.Run(); err != nil {
		t.Fatal(err)
	}
	if command := <-commands; command[0] != "PING" {
		t.Fatal("cleared credentials still authenticated", command)
	}
}
func TestRedisTLSRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	address := server.Listener.Addr().(*net.TCPAddr)
	_, err := testConnection(context.Background(), "Redis", "127.0.0.1", address.Port, "", "", []byte(`{"redis_tls":true}`))
	if err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	if _, err := normalizeInstanceExtraConfig("Redis", map[string]any{"redis_tls": "true"}, "create"); err == nil {
		t.Fatal("non-boolean TLS option accepted")
	}
}

func TestRedisUnicodePasswordFitsEncryptedStorage(t *testing.T) {
	t.Cleanup(func() { _ = secret.LoadKey() })
	t.Setenv(secret.EnvKey, "12345678901234567890123456789012")
	if err := secret.LoadKey(); err != nil {
		t.Fatal(err)
	}
	field, ok := reflect.TypeOf(models.InsightInstances{}).FieldByName("Password")
	if !ok {
		t.Fatal("password field missing")
	}
	var capacity int
	if _, err := fmt.Sscanf(strings.Split(field.Tag.Get("gorm"), ";")[0], "type:varchar(%d)", &capacity); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{strings.Repeat("密", 256), strings.Repeat("😀", 256)} {
		form := forms.AdminCreateInstancesForm{Hostname: "redis.example.com", Port: 6379, DbType: "Redis", Password: password, Environment: 1, Remark: "redis-test"}
		if err := binding.Validator.ValidateStruct(form); err != nil {
			t.Fatal(err)
		}
		encrypted, err := secret.Encrypt(password)
		if err != nil {
			t.Fatal(err)
		}
		if len(encrypted) > capacity {
			t.Fatalf("accepted password needs %d encrypted characters; column capacity %d", len(encrypted), capacity)
		}
		plain, err := secret.Decrypt(encrypted)
		if err != nil || plain != password {
			t.Fatal("encrypted password roundtrip failed")
		}
	}
}
