package main

import "tailscale.com/net/netmon"

func updateDefaultRoute(name, gateway string) {
	netmon.UpdateLastKnownDefaultRouteInterface(name)
	netmon.UpdateLastKnownDefaultGateway(gateway)
}
