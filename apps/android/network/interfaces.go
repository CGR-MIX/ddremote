package main

import (
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"

	"tailscale.com/net/netmon"
)

type interfaceSnapshot struct {
	Interfaces []struct {
		Name      string   `json:"name"`
		Index     int      `json:"index"`
		MTU       int      `json:"mtu"`
		Flags     uint     `json:"flags"`
		Addresses []string `json:"addresses"`
	} `json:"interfaces"`
	DefaultInterface string `json:"defaultInterface"`
	Gateway          string `json:"gateway"`
}

var interfaceMu sync.RWMutex
var currentInterfaces []netmon.Interface
var interfaceHook sync.Once
var activeMonitor atomic.Pointer[netmon.Monitor]

// Android 11+ disallows Go's direct netlink enumeration for application UIDs.
// Read interfaces through Android/Java APIs and feed the upstream supported hook.
func updateInterfaces(payload string) error {
	var input interfaceSnapshot
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		return err
	}
	var interfaces []netmon.Interface
	for _, source := range input.Interfaces {
		if source.Name == "" {
			continue
		}
		item := netmon.Interface{Interface: &net.Interface{Name: source.Name, Index: source.Index, MTU: source.MTU, Flags: net.Flags(source.Flags)}, AltAddrs: []net.Addr{}}
		for _, address := range source.Addresses {
			ip, prefix, err := net.ParseCIDR(address)
			if err != nil {
				continue
			}
			prefix.IP = ip
			item.AltAddrs = append(item.AltAddrs, prefix)
		}
		interfaces = append(interfaces, item)
	}
	interfaceMu.Lock()
	currentInterfaces = interfaces
	interfaceMu.Unlock()
	updateDefaultRoute(input.DefaultInterface, input.Gateway)
	if monitor := activeMonitor.Load(); monitor != nil {
		monitor.InjectEvent()
	}
	return nil
}

func installInterfaceHook() {
	interfaceHook.Do(func() {
		netmon.RegisterInterfaceGetter(func() ([]netmon.Interface, error) {
			interfaceMu.RLock()
			defer interfaceMu.RUnlock()
			return append([]netmon.Interface{}, currentInterfaces...), nil
		})
	})
}
