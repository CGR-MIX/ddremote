package com.limelight.ddremote;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.RouteInfo;
import android.os.Build;
import org.json.JSONArray;
import org.json.JSONObject;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.util.Collections;
import java.util.Enumeration;
import java.util.HashSet;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/** Supplies supported Android interface information to Go's userspace netmon. */
final class AndroidNetworkState {
    private static boolean registered;
    private static final ExecutorService events = Executors.newSingleThreadExecutor();
    private static final AtomicBoolean queued = new AtomicBoolean();

    static JSONObject snapshot(Context context) throws Exception {
        JSONObject out = new JSONObject();
        JSONArray interfaces = new JSONArray();
        Set<String> names = new HashSet<>();
        try {
            Enumeration<NetworkInterface> all = NetworkInterface.getNetworkInterfaces();
            if (all != null) for (NetworkInterface item : Collections.list(all)) {
                try {
                    int flags = item.isUp() ? 33 : 0;
                    if (item.isLoopback()) flags |= 4;
                    if (item.isPointToPoint()) flags |= 8;
                    if (item.supportsMulticast()) flags |= 16;
                    JSONArray addresses = new JSONArray();
                    for (InterfaceAddress address : item.getInterfaceAddresses()) {
                        String ip = address.getAddress().getHostAddress();
                        if (ip != null) addresses.put(ip.split("%", 2)[0] + "/" + address.getNetworkPrefixLength());
                    }
                    interfaces.put(new JSONObject().put("name", item.getName()).put("index", item.getIndex())
                            .put("mtu", Math.max(item.getMTU(), 1280)).put("flags", flags).put("addresses", addresses));
                    names.add(item.getName());
                } catch (java.net.SocketException ignored) { }
            }
        } catch (java.net.SocketException ignored) { }
        ConnectivityManager manager = (ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE);
        LinkProperties active = null;
        if (Build.VERSION.SDK_INT >= 23 && manager.getActiveNetwork() != null) active = manager.getLinkProperties(manager.getActiveNetwork());
        for (Network network : manager.getAllNetworks()) {
            LinkProperties properties = manager.getLinkProperties(network);
            if (properties == null || properties.getInterfaceName() == null) continue;
            if (active == null) for (RouteInfo route : properties.getRoutes()) if (route.isDefaultRoute()) { active = properties; break; }
            if (!names.contains(properties.getInterfaceName())) {
                JSONArray addresses = new JSONArray();
                for (LinkAddress address : properties.getLinkAddresses()) {
                    String ip = address.getAddress().getHostAddress();
                    if (ip != null) addresses.put(ip.split("%",2)[0]+"/"+address.getPrefixLength());
                }
                int mtu = 1500;
                if (Build.VERSION.SDK_INT >= 29 && properties.getMtu() > 0) mtu = properties.getMtu();
                interfaces.put(new JSONObject().put("name",properties.getInterfaceName()).put("index",interfaces.length()+1)
                        .put("mtu",mtu).put("flags",49).put("addresses",addresses));
            }
        }
        String gateway = "", defaultInterface = "";
        if (active != null) {
            defaultInterface = active.getInterfaceName();
            for (RouteInfo route : active.getRoutes()) if (route.isDefaultRoute() && route.getGateway() != null && !route.getGateway().isAnyLocalAddress()) {
                gateway = route.getGateway().getHostAddress();
                if (route.getGateway() instanceof java.net.Inet4Address) break;
            }
        }
        return out.put("interfaces",interfaces).put("defaultInterface",defaultInterface == null?"":defaultInterface)
                .put("gateway",gateway == null?"":gateway);
    }

    static synchronized void register(Context context) {
        if (registered) return;
        Context app = context.getApplicationContext();
        ConnectivityManager manager = (ConnectivityManager) app.getSystemService(Context.CONNECTIVITY_SERVICE);
        ConnectivityManager.NetworkCallback callback = new ConnectivityManager.NetworkCallback() {
            private void changed() {
                if (!queued.compareAndSet(false,true)) return;
                events.execute(() -> {
                    try { EmbeddedNetwork.call("networkChanged", snapshot(app)); }
                    catch (Exception ignored) { }
                    finally { queued.set(false); }
                });
            }
            @Override public void onAvailable(Network network) { changed(); }
            @Override public void onLost(Network network) { changed(); }
            @Override public void onLinkPropertiesChanged(Network network, LinkProperties properties) { changed(); }
        };
        manager.registerNetworkCallback(new NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).build(),callback);
        registered = true;
    }
}
