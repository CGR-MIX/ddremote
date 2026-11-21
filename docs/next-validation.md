# Continuation state — 2026-11-20 0.3.4

Goal active, incomplete. Latest user requires UU-based APK; original UU4.48.1 APK rebuilt with retained DEX/resources and original input/surface/keyboard assets. DD account/activity/toolbar, Moonlight protocol/audio library remains, no Game activity. Do not claim full UU ScreenActivity adaptation.

Delivered: dist/experimental/DDRemote-UU-0.3.4-mobile-lab.apk (code5), unchanged DDRemote-Windows-0.3.3-super-screen-lab.exe. Release facts/hash in experiments/uu-android/evidence/release-0.3.4.json. Final non-instrumented signed APK installed/cold-launched on emulator-5580. Staging unsigned is FINAL non-instrumented. Original0.2.1 APK retained.

0.3.4 product: original UU editor TYPE_CLASS_TEXT|MULTI_LINE, no extract/fullscreen IME; insets-aware viewport shrinks/restores; bounded55% height scrollable keyboard; preferences quality/touch0.5–2x/mute; fractional pointer/scroll; local volume keys. Source files StreamPreferences, StreamAudio, TouchRouter, UuTextInputAdapter, SunshineInput, UuStreamActivity, AccountConnector. Final runtime input-logic and mobile-ui-scroll-panel logs pass. Full keyboard viewport486; IME1080->496->1080;720p95/1080p90 frames, reconnect/cancel/teardown pass. Not frame-rate benchmarks.

Audio passed decoded synthetic tone36 nonSilent,40 muted callbacks. Initial zero-packet failure was test fixture relay reply thread exiting on early Windows UDP ICMP10054; retry fixes it. No product networking patch for this. Fixture UDP counts and failure logs retained; physical speaker unverified.

Windows actual input earlier passed: host-input-actual.json includes Chinese+emoji, Ctrl+A, left/right click, scroll, drag. FINAL repeat failed: target visible but not foreground; first run received nothing; guarded repeat sent no input. sky activate failed with desktop-composition-disabled. Do not repeatedly force foreground or send blind input. verify_host_input.py now waits for synthetic target foreground; InputTargetV3 updates status200ms. Resume when desktop is interactive, then run final APK test build and verify. A screenshot unexpectedly showed user desktop instead of synthetic backdrop; screenshot copies removed locally and device, screenshot test code removed. No exported desktop image in delivery.

Privacy remote capture exclusion passed with visible synthetic RGBW baseline/masked/restored identical; see privacy-stream-visible-baseline.json. Physical monitor blackout still unverified. No claim of finished privacy screen.

Super-screen0.3.3 implementation/tests remain: exact own SWD interface/INF hash, durable layout journal, restore-before-remove/watchdog/lease/local hotkey and manual recovery. Driver NOT installed; real virtual-only capture/restore untested. Current process unelevated. Do not touch existing UU GameViewer device. Driver staging script scripts/prepare-super-screen-driver.ps1 only verified read-only. Hardware validation requires admin and actual restore check; do not bypass signing.

Next: real Xiaomi13/Android16 same-account discovery, click connect, keyboard/touch/quality/audio, logout cloud removal; actual super-screen/physical privacy verification; broader UU feature parity still pending. Windows source unchanged this release; prior Go/UI suite passes. No need repeat unchanged suite without reason.

Toolchain: D:/DSHworkspace/_toolchain/ddremote-uu-android (TEMP/TMP tmp), jdk21/jdk-21.0.13+9, android-sdk/build-tools/37.0.0. ADB .tools/android/sdk/platform-tools/adb.exe. Emulator5580. Computer-use sky via node_repl; refresh window handles. User processes must be preserved. No new agents unless authorized. No root Git.

Dedicated emulator test settings: immersive_mode_confirmations=confirmed and show_ime_with_hard_keyboard=1. Real phone settings untouched. Re-running mobileui requires interactive desktop/synthetic backdrop; avoid screenshot capture of desktop. No fixture/helper processes remain after tests.
