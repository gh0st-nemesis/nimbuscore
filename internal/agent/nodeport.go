package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

type portProxy struct {
	listener net.Listener
	target   string
	cancel   context.CancelFunc
}

type nodePortManager struct {
	mu     sync.Mutex
	active map[int32]*portProxy
}

func newNodePortManager() *nodePortManager {
	return &nodePortManager{active: make(map[int32]*portProxy)}
}

func (m *nodePortManager) reconcile(services []*v1.Service, selfIP string) {
	desired := make(map[int32]string)

	for _, s := range services {
		nodePort := s.GetSpec().GetNodePort()
		if nodePort == 0 {
			continue
		}
		targetPort := s.GetSpec().GetTargetPort()
		if targetPort == 0 {
			targetPort = s.GetSpec().GetPort()
		}
		if targetPort == 0 {
			continue
		}
		for _, ep := range s.GetStatus().GetEndpoints() {
			if ep.GetNodeIp() != selfIP {
				continue
			}
			desired[nodePort] = fmt.Sprintf("127.0.0.1:%d", targetPort)
			break
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for port, target := range desired {
		if existing, ok := m.active[port]; ok {
			if existing.target == target {
				continue
			}
			existing.cancel()
			existing.listener.Close()
			delete(m.active, port)
		}

		p, err := startPortProxy(port, target)
		if err != nil {
			log.Printf("agent: nodeport %d -> %s: %v", port, target, err)
			continue
		}
		m.active[port] = p
		log.Printf("agent: nodeport proxy listening on :%d -> %s", port, target)
	}

	for port, p := range m.active {
		if _, ok := desired[port]; !ok {
			p.cancel()
			p.listener.Close()
			delete(m.active, port)
			log.Printf("agent: nodeport proxy on :%d stopped (no longer needed)", port)
		}
	}
}

func (m *nodePortManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port, p := range m.active {
		p.cancel()
		p.listener.Close()
		delete(m.active, port)
	}
}

func startPortProxy(port int32, target string) (*portProxy, error) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go proxyConn(ctx, conn, target)
		}
	}()

	return &portProxy{listener: lis, target: target, cancel: cancel}, nil
}

func proxyConn(ctx context.Context, src net.Conn, targetAddr string) {
	defer src.Close()

	dst, err := net.Dial("tcp", targetAddr)
	if err != nil {
		return
	}
	defer dst.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(dst, src); done <- struct{}{} }() //nolint:errcheck
	go func() { io.Copy(src, dst); done <- struct{}{} }() //nolint:errcheck

	select {
	case <-done:
	case <-ctx.Done():
	}
}
