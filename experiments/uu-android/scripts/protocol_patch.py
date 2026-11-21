"""Small audited compatibility/lifecycle adaptations of the pinned protocol source."""
def replace_once(text, before, after):
    if text.count(before) != 1:
        raise ValueError('Protocol source changed: ' + before[:90])
    return text.replace(before, after)

def adapt(relative, text):
    name=str(relative).replace('\\','/')
    if name.endswith('/http/NvHTTP.java'):
        text=replace_once(text, 'new Request.Builder().url(completeUrl).get().build()', 'new Request.Builder().url(completeUrl).header("Connection", "close").get().build()')
        # OkHttp 3 is Java-only; sequential fallback is already its default.
        text=replace_once(text, '.fastFallback(false)', '')
        text=replace_once(text, '    public HttpUrl getHttpsUrl(boolean likelyOnline)',
            '    public void cancelPendingRequests() { httpClientLongConnectTimeout.dispatcher().cancelAll(); }\n\n    public HttpUrl getHttpsUrl(boolean likelyOnline)')
    if name.endswith('/nvstream/NvConnection.java'):
        text=replace_once(text, '    private final Context appContext;', """    private final Context appContext;
    private volatile boolean cancelled;
    private volatile Thread connectThread;
    private volatile NvHTTP pendingHttp;
    private volatile boolean bridgeOwned;
    private boolean permitOwned;
    private synchronized void releasePermit() {
        if (permitOwned) { permitOwned=false; connectionAllowed.release(); }
    }
    private void cleanupOwnedBridge() {
        synchronized (MoonBridge.class) {
            if (bridgeOwned) {
                MoonBridge.stopConnection();
                MoonBridge.cleanupBridge();
                bridgeOwned=false;
            }
        }
        releasePermit();
    }""")
        begin=text.index('    public void stop() {')
        end=text.index('    private InetAddress resolveServerAddress()', begin)
        text=text[:begin]+"""    public void stop() {
        cancelled=true;
        Thread worker=connectThread;
        if (worker != null) worker.interrupt();
        // Only interrupt the native singleton when this connection owns it.
        // Repeating during startup covers cancellation before LiStartConnection's reset.
        while (worker != null && worker != Thread.currentThread() && worker.isAlive()) {
            NvHTTP pending=pendingHttp;
            if (pending != null) pending.cancelPendingRequests();
            if (bridgeOwned) MoonBridge.interruptConnection();
            try { worker.join(50); } catch (InterruptedException ex) { Thread.currentThread().interrupt(); break; }
        }
        if (bridgeOwned) MoonBridge.interruptConnection();
        cleanupOwnedBridge();
    }

"""+text[end:]
        text=replace_once(text,'        String serverInfo = h.getServerInfo(true);',
            '        pendingHttp=h;\n        if (cancelled) return false;\n        String serverInfo = h.getServerInfo(true);')
        text=replace_once(text,"        // If there's a game running, resume it", "        if (cancelled) return false;\n        // If there's a game running, resume it")
        begin=text.index('    public void start(final AudioRenderer')
        end=text.index('    public void sendMouseMove(',begin)
        old=text[begin:end]
        native_begin=old.index('                    int ret = MoonBridge.startConnection(')
        native_end=old.index('                    if (ret != 0)',native_begin)
        native_call=old[native_begin:native_end]
        text=text[:begin]+"""    public synchronized void start(final AudioRenderer audioRenderer, final VideoDecoderRenderer videoDecoderRenderer, final NvConnectionListener connectionListener) {
        if (connectThread != null || cancelled) throw new IllegalStateException("Connection already used");
        connectThread=new Thread(() -> {
            boolean live=false;
            String appName=context.streamConfig.getApp().getAppName();
            context.connListener=connectionListener;
            context.videoCapabilities=videoDecoderRenderer.getCapabilities();
            try {
                connectionAllowed.acquire();
                synchronized (this) { permitOwned=true; }
                if (cancelled) return;
                connectionListener.stageStarting(appName);
                if (!startApp()) {
                    if (!cancelled) connectionListener.stageFailed(appName,0,0);
                    return;
                }
                pendingHttp=null;
                if (cancelled) return;
                connectionListener.stageComplete(appName);
                ByteBuffer ib=ByteBuffer.allocate(16);
                ib.putInt(context.riKeyId);
                synchronized (MoonBridge.class) {
                    if (cancelled) return;
                    MoonBridge.setupBridge(videoDecoderRenderer,audioRenderer,connectionListener);
                    bridgeOwned=true;
"""+native_call+"""                    live=ret==0 && !cancelled;
                }
            } catch (InterruptedException ex) {
                Thread.currentThread().interrupt();
            } catch (Exception | LinkageError ex) {
                if (!cancelled) {
                    com.limelight.LimeLog.warning("Connection startup failed: " + ex.getClass().getSimpleName());
                    connectionListener.stageFailed(appName,0,ex instanceof HostHttpResponseException ? ((HostHttpResponseException)ex).getErrorCode() : -1);
                }
            } finally {
                pendingHttp=null;
                if (!live) cleanupOwnedBridge();
            }
        }, "dd-uu-protocol-connect");
        connectThread.start();
    }

"""+text[end:]
    return text
