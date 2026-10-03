# Changelog

## [1.10.0](https://github.com/stupside/castor/compare/v1.9.0...v1.10.0) (2026-10-03)


### Features

* **api:** drive a castor server for the renderers on the client's network ([b4f6349](https://github.com/stupside/castor/commit/b4f6349906fdc99fc7d471229726a998062fd9a0))
* **api:** hold every message to the contract both ways, with protovalidate and CEL rules ([0f2a520](https://github.com/stupside/castor/commit/0f2a5203e24ec629f528593e4182f7d1cb6807f4))
* **api:** log the address of the device a client lends, which the contract sends for that ([4205f3e](https://github.com/stupside/castor/commit/4205f3ee2c20d0838bcb6d2638a673ce91d09537))
* **api:** make the server the media server, which TVs fetch from and which outlives its client ([f9aabf6](https://github.com/stupside/castor/commit/f9aabf6e4524555fb7bba06c9574fce0f18b95df))
* **api:** serve casts behind the contract, driving renderers only through a client ([e48dd4b](https://github.com/stupside/castor/commit/e48dd4b75b2752f53422495bbec1edb2e8525384))
* **api:** state the cast contract in protobuf, generated with buf for Connect ([3d3bf60](https://github.com/stupside/castor/commit/3d3bf60dfe5d706edcfd6a135d4ac6e73bc95e78))
* **app:** add the web client ([e295db7](https://github.com/stupside/castor/commit/e295db7772fc0904dbcc643f3941617dd503a57c))
* **cast:** start a device once the read holds thirty seconds ([7966a51](https://github.com/stupside/castor/commit/7966a516af63b8cf13ffc12e1809a563ff431814))
* **cast:** tell a Turns port each attempt and revision a cast takes ([08de916](https://github.com/stupside/castor/commit/08de916e26457a4f8f219f330fdde1ac8022b452))
* **cli:** also write log lines to castor.log in the user cache directory ([e29bb19](https://github.com/stupside/castor/commit/e29bb19d8e103d38cfafb5c1b148de2835f155bc))
* **cli:** split castor api-server from castor media-server ([8e70bc8](https://github.com/stupside/castor/commit/8e70bc86a7f7737c51797fce5d02e618974c78fa))
* **cmd:** cast through the API, served in process or by castor api server ([6eec870](https://github.com/stupside/castor/commit/6eec8706629896389b67b2905184e0c533a1aedd))
* **cmd:** hold the client's log lines back while a screen is up, and write them after ([e0a7035](https://github.com/stupside/castor/commit/e0a7035decc611e870fbd0696e58a4d7973a6734))
* **cmd:** show a cast's warnings without --debug, and the embedded engine's own lines with it ([f8459af](https://github.com/stupside/castor/commit/f8459af3499b62c06019aa7f837034e8ab2735f2))
* **config:** refuse a subtitle language that is neither a BCP-47 tag nor auto ([a7aa3c2](https://github.com/stupside/castor/commit/a7aa3c2c42a5571ac45e5d590b504ecb93e89ca6))
* **docker:** run media-server and api-server as separate compose services ([2f6570d](https://github.com/stupside/castor/commit/2f6570d33ad838c141e62767a4e449ae3231e1c5))
* **picker:** quit on q at once, without a confirmation dialog ([deeeed3](https://github.com/stupside/castor/commit/deeeed30a32cb20387ec9d4c8d4becafb15d20e1))


### Bug Fixes

* **api:** end a cast at once when it needs a device whose client has left ([5933d52](https://github.com/stupside/castor/commit/5933d5268d1696d5f272ab710e0e2cac3a8e52ab))
* **api:** give castor api server's shutdown its grace before it exits ([1d53b0f](https://github.com/stupside/castor/commit/1d53b0fa9e7040655c21a8e41579a12cdec50ee2))
* **api:** leave a cancelled extraction or ranking to connect's own code ([01edb63](https://github.com/stupside/castor/commit/01edb63d0eafa1685f701651ccdd191fb56e7f82))
* **api:** let only http and https links reach the server's browser and readers ([5784083](https://github.com/stupside/castor/commit/57840832889fc539a38c5048c2aa3aa580ba6f5f))
* **api:** refuse a cast that leaves its delivery unstated ([d8a31d2](https://github.com/stupside/castor/commit/d8a31d2421ce2065d7a9d669dbad33cae8e2bf9e))
* **api:** refuse a device that answers connect without its capabilities ([889556c](https://github.com/stupside/castor/commit/889556cc8e8feedc67ec8ed3ee1abcf11af444ca))
* **api:** send on the Drive stream from its handler alone, each send bounded by its call ([2133f21](https://github.com/stupside/castor/commit/2133f215ac676e719a4819cf0d79e2bf9bff0e7d))
* **api:** stop relaying a delivery's port once its listener closes ([357531d](https://github.com/stupside/castor/commit/357531d5764e19b717aab7262556a01f24c754e8))
* **cast:** blame a delivery silent over castor's proven buffer on the delivery, not the source ([d0f7c1d](https://github.com/stupside/castor/commit/d0f7c1d628862ce45461a1a09d98c7641c6565c2))
* **cast:** end a playing cast whose renderer has had nothing to play for the stall window ([3cba9ed](https://github.com/stupside/castor/commit/3cba9edc79c215cad59c43750d7b1d263133f5b0))
* **cast:** fail a cast whose device ran out of media before the read finished ([26612aa](https://github.com/stupside/castor/commit/26612aa4bb022a668670573ff0504289447abd94))
* **cast:** give up on a delivery whose producer never writes its first artifact ([39b21cf](https://github.com/stupside/castor/commit/39b21cfa8540dbf3eb893ddcfecb967e4d1464e0))
* **cast:** hear sound where the source declares or requires it, and say when subtitles are skipped ([c642d18](https://github.com/stupside/castor/commit/c642d1883234e3152aa9ded0fbeec3cc5b8f1206))
* **cast:** join the source read before teardown releases what it reads from ([c28b2d1](https://github.com/stupside/castor/commit/c28b2d1d3b0c621ac3d69090652e2d44cbc574ca))
* **cast:** judge a producer's growth against playback pace, not since the cast began ([01d034b](https://github.com/stupside/castor/commit/01d034b9b1ace0acba4ee0c8974699abf577aae1))
* **cast:** log a failed lighter rendition by the link castor read, not the rung's missing URL ([14fd3b7](https://github.com/stupside/castor/commit/14fd3b7af9a388fdc3659f0e184ce748dc1cfb81))
* **cast:** move to the next link when castor cannot read a timeline it follows ([c61962c](https://github.com/stupside/castor/commit/c61962cbb8c907d1dec6358b767a6fb2e4fd7f7c))
* **cast:** name a device that ran dry starved ([286f0b8](https://github.com/stupside/castor/commit/286f0b8e30398f5cfc460eba0072456e466385f0))
* **cast:** serve instead only when the renderer was handed the source ([c771bdf](https://github.com/stupside/castor/commit/c771bdf12ae5ad33013727c8179228ac87230927))
* **cast:** transcribe only a read sure to carry sound ([0cd56dd](https://github.com/stupside/castor/commit/0cd56dddbe170435425a873d1028659e88358067))
* **chromecast:** end the cast as gone when the Cast connection drops ([79fa899](https://github.com/stupside/castor/commit/79fa899ec64ec2bd1998aa0be1c7e74f80572f7d))
* **client:** leave the drive when an answer to the server is lost ([6691044](https://github.com/stupside/castor/commit/66910441e9cba8b729ca12a23135f0d2163c6190))
* **client:** lend the LAN only the server's relay route, never its API ([4929538](https://github.com/stupside/castor/commit/4929538b032701fd63d4697c4b634008d4f69d69))
* **client:** open the LAN relay for the first play that needs one, never to close it ([eebaff4](https://github.com/stupside/castor/commit/eebaff464055dc3234c62b3a7e55b30a8c98d519))
* **cli:** refuse to start when the log file cannot be written ([eab1c61](https://github.com/stupside/castor/commit/eab1c614050ee340dec0ddc0311c32747a425aec))
* **cmd:** stop a cast only on Ctrl+C, not when its watch ends ([26c42f3](https://github.com/stupside/castor/commit/26c42f3ff2ba475d274e14a121e7ece05ada3bd5))
* **cmd:** stop the cast and release the drive whatever ended its watch ([13f97f2](https://github.com/stupside/castor/commit/13f97f207e5aada69451ad7e0f632804ad88ee51))
* **cmd:** take an episode's season and number as unsigned, not wrapped from negatives ([5dde342](https://github.com/stupside/castor/commit/5dde34269c5bb4b7317ee508fdf3ceebf496cd48))
* **dash:** never give the last indexed segment a negative length ([5faa050](https://github.com/stupside/castor/commit/5faa05032eae470a126a7890383914d73f28539a))
* **device:** judge a renderer gone by how long it went unanswered, not how many polls failed ([56ceb1c](https://github.com/stupside/castor/commit/56ceb1c4205f072921c1a92dfe47b6784ecadac5))
* **extract:** bound each page and the snapshots taken of it ([f7391be](https://github.com/stupside/castor/commit/f7391be1d5e901d7d8ce0782b820a270b0d8c971))
* **ffmpeg:** count a kill as castor's only while ffmpeg is still running ([3861fde](https://github.com/stupside/castor/commit/3861fde2d678bb3e0605add11da1ee584c7ab663))
* **ffmpeg:** tell castor's own kill from a failure on Windows ([4ba63d6](https://github.com/stupside/castor/commit/4ba63d66a2f4b76aeffaa957346bd1287c0992cd))
* **follow:** answer a segment whose origin broke before its first byte with a bad gateway ([89227ae](https://github.com/stupside/castor/commit/89227ae64353bf6d44e7fdcbe0d2a55e5045213c))
* **follow:** let an empty first window leave the repackaging choice open ([8edd6ef](https://github.com/stupside/castor/commit/8edd6ef45f7b4ef035680da7490632c6c3f7960c))
* **follow:** read a timeline's first window again after a passing failure ([1ba319c](https://github.com/stupside/castor/commit/1ba319c50be49c22e2c3fd4eb2acf476440de197))
* **hls:** open each segment on its own connection ([1aff98c](https://github.com/stupside/castor/commit/1aff98c798f60195fa74873c1b0a2945675d54aa))
* **hls:** read an init BYTERANGE without an offset from the first byte ([2bb368f](https://github.com/stupside/castor/commit/2bb368f13b973c340fccfa5d5ea95a060174acfa))
* **rank:** admit a short stream as a last resort ([9524ca1](https://github.com/stupside/castor/commit/9524ca1ecdc60819e909b267743846f20f5f473f))
* **subtitles:** ask for a language in whisper's own codes ([3535589](https://github.com/stupside/castor/commit/353558928bbd586089041b4aff5ca35ccbfe1416))
* **timeline:** count a seam in the discontinuity sequence when its tag scrolls off ([467513b](https://github.com/stupside/castor/commit/467513bfa859802794e9ca5230bda7760125d769))
* **timeline:** never republish a segment a lagging edge lists again, however long ago it was trimmed ([4c7b729](https://github.com/stupside/castor/commit/4c7b72976ef178bc9309d51e51d27db406b9f987))
* **transcode:** declare a seamed program's discontinuity threshold once, before its inputs ([a0808b2](https://github.com/stupside/castor/commit/a0808b2be39ac82eb4d9075bd981391a0fc274af))


### Refactors

* **api:** build a session's watchers with it, not on the first subscribe ([1733384](https://github.com/stupside/castor/commit/1733384f0e89d0745f75e935132a76fd73ff28da))
* **api:** build the server relay's proxy once, reading the route's values per request ([3231f03](https://github.com/stupside/castor/commit/3231f035f1d73df28ca97a572c0c0c5101bd0f88))
* **api:** carry a stream's headers as one value each ([60506ed](https://github.com/stupside/castor/commit/60506eda207d7bc518ec0d24bf1f94af5b94b815))
* **api:** count in unsigned integers wherever nothing can be negative ([17b9c42](https://github.com/stupside/castor/commit/17b9c42b3c460ba6040329177fbc9900cbac0275))
* **api:** drop rules and comments the contract already states otherwise ([81acb5f](https://github.com/stupside/castor/commit/81acb5fbe04f227450f92cc1839c30d88ca1f0f0))
* **api:** hold a cast's lifecycle in a looplab/fsm machine ([11a4638](https://github.com/stupside/castor/commit/11a4638ec67c8077aecff43061db650afddcb459))
* **api:** hold to the contract what a client sends, and say less in its comments ([7922754](https://github.com/stupside/castor/commit/7922754c230a0c839a5d7e374353bea6af0b4d72))
* **api:** leave a watch's log level unset to ask for none ([1f35f2e](https://github.com/stupside/castor/commit/1f35f2efd58e5712c97b4ef7580aee5fccb1c20e))
* **api:** lend a cast one device, so its commands carry no handle ([0e8be23](https://github.com/stupside/castor/commit/0e8be239f313e1b54d7a014059caeeb2006caa62))
* **api:** lend a device with whether it fetches for itself, not a whole profile ([d417fac](https://github.com/stupside/castor/commit/d417facd146f0d257fed919c16b18418e1f9ec6d))
* **api:** name the cast RPCs Start and Stop, as every other RPC names its verb alone ([fd27212](https://github.com/stupside/castor/commit/fd272128d0139769e194fdb86126ab9e7a1791ff))
* **api:** name the device families after what they hold ([b74f196](https://github.com/stupside/castor/commit/b74f1967194f2be47f1b26c1a66d14f768d53488))
* **api:** read a watch's end one way, and name every refusal the server makes ([e12281a](https://github.com/stupside/castor/commit/e12281ac4276f9e2a165d2b8b256cbf8a4e8e7a9))
* **api:** reduce a cast's status to its counters ([4e609c4](https://github.com/stupside/castor/commit/4e609c4c601f6bdc85be5166268ad3f06662a510))
* **api:** wait for a device for the undriven constant, not a field set to it everywhere ([47463f7](https://github.com/stupside/castor/commit/47463f73d3d05e7a20091a9f27ce76ce40c3012a))
* **browse:** read fields directly instead of through one-line accessors ([0015a6b](https://github.com/stupside/castor/commit/0015a6b90a94bc8580af2642ad841496456dbae3))
* **cast:** cast straight through attempt, and drop what only tests or nothing reached ([a7b5918](https://github.com/stupside/castor/commit/a7b5918c7900e946fdb29fdbd69ae4a8dc6db46e))
* **cast:** declare one Listeners port, in deliver, and open every delivery through it ([6153550](https://github.com/stupside/castor/commit/6153550bbdba71337ec14cd177999f8833d414d0))
* **cast:** finish renaming read plans to fetch plans and candidates to streams ([0a4f93c](https://github.com/stupside/castor/commit/0a4f93c5fbbc8081aeb86f50731dc4d177a4ad1d))
* **cast:** fold single-file packages into their parents ([bac478c](https://github.com/stupside/castor/commit/bac478c858cfcd5494f2f6b3ea0aad5562284c26))
* **cast:** leave a slow source to the device ([dc584de](https://github.com/stupside/castor/commit/dc584de7a9f494b023920a4b63931be034536e7c))
* **cast:** name read and watch by what they decide: fetch and health ([4d78b0a](https://github.com/stupside/castor/commit/4d78b0a5a2570b6b68d468a5372506bbafde0301))
* **cast:** open deliveries through a Listeners port, under the cast's context ([6f4764c](https://github.com/stupside/castor/commit/6f4764c72ae5d8be7be80b7ce03fd6712196d402))
* **cast:** run an attempt on the values its steps return, and drop the indirection around it ([9418e0f](https://github.com/stupside/castor/commit/9418e0f63d007944e9ec6a1ce9fa5bcec816b205))
* **cast:** stand in a Turns nobody hears once, not a nil check at each turn ([f1e3a16](https://github.com/stupside/castor/commit/f1e3a16d5942b9046941bca94571ada397a608e3))
* **client:** leave the contract's rules to the server, and say plainly when it refuses ([462167a](https://github.com/stupside/castor/commit/462167a8cf2090d2b57d360def9c65f9fcb0623e))
* **client:** name the LAN address port Address, as one address it is ([f3874ea](https://github.com/stupside/castor/commit/f3874eac920574b66c2712446b60053f347f7b89))
* **cli:** name and group the castor command line ([95463fa](https://github.com/stupside/castor/commit/95463fa17076631785f83d3436cd7c9f001bcbb3))
* **cmd:** build the missing TMDB key error with errors.New, having nothing to format ([758372a](https://github.com/stupside/castor/commit/758372a08917291719b6746ecb53abec38628371))
* **cmd:** load config with a method, and inline the forwarders around it ([6fa2ee8](https://github.com/stupside/castor/commit/6fa2ee8f1f8c8afca9e4fc7cdb938d11ccda929d))
* **config:** connect through remote.url and serve as castor server, one section each ([0fb2839](https://github.com/stupside/castor/commit/0fb283926af0eecf9dd0ce646d741ca768ee1047))
* **config:** identify a link with its cast's own resolver ([206865a](https://github.com/stupside/castor/commit/206865a1c8dfa80dfe55d7b6be7e8ddae93abf82))
* **deliver:** open deliveries on their Opening, with what is written where passed alongside ([adf9f61](https://github.com/stupside/castor/commit/adf9f61a3dcf9d7770be6c6b0b0f63bc9b5d649b))
* **device:** drop what renderers and screens carried for nobody ([e8bea2b](https://github.com/stupside/castor/commit/e8bea2b69ffc27b381c888682ee1158ee4a6df21))
* **device:** hold constants every caller passed as parameters ([6a6ddad](https://github.com/stupside/castor/commit/6a6ddad593b61725150b4060daf40c8e9de8f93a))
* drop the probing and extraction layers that held nothing ([3991e73](https://github.com/stupside/castor/commit/3991e73aca030f483539836ada7e869558e7cdcd))
* drop the standalone castor-api and castor-media binaries ([5c74b40](https://github.com/stupside/castor/commit/5c74b4013109cc640da4f956cd58a503a7856622))
* drop what media and subtitle carried for nobody ([6709ecc](https://github.com/stupside/castor/commit/6709ecc44848f60de794b6ec5a8f4cc6372bd18b))
* **execute:** call what an attempt keeps its files in a directory, not a one-field workspace ([68da4a9](https://github.com/stupside/castor/commit/68da4a9ece635fffec7b0689c0e54661ef39dda5))
* **extract:** drive the page and parallel extraction without single-use callbacks ([8a77d75](https://github.com/stupside/castor/commit/8a77d75f28ae02002150d222d6dd7da07fcc9c5d))
* **extract:** log what a page capture finds under the extraction's context ([53bc091](https://github.com/stupside/castor/commit/53bc091c99540569994e2a7aba1a66a7fa5aa12f))
* file each screen with what it picks: titles and devices ([27c3e62](https://github.com/stupside/castor/commit/27c3e62210f2acf3ea2ea2ed4d003f2159a50d2a))
* give the API server and the media server the same shape ([160ddb6](https://github.com/stupside/castor/commit/160ddb60eccc631e9584a87a97c944812c490a98))
* keep both servers' casts in one shared registry ([c7d478d](https://github.com/stupside/castor/commit/c7d478dcde91cf93ba744a4f30a77d791ac4e30c))
* make the TUI and the process plumbing part of the binaries ([77ce80e](https://github.com/stupside/castor/commit/77ce80e656cfd9493aff38c71d0466c55006b79a))
* **media:** collect a program's header names in one place ([ef39734](https://github.com/stupside/castor/commit/ef39734005606437b46c06936339c85a8659f107))
* **media:** name and group the ffmpeg, probe, media and subtitle tools ([ad6d943](https://github.com/stupside/castor/commit/ad6d943b61bda7d94809abb29ffc9a67f9b56ca7))
* **media:** name and group the source and extract packages ([6c68eb5](https://github.com/stupside/castor/commit/6c68eb5cec6cffcc3ffdf5b32ddb4d8a682f02d9))
* **media:** name the cast runtime after what it does ([ab2ff64](https://github.com/stupside/castor/commit/ab2ff6496c814820f114b13330b75fe12374db7b))
* **screens:** move the browser, the picker and the logger to Charm v2 ([7b977e4](https://github.com/stupside/castor/commit/7b977e459c00328de0e7eb42fe092e94109b6f0c))
* **screens:** name the bar colours, draw every list with one delegate, ask the background once ([20b0c58](https://github.com/stupside/castor/commit/20b0c58a87f898f98b044ddb43f8f6ce3e246de1))
* **source:** call a found link a Stream, as everything that hands it on does ([aa29554](https://github.com/stupside/castor/commit/aa29554f8157fe04dc6611d292e0cc2717ead2bd))
* **source:** give each port only what its implementations use ([e7eef87](https://github.com/stupside/castor/commit/e7eef874021d2a21c83450e5a7c683322b130526))
* **source:** identify a link without a header nobody passes, and inline config's one-line helpers ([ceb8b49](https://github.com/stupside/castor/commit/ceb8b49308d57418ebd7c3a70414eb7e7f6b4d5d))
* split castor into sealed media server, API server and TUI tiers ([10224d5](https://github.com/stupside/castor/commit/10224d5bf322409c48b0ea029177b369209ce703))
* start both servers through transport.Listen and transport.Loopback ([5137b23](https://github.com/stupside/castor/commit/5137b23f2767cea8e72b058ad57b28e888b60241))
* **subtitle:** name subtitle.Word and ensure the VAD model directly ([c431374](https://github.com/stupside/castor/commit/c4313747b477971043c2258a248245f64b3e8e09))
* **transport:** split serving and the token rule out of transport.go ([10bfe91](https://github.com/stupside/castor/commit/10bfe915326d452a0404035c5437b84658a20b7a))
* unexport what no other package reads ([9da3c09](https://github.com/stupside/castor/commit/9da3c09fc2bcbf16788040ea26252619ff621da9))


### Documentation

* **config:** say subtitle burn-in reaches DLNA only ([9f746f2](https://github.com/stupside/castor/commit/9f746f28798aee7152b74138d058d5c5988212ba))
* **container:** say what the package describes ([4f17f03](https://github.com/stupside/castor/commit/4f17f03532488b51f7c7211f71c78b04970db1a4))
* explain how castor works inside in ARCHITECTURE.md ([b8f9b8f](https://github.com/stupside/castor/commit/b8f9b8f0c1a6a776ff2c58b57afc080076392208))
* **readme:** cover using castor, and leave its mechanics to ARCHITECTURE.md ([b7c3a9d](https://github.com/stupside/castor/commit/b7c3a9d775dd210dc30673a6c040d8271b13f947))
* rewrite ARCHITECTURE.md around tiers, topologies and boundaries ([d9a3235](https://github.com/stupside/castor/commit/d9a3235a52c938f6be5cac59467c4d59ca854ab6))
* say what the media server model does where comments and SECURITY.md still told the relay's ([4dafe9a](https://github.com/stupside/castor/commit/4dafe9a2c446a100537f3fa687bd5b9c8afd99a6))
* tighten the readme, contributing and security guides ([d0c3e38](https://github.com/stupside/castor/commit/d0c3e38b5f10bfd41530824736610e6ccfdadc04))
* **transcode:** say the splice timestamp threshold holds for every input ([7ee3d01](https://github.com/stupside/castor/commit/7ee3d01decf04bfd4ce79aaa7f5bfc076581d3cd))


### Build System

* **deps:** bump Go to 1.27.1 and the direct dependencies ([83ea245](https://github.com/stupside/castor/commit/83ea245d65ee711a3c6792f4a104236c38a0eb99))
* **deps:** bump whisper.cpp to v1.9.4 ([a1f75e3](https://github.com/stupside/castor/commit/a1f75e38bbe81a4dbe5b326dc44ae3c7503fed42))
* drop the tooling that did nothing ([b563df3](https://github.com/stupside/castor/commit/b563df3876a4a7711059e0b069a952a394ea8c1c))
* move to Go 1.27 and the latest dependencies ([f4056b8](https://github.com/stupside/castor/commit/f4056b8617b1eda524916a8927e413f4a778348d))
* **release:** rename the cask script to brew.sh ([e3151ed](https://github.com/stupside/castor/commit/e3151ed9de224e2ba67b0c5b3ca4c30810d22a38))


### Continuous Integration

* bump the action pins and lint tools, and gate on go fix ([fb187c8](https://github.com/stupside/castor/commit/fb187c8406f24ed6b2eb608f667da40742978b7f))
* leave the e2e suite out of the test job ([44ac704](https://github.com/stupside/castor/commit/44ac704fc2d8a2efae427d0cb338c274d82cd128))
* lint the contract, refuse breaking changes to it, and check gen/ is current ([c6ddb87](https://github.com/stupside/castor/commit/c6ddb8730bbe1208acdb1330b7ce830081b30ca0))
* lint the sealed tiers, keep castor-api cgo-free, and release the three binaries ([a48242a](https://github.com/stupside/castor/commit/a48242a66e2bf1c0ecfb0d0862d1173d190af93b))
* **release:** publish only the castor binary ([7b3858b](https://github.com/stupside/castor/commit/7b3858b22f29689b2d698d38b784afac2c3bed16))
* run go fix -diff as the gate itself, so a failure shows its diff ([c2b8014](https://github.com/stupside/castor/commit/c2b8014cc7178d1c48d2fcdbdc4a99ebf6d84e45))

## [1.9.0](https://github.com/stupside/castor/compare/v1.8.1...v1.9.0) (2026-09-22)


### Features

* **ffmpeg:** read a demuxed program as two inputs ([2333d0f](https://github.com/stupside/castor/commit/2333d0f3f027928be826115649fb7151e463e034))
* **media:** let one program span two renditions ([279407f](https://github.com/stupside/castor/commit/279407f795dd13f7a801207cf111e17bd60fe846))
* **release:** ship windows builds, and carry ffmpeg's side feeds over loopback ([#66](https://github.com/stupside/castor/issues/66)) ([5ffeb79](https://github.com/stupside/castor/commit/5ffeb7936199acba403feab2f2a411b32d650edf)), closes [#61](https://github.com/stupside/castor/issues/61)
* **resolve:** keep the chosen variant's audio rendition ([a70b814](https://github.com/stupside/castor/commit/a70b814c9e2300a3b95c7179000565c291682470))


### Bug Fixes

* **cast:** serve sources that need request headers ([#54](https://github.com/stupside/castor/issues/54)) ([f2fe1e1](https://github.com/stupside/castor/commit/f2fe1e11624150052b0d5cc0dad21c60ab9bde1c))
* **deps:** bump golang.org/x/mod v0.38.0 → v0.40.0 ([#62](https://github.com/stupside/castor/issues/62)) ([2cf0627](https://github.com/stupside/castor/commit/2cf0627f2e84bc7306217de82f2b212d09edcf3f))
* **release:** clear quarantine through the install steps homebrew now expects ([#65](https://github.com/stupside/castor/issues/65)) ([9a9b340](https://github.com/stupside/castor/commit/9a9b3408b13e7ce61b0c128702b892d622f1e8e9)), closes [#63](https://github.com/stupside/castor/issues/63)
* **resolve:** never cast a variant that carries no video ([6bb905e](https://github.com/stupside/castor/commit/6bb905e02997a163408763d85e89cf35c9103db1))


### Refactors

* **resolve:** decode HLS playlists with a spec parser ([e714185](https://github.com/stupside/castor/commit/e714185aaa29a6f7242c6e73d44d80ae5c19dfa8))

## [1.8.1](https://github.com/stupside/castor/compare/v1.8.0...v1.8.1) (2026-07-27)


### Bug Fixes

* **ffmpeg:** pass -nostats to the puller invocation ([5fb1650](https://github.com/stupside/castor/commit/5fb165033857fa7e15033150e952f31cbd233723))
* **resolve:** tiebreak equal-bandwidth candidates by height ([3a25c70](https://github.com/stupside/castor/commit/3a25c70173ec67c143e427a6fe72d6e43327e222))

## [1.8.0](https://github.com/stupside/castor/compare/v1.7.1...v1.8.0) (2026-07-27)


### Features

* add Roku cast target ([#50](https://github.com/stupside/castor/issues/50)) ([f109d47](https://github.com/stupside/castor/commit/f109d47c061ea9a2fe2bff31ab8885967fecfde9))
* **cast:** surround audio and compiler-isolated device strategies ([6434ba6](https://github.com/stupside/castor/commit/6434ba659b2cfb77e0003194dd129a83c20c77d8))
* **device:** connect by pinned host, skipping discovery ([dd876cd](https://github.com/stupside/castor/commit/dd876cde770c5501128d2b5b723cb79b6e1bd142)), closes [#24](https://github.com/stupside/castor/issues/24)
* **extract:** disable site isolation to capture cross-origin iframe requests ([bd32c0e](https://github.com/stupside/castor/commit/bd32c0e1d14257cda5aa6cd2d8e6f23aadb62901))


### Bug Fixes

* **cmd:** propagate picked device's type and host, not just name ([3af67b9](https://github.com/stupside/castor/commit/3af67b90e246f018ca424cf8a828d1c19cc90ace))
* **device:** derive each family's SelfFetch from its own selfFetches() ([857ac35](https://github.com/stupside/castor/commit/857ac354d6b92e3d6594da05bb946bd3fc13f0b8))
* **device:** retry DLNA SetAVTransportURI/Play through transient 705 Transport is locked ([6130364](https://github.com/stupside/castor/commit/61303645ae996b1148a60736ba542fc822c26566))
* **ffmpeg:** reject subtitle burn-in without a video encoder ([faf9e25](https://github.com/stupside/castor/commit/faf9e25f7f9d4567bed313f94c549d431b43a11d))
* **media:** normalize BitDepths nil-vs-empty handling to match Profiles ([42dde0b](https://github.com/stupside/castor/commit/42dde0bc812b449e3d9ddbdf199b1ee24c9d0674))


### Refactors

* **cast:** add SelfFetch renderer capability ([d9f9213](https://github.com/stupside/castor/commit/d9f92134e1787a9b630c5ab8eb5c062d0e492410))
* **cast:** capability-driven composition engine ([a7b3f07](https://github.com/stupside/castor/commit/a7b3f07fba7577dd599e737dd52f8ab74d4243fa))
* **cast:** tier renderers below shared building blocks ([2ff4fa8](https://github.com/stupside/castor/commit/2ff4fa8824cdc20c377a1a723eb7c187255b9b6d))
* **device:** dispatch renderer families through a strategy registry ([69d3798](https://github.com/stupside/castor/commit/69d37983f91dd352a030c29ca57c9c78d8c29fd1))
* type sentinel strings as named constants and enums ([b84a396](https://github.com/stupside/castor/commit/b84a3964427bac319bea3e0105a1c4d01b6d3522))


### Documentation

* document how to verify the image SBOM and provenance ([c675665](https://github.com/stupside/castor/commit/c6756652e48078192aeec1a02b088dea9e625fd6))
* document pinning a device by host ([1861a60](https://github.com/stupside/castor/commit/1861a60507f2ad495d704b0b9b3c0c67767fd78b))
* **media:** correct ProbeInfo's blanket unknown-value doc ([eaaae7c](https://github.com/stupside/castor/commit/eaaae7cfaaa8e3a4109ed92c76bf144ef71a3a87))


### Build System

* **docker:** attach an SPDX SBOM and SLSA provenance to the image ([aaaf0ee](https://github.com/stupside/castor/commit/aaaf0ee26c520340b6347011c98f7efde79713e2))
* **docker:** build image with docker bake ([cdb9de6](https://github.com/stupside/castor/commit/cdb9de6338cd787e5e8cd80e2bde43ed7076310b))

## [1.7.1](https://github.com/stupside/castor/compare/v1.7.0...v1.7.1) (2026-07-22)


### Bug Fixes

* **docker:** hw capable ffmpeg with good flag support ([#43](https://github.com/stupside/castor/issues/43)) ([44460a9](https://github.com/stupside/castor/commit/44460a9768fff628996906a521565ed2b872bd69))


### Documentation

* add Trendshift badge ([016a64a](https://github.com/stupside/castor/commit/016a64afabcd34dc7c9420050d77aec17192ad53))
* restructure installation, configuration, and docker sections ([8ecabb2](https://github.com/stupside/castor/commit/8ecabb22112cbc0dd670277e8e621f7a973cef24))

## [1.7.0](https://github.com/stupside/castor/compare/v1.6.2...v1.7.0) (2026-07-21)


### Features

* show cast target device in browse header ([986b3ee](https://github.com/stupside/castor/commit/986b3eef5b4968100676589d4cb6773ef76e93ae))


### Bug Fixes

* **cast:** return error from BuildPlan instead of panicking for unknown device type ([b4c9a6b](https://github.com/stupside/castor/commit/b4c9a6b43e4dfa6be0d8a1250968a7d33bf288a2))
* **cmd:** use CLI framework for --debug flag instead of scanning os.Args ([d4c307b](https://github.com/stupside/castor/commit/d4c307b4ce40f6ffe37ea971e18ab3ae4d66f625))
* config yaml should be optional ([#38](https://github.com/stupside/castor/issues/38)) ([33eea9f](https://github.com/stupside/castor/commit/33eea9faa9e3ab8d146210a9f174e6918b73dc1b))
* **docker:** cli cannot run in castor because of glibc version ([#40](https://github.com/stupside/castor/issues/40)) ([18b6a88](https://github.com/stupside/castor/commit/18b6a88a6a59b5d3ffe9ab3f6947a50ea6dfa82e))
* suppress unchecked Close() lint warnings ([88be120](https://github.com/stupside/castor/commit/88be1209f3569d5bd10eeb3b7962786c11ff6a90))
* wire picked device name into config before browse ([c2fa73f](https://github.com/stupside/castor/commit/c2fa73fd4464eca37bf63ac66a324989261e84ab))


### Refactors

* **cast:** collapse deviceConnector and cueSource single-implementation interfaces ([a9f92dd](https://github.com/stupside/castor/commit/a9f92dd103ad4f3c76c3550d8b912e79c9e32f11))
* **media:** simplify FormatForContentType to return (FormatInfo, bool) ([521205c](https://github.com/stupside/castor/commit/521205cef3b5dbf8d24440ddd39b0313e9487cbb))
* remove subtitle font customization support ([d78d5ed](https://github.com/stupside/castor/commit/d78d5ed0a5f43672a410f95023d0679b824f5539))
* **source:** deduplicate MIME tables and unexport Extractor.Extract ([5858b52](https://github.com/stupside/castor/commit/5858b5260884a906ba2eff184fcb2aa11a1c9852))
* **tmdb:** remove dead Page.HasMore method and SearchResult.GenreIDs field ([42c419d](https://github.com/stupside/castor/commit/42c419dc118df0a7ec4a0acd028f074dbe6e00e7))
* **whisper:** collapse WordSink interface and unexport EnsureModel/EnsureVADModel ([142e2ec](https://github.com/stupside/castor/commit/142e2ec53a3bbb5a1f9bf107c8efba38e21f1b14))


### Documentation

* **browse:** remove stale comment on inspector.load ([bebb2b2](https://github.com/stupside/castor/commit/bebb2b28b1509f8a1dad9295c2cb378a8e7853f1))

## [1.6.2](https://github.com/stupside/castor/compare/v1.6.1...v1.6.2) (2026-07-20)


### Bug Fixes

* drop component prefix and use v in release tags ([8c6a853](https://github.com/stupside/castor/commit/8c6a85383c8da1f3fed4a02cb725f73b62277a08))
* remove v prefix from cask download URL ([62c7703](https://github.com/stupside/castor/commit/62c770360ff4a7c7429ec3fc9cb65a0ea1fecd75))


### Documentation

* document resolver max_height option ([5952564](https://github.com/stupside/castor/commit/59525649ec6e47218997317ed9a0f42882b8d977))


### Continuous Integration

* skip push CI on main ([cfec78c](https://github.com/stupside/castor/commit/cfec78c88f85a0429cbccebdbc59448ebdbb4098))

## [1.6.1](https://github.com/stupside/castor/compare/castor-1.6.0...castor-1.6.1) (2026-07-20)


### Bug Fixes

* **dlna:** drive AVTransport and ConnectionManager v1, v2 and v3 ([#23](https://github.com/stupside/castor/issues/23)) ([b18f045](https://github.com/stupside/castor/commit/b18f0451c31e83e8c168190f91eb17a3eb64ed9b))
* pass ggml include path to cmake for Metal .m files ([4b70efd](https://github.com/stupside/castor/commit/4b70efd838113af32b9634acc3d96a8a6039f661))


### Continuous Integration

* fix empty release version and release on ci commits ([a241612](https://github.com/stupside/castor/commit/a241612fe21fcd4e1039e3a3500fe8c89ca26484))

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
