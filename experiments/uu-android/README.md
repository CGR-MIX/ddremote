# Current release: 0.3.4 mobile usability

Read [DELIVERY-0.3.4.zh-CN.md](DELIVERY-0.3.4.zh-CN.md) and [release evidence](evidence/release-0.3.4.json) first. Delivered APK is code5 and contains no instrumentation. Android improvements: declared original UU editor input type, IME-aware viewport, bounded scrollable full keyboard, persistent quality/touch/sensitivity/mute, fractional movement, local volume keys. Normal stream, UI/IME lifecycle and decoded tone/mute pass on Android16 emulator. Earlier Windows input receipts pass; final receipt rerun did not get foreground and is not a pass. Privacy remote capture exclusion passes, physical blackout remains unverified; super-screen driver not installed. Windows artifact remains 0.3.3. Real Xiaomi/account/logout verification remains outstanding.

The sections below retain the historical 0.3.1—0.3.3 record; newer delivery facts above take precedence.

# UU Android adaptation workspace

This is a local, non-production modification experiment based on the official UU 4.48.1 APK identified in `research/uu/android`. It is **not** a Moonlight app with a new label. The rebuilt package retains UU's original reassembled DEX and resource table, plus a new DD entry DEX. Version 0.3.1 adds a working Sunshine protocol adapter. Local Android 16 emulator tests have received and rendered real H.264 video on the original UU SurfaceView; phone/account end-to-end validation remains outstanding.

The manifest replaces original application startup with Android's base Application and registers the DD entry, input component lab, non-exported stream activity and account lifecycle service. Original account, analytics, payment, update providers and activities are not registered or started. The original UU code is retained for staged adaptation, not represented as independently authored DD source.

## Verified progress

- APKTool 3.0.3 decodes both original DEX files and all resources. Rebuild succeeds using its bundled aapt2; Android SDK aapt2 rejects original `$...` resource names.
- Original UU `InvisibleEditText` and its actual `InputConnectionWrapper` load without `BlinkApplication` or the original account services.
- Original full keyboard layout assets are rendered, and callbacks can be routed to a DD-owned listener. The stream adapter routes these callbacks to GameStream input.
- On Android 16 / API 36 x86_64, the signed lab APK installs and launches.
- Runtime assertions using the **original input connection** pass: composing text is not prematurely committed, Chinese+emoji produces one exact text callback, newline maps to keycode66, deletion maps to67, finishing composition twice does not submit twice. Event payloads are retained in memory only during the synthetic test, never logged.
- DD's embedded network starts and login opens the actual Tailscale login page. No account credentials were entered. Discovery with real signed-in devices and cloud node deletion are not yet validated for this package.

Evidence lives in `evidence/`. Original APK/decompiled files/signing material remain outside this source folder. The old production DD Android package remains unchanged.

## Build

Requires supplied original APK with SHA256 `19252db1dd384f25db000b334e6f77616b9dfa26b2e416ce5383038e973e219f`, Apktool JAR3.0.3 with SHA256 `dbf930b076c6b9be08d57c449cacefc3bdd6b71ebd59b3066fc0e1f5b14f9423`, existing DD network libraries, JDK21 and Android SDK37.

```powershell
python experiments/uu-android/scripts/build.py --stage D:/DSHworkspace/_toolchain/ddremote-uu-android --java-home D:/DSHworkspace/_toolchain/jdk21/jdk-21.0.13+9 --sdk D:/DSHworkspace/_toolchain/android-sdk --apk research/uu/android/uu-remote-official.apk --apktool .tools/rea/apktool_3.0.3.jar
```

Use the existing `apps/android/sign-ddremote.ps1` with the locally stored DD keystore/password file. Never copy signing secrets to this folder. Package identity is `io.ddremote.uuadapter`, version `0.3.1-stream-lab`; it installs alongside the working `io.ddremote.android` APK.

## 0.3.1 stream adapter and verified scope

