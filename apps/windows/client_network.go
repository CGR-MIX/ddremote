package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const networkClientHost = "127.0.0.2"

type networkClientBridge struct {
	run       *networkRun
	remote    string
	cancel    context.CancelFunc
	listeners []interface{ Close() error }
	wg        sync.WaitGroup
	once      sync.Once
	pairMu    sync.RWMutex
	pairTag   string
}

// DialContext allows companion HTTPS requests to use the embedded tailnet.
// Use it as http.Transport.DialContext while retaining normal/pinned TLS
// certificate verification in the transport.
func (m *NetworkManager) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m.mu.RLock()
	r, running := m.run, m.status.Running
	m.mu.RUnlock()
	if r == nil || !running || r.ctx.Err() != nil {
		return nil, errors.New("请先登录并连接安全网络")
	}
	return r.server.Dial(ctx, network, address)
}

// PrepareClient makes a native Moonlight process able to reach another DD
// node through this process's userspace tailnet. The returned loopback host
// retains GameStream's normal ports. Call it only for a user-selected target:
// choosing a different target closes the previous client session.
func (m *NetworkManager) PrepareClient(ctx context.Context, remote string) (string, error) {
	remote, err := normalizeNetworkClientRemote(remote)
	if err != nil {
		return "", err
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.RLock()
	r, running := m.run, m.status.Running
	m.mu.RUnlock()
	if r == nil || !running || r.ctx.Err() != nil {
		return "", errors.New("请先登录并连接安全网络")
	}
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil && m.client.run == r && m.client.remote == remote {
		return networkClientHost, nil
	}
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	bridge, err := startNetworkClientBridge(r.ctx, remote, r.server.Dial)
	if err != nil {
		return "", err
	}
	bridge.run = r
	m.client = bridge
	return networkClientHost, nil
}

func (m *NetworkManager) CloseClient() {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
}

func (m *NetworkManager) closeClientForRun(r *networkRun) {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil && m.client.run == r {
		m.client.Close()
		m.client = nil
	}
}

func normalizeNetworkClientRemote(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "[") && strings.HasSuffix(remote, "]") {
		remote = strings.TrimSuffix(strings.TrimPrefix(remote, "["), "]")
	}
	if ip, err := netip.ParseAddr(remote); err == nil {
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
			return "", errors.New("请选择远端安全网络地址")
		}
		return ip.String(), nil
	}
	if remote == "" || len(remote) > 253 {
		return "", errors.New("远端设备名称或 IP 地址无效")
	}
	for _, label := range strings.Split(strings.TrimSuffix(remote, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("远端设备名称或 IP 地址无效")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("请输入设备 IP 或安全网络名称，不含协议和端口")
			}
		}
	}
	return strings.TrimSuffix(strings.ToLower(remote), "."), nil
}

type networkDialFunc func(context.Context, string, string) (net.Conn, error)

func startNetworkClientBridge(ctx context.Context, remote string, dial networkDialFunc) (*networkClientBridge, error) {
	ctx, cancel := context.WithCancel(ctx)
	b := &networkClientBridge{cancel: cancel, remote: remote}
	for _, p := range networkForwardPorts {
		if p.port == 48200 {
			// The local DD file receiver may keep this port open on every
			// interface. Companion requests use DialContext directly instead.
			continue
		}
		address := net.JoinHostPort(networkClientHost, fmt.Sprint(p.port))
		target := net.JoinHostPort(remote, fmt.Sprint(p.port))
		if p.network == "tcp" {
			ln, err := net.Listen("tcp4", address)
			if err != nil {
				b.Close()
				return nil, fmt.Errorf("本机控制代理端口被占用 (%s): %w", address, err)
			}
			b.listeners = append(b.listeners, ln)
			b.wg.Add(1)
			if p.port == 47989 {
				server, transport := newNetworkPairProxy(ctx, target, dial, b.currentPairTag)
				b.listeners = append(b.listeners, server)
				go func() { defer b.wg.Done(); defer transport.CloseIdleConnections(); _ = server.Serve(ln) }()
			} else {
				go func() { defer b.wg.Done(); serveNetworkClientTCP(ctx, ln, target, dial, &b.wg) }()
			}
		} else {
			addr, err := net.ResolveUDPAddr("udp4", address)
			if err != nil {
				b.Close()
				return nil, err
			}
			pc, err := net.ListenUDP("udp4", addr)
			if err != nil {
				b.Close()
				return nil, fmt.Errorf("本机控制代理端口被占用 (%s): %w", address, err)
			}
			b.listeners = append(b.listeners, pc)
			b.wg.Add(1)
			go func() { defer b.wg.Done(); serveNetworkClientUDP(ctx, pc, target, dial, &b.wg, 90*time.Second) }()
		}
	}
	return b, nil
}

