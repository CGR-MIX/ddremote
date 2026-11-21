package com.limelight.ddremote;

/** Generation guard shared by asynchronous account work and its final effects. */
public final class SessionGate {
    private long generation;
    private boolean active, closing;
    public synchronized long open() {
        if (closing) throw new IllegalStateException("账号正在退出，请稍后重新登录");
        if (!active) { generation++; active = true; }
        return generation;
    }
    public synchronized long invalidate() { generation++; active = false; closing = true; return generation; }
    public synchronized void closed() { closing = false; }
    public synchronized long currentToken() {return active && !closing ? generation : 0;}
    public synchronized boolean isCurrent(long token) { return active && !closing && token == generation; }
    public synchronized void require(long token) {
        if (!isCurrent(token)) throw new IllegalStateException("账号会话已结束");
    }
    public synchronized boolean runIfCurrent(long token, Runnable action) {
        if (!isCurrent(token)) return false;
        action.run();
        return true;
    }
}
