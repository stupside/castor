# Changelog

## [1.6.0](https://github.com/stupside/castor/compare/castor-v1.5.0...castor-1.6.0) (2026-07-20)


### Features

* live-edge pacing at realtime with no burst ([4feef28](https://github.com/stupside/castor/commit/4feef282befbe8cebe5a96e343d4a5a431fdfdec))


### Bug Fixes

* reconnect on HTTP 429 with backoff ([a2e6eb5](https://github.com/stupside/castor/commit/a2e6eb5f78bb434f94acc64d2c0b481d6accba5e))
* remove non-canonical timestamp gate from LA-2 ([0b789d4](https://github.com/stupside/castor/commit/0b789d49341cf301dd2fe33fc64b2f608aa60248))


### Refactors

* split HLS fetch from parse ([d1f44dd](https://github.com/stupside/castor/commit/d1f44dd2a5ee940fc2c033ec163e2c4da6989381))

## [1.5.0](https://github.com/stupside/castor/compare/castor-v1.4.3...castor-v1.5.0) (2026-07-20)


### Features

* add badges to the readme ([cf33c6d](https://github.com/stupside/castor/commit/cf33c6dc631297bd3e9a75af9eb3e2907c1da00c))
* add better feedbacks ([cb5b4e3](https://github.com/stupside/castor/commit/cb5b4e3361dac6ab36766b9b423984cd2bc805bb))
* **browse:** genre discovery, rich metadata, and componentized TUI ([898dcdd](https://github.com/stupside/castor/commit/898dcdd45f4520eaf1551c8fd5ff66174388cbca))
* **browse:** tmdb browse tui ([abe7305](https://github.com/stupside/castor/commit/abe73054e6567810001b9d4e077d572c70380cc9))
* **cast:** auto-detect the local interface from the default route ([d36085d](https://github.com/stupside/castor/commit/d36085d825a7f212f8b8e480f6d8e515f9784052))
* **cast:** read-once spooled pipeline with staged execution ([d11493b](https://github.com/stupside/castor/commit/d11493b405cd9bc78d62ff5714bdda25e559ad53))
* **cmd:** load config lazily so scan and info need none ([106afcb](https://github.com/stupside/castor/commit/106afcbc65f836fe142ea8c8fa0047798394624c))
* **config:** default every setting except device and sources ([b544edc](https://github.com/stupside/castor/commit/b544edcae983cc7e13039127e29985312ec3b449))
* discover chromecast devices via mDNS ([20993bc](https://github.com/stupside/castor/commit/20993bc80210514a363dfb1f19703ef631eb49a8))
* discover Chromecast devices via mDNS ([e757f7a](https://github.com/stupside/castor/commit/e757f7aae7913a76d20e8a3012f5760db3f4e389))
* dive into iframe to trigger loading video ([eabc76f](https://github.com/stupside/castor/commit/eabc76f219977fa2158e8bdf05ef150a2acb5954))
* don't use ring buffer anymore ([172f1dc](https://github.com/stupside/castor/commit/172f1dcf81b223d57ed4e557cb41839313dbddd8))
* drop short streams when ranking candidates ([dd93e4d](https://github.com/stupside/castor/commit/dd93e4d79cb1ba75d261b520175249e3e13a4d52))
* first commit ([849910a](https://github.com/stupside/castor/commit/849910acaf53265206dfb7d8ce55ae92ffe80da5))
* make logs a little bit better looking ([4bfa393](https://github.com/stupside/castor/commit/4bfa39300f2ac05981bc61ef810ffb3143f052ba))
* **release:** static CGO whisper.cpp via matrix + zig cross-compilation ([48feee9](https://github.com/stupside/castor/commit/48feee98c2c2f88d1b68146b81ea656e5276117f))
* replay real browser headers to ffmpeg and ffprobe ([7ef788f](https://github.com/stupside/castor/commit/7ef788f06d4f35264d4ce99c696bf11cf11fbb56))
* simplify browse tui access ([7b13c18](https://github.com/stupside/castor/commit/7b13c18d830fc63c31ab7720589ab2588c39a26a))
* **tmdb:** add genre, discover, and details endpoints ([3bac0d5](https://github.com/stupside/castor/commit/3bac0d58bdb1bf82475988610940201a792cbc00))
* update docker run instruction with working proxy ([cf5e010](https://github.com/stupside/castor/commit/cf5e010c094690d6531a1534d144094dbfbcd976))
* **whisper:** in-process transcription via whisper.cpp ([a09d67d](https://github.com/stupside/castor/commit/a09d67df125034414c74d4544452c36e3156691a))
* **whisper:** stream subtitles via LocalAgreement-2, VAD, and a realtime-paced encoder ([1386dec](https://github.com/stupside/castor/commit/1386dec6d97f7cefda50571ad2c55ce2c6574f2d))


### Bug Fixes

* apply HLS-only ffmpeg input flags to HLS sources only ([60207bc](https://github.com/stupside/castor/commit/60207bc6e8a8cbf8751844fa799e42407985b264))
* clear quarantine xattr in cask postflight ([1d15a29](https://github.com/stupside/castor/commit/1d15a2921e729eecbf2d18beaeba5d1a18f7da07))
* clear quarantine xattr in cask postflight so castor runs after brew install ([c425683](https://github.com/stupside/castor/commit/c425683143f633e7de94d1406fd3328302fc543c)), closes [#17](https://github.com/stupside/castor/issues/17)
* **config:** keep defaults when a section is present but empty ([bc9fd8b](https://github.com/stupside/castor/commit/bc9fd8b02ac438f293bfbc4e4c72705390824e36))
* **dlna:** stop pacing delivery to the renderer ([81183ee](https://github.com/stupside/castor/commit/81183ee383eb342899419cc7bbf7434ab6fa3baa))
* docker image has old ffmpeg version ([0affafa](https://github.com/stupside/castor/commit/0affafa4d39ff3bc0fda9b954a0b77693230dbae))
* **docker:** install the ffmpeg/chromium/TLS runtime the pipeline needs ([bd1cb14](https://github.com/stupside/castor/commit/bd1cb14ae2a3119f88822e47ad610c7b2685c96e))
* **extract:** pass --disable-dev-shm-usage to chromium ([a793431](https://github.com/stupside/castor/commit/a79343150288396fb18723679cec9620aa77e6b1))
* **extract:** reap headless Chrome on session teardown ([aa56480](https://github.com/stupside/castor/commit/aa5648077a1546b903692ae796d2e68c5a4822a3))
* let real request headers win over header-less captures ([b9f6632](https://github.com/stupside/castor/commit/b9f6632b34e31bcc58f3831d1a16cddc36969025))
* **release:** build whisper with GGML_NATIVE=OFF for portable binaries ([98efaa8](https://github.com/stupside/castor/commit/98efaa8b0ac6c87abcebe53152a7b4d543f79a82))
* **release:** ignore .zig-cache and whisper build_* dirs ([d58ee28](https://github.com/stupside/castor/commit/d58ee28e5a4bec692a4ebabb7d756eb0b6acc6a3))
* **release:** matrix parallel whisper builds; stub install_name_tool for darwin cross ([114775c](https://github.com/stupside/castor/commit/114775c943d308511454189153714541ac20a3cd))
* **release:** native runners for whisper builds, no cross-compile hacks ([2c0b780](https://github.com/stupside/castor/commit/2c0b78037999b09cdf2d37a1ba6d6d88c307f5fc))
* **release:** replace Pro-only split/merge with single-runner zig cross-compilation ([976eab6](https://github.com/stupside/castor/commit/976eab61e302bb79bfd574565afbba1f308cf227))
* **release:** use MAJOR.MINOR.PATCH format for zig macOS target triple ([6ca20fd](https://github.com/stupside/castor/commit/6ca20fd530c83ae3986a7781982d0a4c8d1b86c3))
* **resolve:** drop decoy streams with no castable video+audio ([4d0f14d](https://github.com/stupside/castor/commit/4d0f14de0b6aaea442224a3106af89f4d4fd3642))
* send browser request headers to puller so hotlinked streams work ([5651dd9](https://github.com/stupside/castor/commit/5651dd9b0747d947d615d1eb194bfc7477598a12))
* send browser request headers to puller so hotlinked streams work ([ca50081](https://github.com/stupside/castor/commit/ca50081758673da2b4262c11069eb5128520fcf3)), closes [#14](https://github.com/stupside/castor/issues/14)
* stream buffers too much ([eeb5bb9](https://github.com/stupside/castor/commit/eeb5bb995308d3838b5294d520578a0e788a39b7))
* stream can stop after some time ([e1aef10](https://github.com/stupside/castor/commit/e1aef10029e24f5fe276139e0fcda3914cbced3e))


### Refactors

* **cast:** split cue shaping out of whisper into a cue package ([1e2ea3c](https://github.com/stupside/castor/commit/1e2ea3c7ee53c064452223835ba26781f44f5572))
* **cmd:** typed config wiring ([a5fcc76](https://github.com/stupside/castor/commit/a5fcc761f6f26535048e5caa7a067c8efa73ade1))
* **config:** compose per-package configs ([067977b](https://github.com/stupside/castor/commit/067977b2bc235ded0011f9864265570e8278532d))
* **device:** flatten renderers behind a connect factory ([4c02e50](https://github.com/stupside/castor/commit/4c02e50085e37e577fdbd23b519b3cdb73c0f2a2))
* make sources an array without names, remove --source flag ([657d041](https://github.com/stupside/castor/commit/657d0414d8619b267331ab80da168af71f48a933))
* **media:** add http header helpers ([6360bf9](https://github.com/stupside/castor/commit/6360bf9726e54594ff2c8ef0a432446a6967d7e8))
* mirror DLNA discovery on the chromecast pattern ([afa52f1](https://github.com/stupside/castor/commit/afa52f1f31e3a1b667786d97c81e3360928d3964))
* modernize Discover and gate device interface impls ([b277fef](https://github.com/stupside/castor/commit/b277fefb2df9be2d52604fbf8c0ddb38d97aa326))
* **replay:** remove the now-unused token bucket ([7858d0e](https://github.com/stupside/castor/commit/7858d0eaa0fecede13e122e76e3760c7a274fb8a))
* **source:** group extraction and resolution under internal/source ([c0eeee4](https://github.com/stupside/castor/commit/c0eeee402cc3980e7c2810177c96adbcdbc3c38a))
* use explicit 2xx range check instead of the /100 trick ([3999cab](https://github.com/stupside/castor/commit/3999cab68015a21bb5229805d68a95fd54bcec91))


### Documentation

* add a castor mascot to the README ([be9cd9b](https://github.com/stupside/castor/commit/be9cd9b9b67ae3e61940306927d869badb7345b1))
* document Docker usage ([ab0dfbd](https://github.com/stupside/castor/commit/ab0dfbda1794f35746c405400c84b3781d2fce75))
* document subtitle generation and source build ([b90c621](https://github.com/stupside/castor/commit/b90c62114d7ab7eca314e8b511818a7aad954897))
* drop stale references to the send pacer ([8157dd9](https://github.com/stupside/castor/commit/8157dd9071cb3f7a01a2d51d2d4f939907f35ffd))
* minimal config and direnv setup ([f925ca9](https://github.com/stupside/castor/commit/f925ca9584891fa57c2f00ce387f9dd0e62fb1bf))
* overhaul README, add CONTRIBUTING and SECURITY ([e010f18](https://github.com/stupside/castor/commit/e010f181ba2742dd5349910dfe05ed0de8522bd5))
* **readme:** add Installation section (Homebrew, Docker, source) ([a1c5d13](https://github.com/stupside/castor/commit/a1c5d1399d36a070ab1e43652fbf0cac38540148))
* **readme:** fix stale commands, add config quickstart and screenshots ([b01c55e](https://github.com/stupside/castor/commit/b01c55edc1a5a2428ffe5a9c93be0ff30e9f3ff9))