func (b *networkClientBridge) Close() {
	b.once.Do(func() {
		b.cancel()
		for _, listener := range b.listeners {
			listener.Close()
		}
		b.wg.Wait()
	})
}

func serveNetworkClientTCP(ctx context.Context, ln net.Listener, target string, dial networkDialFunc, wg *sync.WaitGroup) {
	for {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			client.Close()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer client.Close()
			dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			remote, err := dial(dialCtx, "tcp", target)
			cancel()
			if err != nil {
				return
			}
			defer remote.Close()
			bridgeNetworkStreams(ctx, client, remote)
		}()
	}
}

type networkClientUDPFlow struct {
	ctx    context.Context
	cancel context.CancelFunc
	input  chan []byte
	peer   *net.UDPAddr
}

// Each local client source port gets its own tailnet flow. This is required
// for separate Moonlight audio/video/control sessions and reconnections.
func serveNetworkClientUDP(ctx context.Context, socket *net.UDPConn, target string, dial networkDialFunc, wg *sync.WaitGroup, idle time.Duration) {
	var mu sync.Mutex
	flows := make(map[netip.AddrPort]*networkClientUDPFlow)
	defer func() {
		mu.Lock()
		for _, flow := range flows {
			flow.cancel()
		}
		mu.Unlock()
	}()
	buf := make([]byte, 65535)
	for {
		n, peer, err := socket.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		if !peer.IP.IsLoopback() {
			continue
		}
		key := peer.AddrPort()
		mu.Lock()
		flow := flows[key]
		if flow != nil && flow.ctx.Err() != nil {
			flow = nil
		}
		if flow == nil {
			flowCtx, cancel := context.WithCancel(ctx)
			flow = &networkClientUDPFlow{ctx: flowCtx, cancel: cancel, input: make(chan []byte, 64), peer: peer}
			flows[key] = flow
			wg.Add(1)
			go func(flow *networkClientUDPFlow) {
				defer wg.Done()
				defer func() {
					mu.Lock()
					if flows[key] == flow {
						delete(flows, key)
					}
					mu.Unlock()
				}()
				runNetworkClientUDPFlow(flow, socket, target, dial, idle)
			}(flow)
		}
		mu.Unlock()
		packet := append([]byte{}, buf[:n]...)
		select {
		case flow.input <- packet:
		case <-flow.ctx.Done():
		default: // UDP is lossy; drop overload instead of accumulating latency.
		}
	}
}

func runNetworkClientUDPFlow(flow *networkClientUDPFlow, socket *net.UDPConn, target string, dial networkDialFunc, idle time.Duration) {
	defer flow.cancel()
	dialCtx, cancel := context.WithTimeout(flow.ctx, 10*time.Second)
	remote, err := dial(dialCtx, "udp", target)
	cancel()
	if err != nil {
		return
	}
	defer remote.Close()
	stop := context.AfterFunc(flow.ctx, func() { remote.Close() })
	defer stop()
	remote.SetReadDeadline(time.Now().Add(idle))
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer flow.cancel()
		buf := make([]byte, 65535)
		for {
			n, err := remote.Read(buf)
			if err != nil {
				return
			}
			remote.SetReadDeadline(time.Now().Add(idle))
			if _, err = socket.WriteToUDP(buf[:n], flow.peer); err != nil {
				return
			}
		}
	}()
	defer func() { remote.Close(); <-readDone }()
	for {
		select {
		case <-flow.ctx.Done():
			return
		case packet := <-flow.input:
			remote.SetReadDeadline(time.Now().Add(idle))
			remote.SetWriteDeadline(time.Now().Add(10 * time.Second))
			n, err := remote.Write(packet)
			if err != nil || n != len(packet) {
				return
			}
		}
	}
}
