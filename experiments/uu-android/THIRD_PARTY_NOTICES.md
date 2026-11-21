# DD UU adaptation dependencies

The APK is a local modification experiment based on NetEase UU Remote Android 4.48.1. Its original reassembled DEX, resources, assets and native libraries remain third-party material. This is not original DD source, and is not affiliated with or endorsed by NetEase.

The DD-added Android entry, lifecycle, H.264 renderer, input router and build scripts are in this directory. Selected protocol, certificate and audio code from Moonlight Android v12.2, plus moonlight-common-c/Opus native libraries extracted from the pinned DD 0.2.1 build, provide Sunshine compatibility. Moonlight's Game activity and app resources are not included as the UI. Moonlight Android is GPL-3.0; see the existing `apps/android/LICENSE.txt` and pinned source archives in `dist/source`. Dependency use here does not relicense UU's third-party code.

The DD network native library uses Tailscale tsnet and the licenses/source recorded under `apps/android/network` and `dist/source`. OkHttp 3.14.9 and Okio 1.17.2 (Apache-2.0), Bouncy Castle bcprov 1.85.2 / bcpkix and bcutil 1.85 (Bouncy Castle license) are namespace-isolated in the added DEX. Exact dependency hashes are in `dependencies.json`. These libraries preserve the original application's dependency namespace.

No UU account, subscription, authorization server or native streaming protocol has been bypassed. The entry uses DD's existing Tailscale account flow and the user's Sunshine host. No publication or third-party upload is part of this experiment.
