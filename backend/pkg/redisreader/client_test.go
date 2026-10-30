package redisreader

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

func TestReaderBounds(t *testing.T) {
	for _, wire := range []string{"$536870912\r\n", "*100000000\r\n", strings.Repeat("*1\r\n", 12) + "+x\r\n"} {
		reader := responseReader{reader: bufio.NewReader(strings.NewReader(wire)), remaining: maxResponseBytes, arraySlots: maxArraySlots}
		if _, err := reader.read(0); err == nil {
			t.Fatalf("unbounded response accepted: %q", wire[:min(len(wire), 30)])
		}
	}
}
func TestReadOnlyClient(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	commands := make(chan []string, 4)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := responseReader{reader: bufio.NewReader(conn), remaining: maxResponseBytes, arraySlots: maxArraySlots}
		for _, reply := range []string{"+OK\r\n", "+OK\r\n", "*2\r\n$1\r\n0\r\n*1\r\n$3\r\na\x00b\r\n"} {
			value, err := reader.read(0)
			if err != nil {
				return
			}
			args := []string{}
			for _, v := range value.Array {
				args = append(args, v.Text)
			}
			commands <- args
			conn.Write([]byte(reply))
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	client, err := Dial(context.Background(), Options{Host: "127.0.0.1", Port: address.Port, Username: "app", Password: "secret\r\n", Database: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	value, err := client.Read("SCAN", "0", "MATCH", "*", "COUNT", "100")
	if err != nil {
		t.Fatal(err)
	}
	if value.Array[1].Array[0].Text != "a\x00b" {
		t.Fatal(value)
	}
	auth := <-commands
	if len(auth) != 3 || auth[2] != "secret\r\n" {
		t.Fatal(auth)
	}
	selected := <-commands
	if selected[0] != "SELECT" || selected[1] != "2" {
		t.Fatal(selected)
	}
	for _, name := range []string{"SET", "DEL", "EVAL", "CONFIG", "KEYS", "FLUSHALL", "AUTH", "SELECT"} {
		if _, err := client.Read(name, "x"); err == nil {
			t.Fatalf("command %s not rejected", name)
		}
	}
}

func TestNestedArraysReserveBudgetBeforeAllocation(t *testing.T) {
	reader := responseReader{reader: bufio.NewReader(strings.NewReader(strings.Repeat("*4096\r\n", 4))), remaining: maxResponseBytes, arraySlots: maxArraySlots}
	if _, err := reader.read(0); err == nil || !strings.Contains(err.Error(), "嵌套项目") {
		t.Fatalf("aggregate allocation budget not enforced: %v", err)
	}
}