- The base is the pinned actual UU APK, retaining both reassembled original DEX files and original resources. The original `StreamerSurfaceView`, `InvisibleEditText` and fullkeyboard assets are used at runtime. DD supplies the account home and stream activity; this is **not the unchanged original UU ScreenActivity/UI**.
- Selected Moonlight protocol, TLS/pairing and Android audio library classes, plus `libmoonlight-core.so`, implement Sunshine compatibility. No Moonlight Game activity is included. Private dependency namespaces avoid collisions with UU libraries.
- H.264 MediaCodec decoding, fullscreen aspect fit, keyboard and shortcut panel, direct/trackpad modes, long-press drag, two-finger scroll/right click, and 720p30/720p60/1080p60 quality reconnect are implemented.
- Protocol cancellation owns its semaphore/native singleton explicitly; pending HTTP work can be cancelled, startup cancellation and repeated teardown cannot release another connection. Quality changes release the old connection before opening the new one.
- `evidence/input-logic-test.log` records real original UU IME → production input router assertions against a **recording protocol sink**. It covers Unicode/composition, Enter/Backspace, held Ctrl chords, release, trackpad, direct touch, two-finger click, cumulative scroll, and drag release. This is not a Windows text-receipt test.
- `evidence/stream-quality-final-test.log` is the most recent instrumented real-stream test. Tests run on Android16/API36 x86_64 against an isolated Sunshine instance on alternate ports, with local TCP/UDP relays. Instrumentation and emulator-only cleartext allowance are omitted from the delivery APK.
- A direct emulator-host path produced truncated HTTP/encrypted RTSP responses; local relays resolved this in the fixture. Production uses DD's existing tsnet relay. The modified package has not yet been tested across a real tailnet.
- Windows was locked during an attempted real text-input test; Sunshine returned access-denied for the input desktop. That test did not pass and no successful real text receipt is claimed. No lock-screen authentication was attempted.

## Remaining work

1. Real Xiaomi13/Android16 account discovery, click-to-connect, keyboard/touch ergonomics, audio, and quality validation. The local video counter proves rendered frames, not 60fps performance. Audio stream setup succeeds but the silent fixture received no audio packets.
2. Original UU remote toolbar/room ScreenActivity remains coupled to UU account/Koin state. Current toolbar is DD-authored, using the original keyboard assets and two isolated UU views.
3. Windows virtual display/super-screen and privacy-screen still require implementation and safe physical-display restoration. No functioning buttons for these are claimed.
4. Recheck logout/cloud node removal with a real account. Existing ephemeral/session cleanup code is retained, but actual cloud deletion has not been verified for this rebuilt package.
5. This package currently provides account connection and stream controls; the old client's file-transfer interface has not been ported here.

This is an experimental side-by-side APK (`io.ddremote.uuadapter`); the working 0.2.1 application is retained. The Windows EXE has not changed for this adaptation. No public redistribution or external upload has been performed. Original APK/decompilation and signing secrets are excluded from the adapter source archive. See `THIRD_PARTY_NOTICES.md` for dependency scope.

## 0.3.2 privacy experiment

Windows and Android privacy-session control is now implemented; helper lifecycle and account/lease cleanup are tested. Visual privacy is NOT yet verified. Super-screen remains incomplete. See `DELIVERY-0.3.2.zh-CN.md` and `evidence/release-0.3.2.json` for current facts; earlier 0.3.1 records above are historical.

## 0.3.3 super-screen plumbing (not hardware-validated)

See DELIVERY-0.3.3.zh-CN.md and evidence/release-0.3.3.json. The UU-based APK is code4. The Windows helper/API and Android toggle are implemented, with exact device/driver identity, durable layout journal, restoration watchdog and manual recovery. Driver not installed; no virtual output or physical-output disable was exercised. Do not claim working super-screen or visual privacy. Latest normal 720p/1080p stream and input regressions pass on the emulator after correcting the local relay (failed attempts retained).
