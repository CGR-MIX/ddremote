package main

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// SetClientPairTag binds the first Moonlight pairing message to an account
// approval nonce. The nonce is not a credential and never replaces uniqueid.
// PrepareClient must have completed before setting the tag. Empty clears it.
func (m *NetworkManager) SetClientPairTag(nonce string) error {
	if nonce != "" {
		decoded, err := hex.DecodeString(nonce)
		if err != nil || len(decoded) != 16 || len(nonce) != 32 {
			return errors.New("配对标识必须是32位十六进制字符")
		}
		nonce = strings.ToLower(nonce)
	}
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client == nil {
		return errors.New("请先准备远端连接")
	}
	m.client.pairMu.Lock()
	m.client.pairTag = nonce
	m.client.pairMu.Unlock()
	return nil
}

func (b *networkClientBridge) currentPairTag() string {
	b.pairMu.RLock()
	defer b.pairMu.RUnlock()
	return b.pairTag
}

func taggedNetworkPairQuery(path, rawQuery, nonce string) (string, error) {
	if nonce == "" || path != "/pair" {
		return rawQuery, nil
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", errors.New("invalid pairing query")
	}
	phrases := query["phrase"]
	if len(phrases) == 0 {
		return rawQuery, nil
	}
	if len(phrases) != 1 {
		return "", errors.New("ambiguous pairing phrase")
	}
	if phrases[0] != "getservercert" {
		return rawQuery, nil
	}
	// Keep all unrelated parameters byte-for-byte, including their ordering,
	// escapes and duplicates. Only devicename is replaced (or added).
	parts := strings.Split(rawQuery, "&")
	out := make([]string, 0, len(parts)+1)
	replaced := false
	for _, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		key, err = url.QueryUnescape(key)
		if err != nil {
			return "", err
		}
		if key == "devicename" {
			if !replaced {
				out = append(out, "devicename=DD-"+nonce)
				replaced = true
			}
		} else {
			out = append(out, part)
		}
	}
	if !replaced {
		out = append(out, "devicename=DD-"+nonce)
	}
	return strings.Join(out, "&"), nil
}

func newNetworkPairProxy(ctx context.Context, target string, dial networkDialFunc, pairTag func() string) (*http.Server, *http.Transport) {
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: false, DisableCompression: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dial(ctx, "tcp", target)
		},
		MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: 20 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.Out.URL.Scheme = "http"
			req.Out.URL.Host = target
			req.Out.URL.RawQuery = req.In.URL.RawQuery
			req.Out.Host = target
		},
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "remote pairing service unavailable", http.StatusBadGateway)
		},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodConnect || req.Header.Get("Upgrade") != "" {
			http.Error(w, "unsupported request", http.StatusBadRequest)
			return
		}
		query, err := taggedNetworkPairQuery(req.URL.Path, req.URL.RawQuery, pairTag())
		if err != nil {
			http.Error(w, "invalid pairing request", http.StatusBadRequest)
			return
		}
		req.URL.RawQuery = query
		proxy.ServeHTTP(w, req)
	})
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute,
		IdleTimeout: 20 * time.Second, MaxHeaderBytes: 64 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}, transport
}
