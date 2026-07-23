package agent

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func echoServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) //nolint:errcheck
			}(conn)
		}
	}()
	_, portStr, _ := net.SplitHostPort(lis.Addr().String())
	return portStr, func() { lis.Close() }
}

func dialAndEcho(t *testing.T, addr, msg string) string {
	t.Helper()
	var conn net.Conn
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.Dial("tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

func TestNodePortManagerProxiesToMatchingLocalEndpoint(t *testing.T) {
	targetPort, stop := echoServer(t)
	defer stop()

	targetPortInt, err := strconv.Atoi(targetPort)
	if err != nil {
		t.Fatalf("parse target port: %v", err)
	}

	nodePort := freeTCPPort(t)

	svc := &v1.Service{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:     &v1.ServiceSpec{Port: 80, TargetPort: int32(targetPortInt), NodePort: nodePort},
		Status: &v1.ServiceStatus{
			Endpoints: []*v1.ServiceEndpoint{{PodName: "web-0", NodeIp: "127.0.0.1", NodePort: nodePort}},
		},
	}

	m := newNodePortManager()
	defer m.stopAll()
	m.reconcile([]*v1.Service{svc}, "127.0.0.1")

	got := dialAndEcho(t, fmt.Sprintf("127.0.0.1:%d", nodePort), "hello")
	if got != "hello" {
		t.Errorf("echoed = %q, want %q", got, "hello")
	}
}

func TestNodePortManagerIgnoresServicesForOtherNodes(t *testing.T) {
	nodePort := freeTCPPort(t)

	svc := &v1.Service{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:     &v1.ServiceSpec{Port: 80, TargetPort: 8080, NodePort: nodePort},
		Status: &v1.ServiceStatus{
			Endpoints: []*v1.ServiceEndpoint{{PodName: "web-0", NodeIp: "10.0.0.99", NodePort: nodePort}},
		},
	}

	m := newNodePortManager()
	defer m.stopAll()
	m.reconcile([]*v1.Service{svc}, "127.0.0.1")

	if _, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", nodePort)); err == nil {
		t.Error("proxy is listening for a service with no endpoint on this node, want no listener")
	}
}

func TestNodePortManagerStopsRemovedService(t *testing.T) {
	targetPort, stop := echoServer(t)
	defer stop()

	targetPortInt, err := strconv.Atoi(targetPort)
	if err != nil {
		t.Fatalf("parse target port: %v", err)
	}

	nodePort := freeTCPPort(t)
	svc := &v1.Service{
		Metadata: &v1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:     &v1.ServiceSpec{Port: 80, TargetPort: int32(targetPortInt), NodePort: nodePort},
		Status: &v1.ServiceStatus{
			Endpoints: []*v1.ServiceEndpoint{{PodName: "web-0", NodeIp: "127.0.0.1", NodePort: nodePort}},
		},
	}

	m := newNodePortManager()
	defer m.stopAll()
	m.reconcile([]*v1.Service{svc}, "127.0.0.1")
	dialAndEcho(t, fmt.Sprintf("127.0.0.1:%d", nodePort), "hi")

	m.reconcile(nil, "127.0.0.1")
	time.Sleep(100 * time.Millisecond)

	if _, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", nodePort)); err == nil {
		t.Error("proxy still listening after the service was removed")
	}
}

func freeTCPPort(t *testing.T) int32 {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	return int32(lis.Addr().(*net.TCPAddr).Port)
}
