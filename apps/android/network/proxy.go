package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

const clientHost = "127.0.0.2"

type dialFunc func(context.Context, string, string) (net.Conn, error)
type clientBridge struct {
	remote    string
	cancel    context.CancelFunc
	listeners []io.Closer
	wg        sync.WaitGroup
	once      sync.Once
}

func startClientBridge(parent context.Context, remote string, dial dialFunc) (*clientBridge, error) {
	ctx, cancel := context.WithCancel(parent)
	b := &clientBridge{remote: remote, cancel: cancel}
	// Sunshine's default GameStream ports, plus pinned companion HTTPS.
	for _, port := range []int{47984, 47989, 48010, 48200} {
		ln, err := net.Listen("tcp4", net.JoinHostPort(clientHost, fmt.Sprint(port)))
		if err != nil {
			b.Close()
			return nil, err
		}
		b.listeners = append(b.listeners, ln)
		b.wg.Add(1)
		go func() { defer b.wg.Done(); serveTCP(ctx, ln, net.JoinHostPort(remote, fmt.Sprint(port)), dial, &b.wg) }()
	}
	for _, port := range []int{47998, 47999, 48000, 48002, 48010} {
		pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(clientHost), Port: port})
		if err != nil {
			b.Close()
			return nil, err
		}
		b.listeners = append(b.listeners, pc)
		b.wg.Add(1)
		go func() { defer b.wg.Done(); serveUDP(ctx, pc, net.JoinHostPort(remote, fmt.Sprint(port)), dial, &b.wg) }()
	}
	return b, nil
}

func (b *clientBridge) Close() {
	b.once.Do(func() {
		b.cancel()
		for _, ln := range b.listeners {
			ln.Close()
		}
		b.wg.Wait()
	})
}

func serveTCP(ctx context.Context, ln net.Listener, target string, dial dialFunc, wg *sync.WaitGroup) {
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
			dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			remote, err := dial(dctx, "tcp", target)
			cancel()
			if err != nil {
				return
			}
			defer remote.Close()
			stop := context.AfterFunc(ctx, func() { client.Close(); remote.Close() })
			defer stop()
			done := make(chan struct{})
			go func() { io.Copy(remote, client); remote.Close(); close(done) }()
			io.Copy(client, remote)
			client.Close()
			<-done
		}()
	}
}

type udpFlow struct {
	ctx    context.Context
	cancel context.CancelFunc
	input  chan []byte
	peer   *net.UDPAddr
}

func serveUDP(ctx context.Context, socket *net.UDPConn, target string, dial dialFunc, wg *sync.WaitGroup) {
	var mu sync.Mutex
	flows := make(map[netip.AddrPort]*udpFlow)
	defer func() {
		mu.Lock()
		for _, f := range flows {
			f.cancel()
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
			fctx, cancel := context.WithCancel(ctx)
			flow = &udpFlow{ctx: fctx, cancel: cancel, input: make(chan []byte, 64), peer: peer}
			flows[key] = flow
			wg.Add(1)
			go func(f *udpFlow) {
				defer wg.Done()
				defer func() {
					mu.Lock()
					if flows[key] == f {
						delete(flows, key)
					}
					mu.Unlock()
				}()
				runUDP(f, socket, target, dial)
			}(flow)
		}
		mu.Unlock()
		packet := append([]byte{}, buf[:n]...)
		select {
		case flow.input <- packet:
		case <-flow.ctx.Done():
		default:
		}
	}
}

func runUDP(flow *udpFlow, socket *net.UDPConn, target string, dial dialFunc) {
	defer flow.cancel()
	ctx, cancel := context.WithTimeout(flow.ctx, 10*time.Second)
	remote, err := dial(ctx, "udp", target)
	cancel()
	if err != nil {
		return
	}
	defer remote.Close()
	stop := context.AfterFunc(flow.ctx, func() { remote.Close() })
	defer stop()
	remote.SetReadDeadline(time.Now().Add(90 * time.Second))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer flow.cancel()
		buf := make([]byte, 65535)
		for {
			n, err := remote.Read(buf)
			if err != nil {
				return
			}
			remote.SetReadDeadline(time.Now().Add(90 * time.Second))
			if _, err = socket.WriteToUDP(buf[:n], flow.peer); err != nil {
				return
			}
		}
	}()
	defer func() { remote.Close(); <-done }()
	for {
		select {
		case <-flow.ctx.Done():
			return
		case packet := <-flow.input:
			remote.SetReadDeadline(time.Now().Add(90 * time.Second))
			remote.SetWriteDeadline(time.Now().Add(10 * time.Second))
			n, err := remote.Write(packet)
			if err != nil || n != len(packet) {
				return
			}
		}
	}
}
