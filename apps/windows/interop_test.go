package main

import("net/url";"os";"path/filepath";"testing";"time")

func TestLiveAndroidInterop(t *testing.T){dir:=os.Getenv("DD_INTEROP_DIR");if dir==""{t.Skip("set DD_INTEROP_DIR for explicit Android/Windows protocol verification")};if e:=os.MkdirAll(dir,0700);e!=nil{t.Fatal(e)};s:=testTransfer(t);if e:=s.Start();e!=nil{t.Fatal(e)};q:=url.Values{"host":{"127.0.0.1"},"port":{"48200"},"token":{s.token},"fingerprint":{s.fingerprint}};link:="ddremote://connect?"+q.Encode();if e:=os.WriteFile(filepath.Join(dir,"link.txt"),[]byte(link),0600);e!=nil{t.Fatal(e)};defer os.Remove(filepath.Join(dir,"link.txt"));t.Log("Production Windows TLS companion is ready for Java client (token not logged)");deadline:=time.Now().Add(5*time.Minute);for time.Now().Before(deadline){if _,e:=os.Stat(filepath.Join(dir,"done"));e==nil{return};time.Sleep(250*time.Millisecond)};t.Fatal("Android client test did not finish within 5 minutes")}
