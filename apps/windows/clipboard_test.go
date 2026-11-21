package main

import("os";"testing")

// Only explicitly enabled local verification touches the real clipboard, and restores text afterwards.
func TestLiveClipboardRoundTrip(t *testing.T){if os.Getenv("DD_TEST_CLIPBOARD")!="1"{t.Skip("set DD_TEST_CLIPBOARD=1 for explicit local clipboard verification")};old,e:=readClipboard();if e!=nil{t.Fatal(e)};defer writeClipboard(old);want:="DD远程剪贴板联测 / 🔗 / 123";if e=writeClipboard(want);e!=nil{t.Fatal(e)};got,e:=readClipboard();if e!=nil||got!=want{t.Fatalf("clipboard roundtrip failed: %v",e)}}
