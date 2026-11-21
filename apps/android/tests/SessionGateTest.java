package com.limelight.ddremote;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

/** JVM regression test for the production account callback/credential guard. */
public final class SessionGateTest {
    private static void check(boolean condition,String message) {
        if (!condition) throw new AssertionError(message);
    }
    private static void await(CountDownLatch latch) throws Exception {
        check(latch.await(3,TimeUnit.SECONDS),"concurrent guard test timed out");
    }
    public static void main(String[] args) throws Exception {
        SessionGate gate=new SessionGate();
        check(gate.currentToken()==0,"new gate already active");
        long first=gate.open();
        check(first==gate.open(),"ordinary resume changed account generation");
        AtomicInteger gameLaunches=new AtomicInteger(),credentialWrites=new AtomicInteger();
        check(gate.runIfCurrent(first,credentialWrites::incrementAndGet),"active credential write rejected");
        long invalidated=gate.invalidate();
        check(invalidated>first,"logout did not advance generation");
        check(!gate.runIfCurrent(first,gameLaunches::incrementAndGet),"queued Game launched after logout");
        check(!gate.runIfCurrent(first,credentialWrites::incrementAndGet),"old pairing recreated credentials");
        check(gameLaunches.get()==0 && credentialWrites.get()==1,"stale callback had side effects");
        try {gate.open();throw new AssertionError("login entered during cleanup");} catch(IllegalStateException expected) { }
        gate.closed();
        long next=gate.open();
        check(next>invalidated && gate.isCurrent(next),"new login did not receive a fresh generation");
        check(!gate.isCurrent(first),"old generation accepted in new session");
        try {gate.require(first);throw new AssertionError("old provider accepted");} catch(IllegalStateException expected) { }
        System.out.println("PASS logout suppresses stale Game launches, credential writes and old-session requests");
        System.out.println("PASS login denied during cleanup; new login has independent generation");

        for (int iteration=0;iteration<100;iteration++) {
            SessionGate concurrent=new SessionGate();
            long token=concurrent.open();
            CountDownLatch effectEntered=new CountDownLatch(1),releaseEffect=new CountDownLatch(1),logoutReturned=new CountDownLatch(1);
            AtomicReference<Throwable> failure=new AtomicReference<>();
            AtomicInteger effects=new AtomicInteger();
            Thread writer=new Thread(() -> {
                try {concurrent.runIfCurrent(token,() -> {
                    effectEntered.countDown();
                    try {await(releaseEffect);} catch(Exception e) {throw new RuntimeException(e);}
                    effects.incrementAndGet();
                });} catch(Throwable e) {failure.set(e);}
            });
            writer.start();await(effectEntered);
            Thread logout=new Thread(() -> {concurrent.invalidate();logoutReturned.countDown();});
            logout.start();
            releaseEffect.countDown();await(logoutReturned);
            check(!concurrent.runIfCurrent(token,effects::incrementAndGet),"effect committed after invalidate returned");
            writer.join(3000);logout.join(3000);
            check(failure.get()==null,"effect thread failed: "+failure.get());
            check(effects.get()==1,"effect/logout ordering violated");
        }
        System.out.println("PASS 100 concurrent effect/logout races: no effect after invalidation returns");
    }
}
