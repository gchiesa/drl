# Changelog

## [1.0.2](https://github.com/gchiesa/drl/compare/v1.0.1...v1.0.2) (2026-06-07)


### Bug Fixes

* remove set if absent implementation ([8a7c116](https://github.com/gchiesa/drl/commit/8a7c1167e684072aa7a04fe23a68a25016fb1933))

## [1.0.1](https://github.com/gchiesa/drl/compare/v1.0.0...v1.0.1) (2026-06-07)


### Bug Fixes

* merge state should preserve newer expirations ([af05580](https://github.com/gchiesa/drl/commit/af0558003f1258606b94651898dcd10dd93e1851))

## [1.0.0](https://github.com/gchiesa/drl/compare/v0.1.8...v1.0.0) (2026-06-03)


### ⚠ BREAKING CHANGES

* stabilising public API and configuration format for the 1.0.0 release

### Features

* **ms018:** embedded proxy implementation ([7692e90](https://github.com/gchiesa/drl/commit/7692e903416a459d1f58e5770f1fba59af6eb706))
* **ms019:** oidc implementation ([d76e30d](https://github.com/gchiesa/drl/commit/d76e30d4b66acf98e7bf40f281c2c7dd9e5de7db))
* promote to stable 1.0.0 ([f55e808](https://github.com/gchiesa/drl/commit/f55e8080eb81b4e14aaa2b0492a5d4c423186bd2))
* UI and private API support for version printing ([596405b](https://github.com/gchiesa/drl/commit/596405b5ee9419d3dd5a677bf7dae5781ade85e0))


### Bug Fixes

* linting issues in codebase ([5cd3e7f](https://github.com/gchiesa/drl/commit/5cd3e7f596c005677da980d48621d73e2079999e))


### Refactoring

* simplified code ([763aefc](https://github.com/gchiesa/drl/commit/763aefce255ef72c399b032a1260c3fbcda3fbe0))


### Docs

* update documentation ([243e348](https://github.com/gchiesa/drl/commit/243e3482013dfdbdf6cf47cd965a6d65f71fcbc8))

## [0.1.8](https://github.com/gchiesa/drl/compare/v0.1.7...v0.1.8) (2026-05-20)


### Features

* masking header via redactions in accounting rules ([4160721](https://github.com/gchiesa/drl/commit/4160721eb547bfe7ce09262dc1a6c8a1ed3e4bda))

## [0.1.7](https://github.com/gchiesa/drl/compare/v0.1.6...v0.1.7) (2026-05-17)


### Bug Fixes

* ensure Host header is reinjected for rule-based filtering ([110db6b](https://github.com/gchiesa/drl/commit/110db6b99c153863fdafa2718d77ef29fd1519cc))

## [0.1.6](https://github.com/gchiesa/drl/compare/v0.1.5...v0.1.6) (2026-05-16)


### Bug Fixes

* moved retrieval of effectiveIP to buildEntity ([a91ff7a](https://github.com/gchiesa/drl/commit/a91ff7a3d7df7efc2266900d8fed747fa72fbe7c))

## [0.1.5](https://github.com/gchiesa/drl/compare/v0.1.4...v0.1.5) (2026-05-14)


### Features

* add x-forwarded-for suppport ([ec3dc11](https://github.com/gchiesa/drl/commit/ec3dc113d6f46177af8aa1194537b2d014eadd58))


### Bug Fixes

* bug evaluating the headers case sensitivity ([e75148c](https://github.com/gchiesa/drl/commit/e75148ca4e7276394be84a21cd70d95a4cc3119a))
* linting issue ([86e37c5](https://github.com/gchiesa/drl/commit/86e37c5cb7cd9e0422bdc66029ca45504ab114c9))


### Docs

* add contribution, license and security markdown + finalized some other docs ([d513034](https://github.com/gchiesa/drl/commit/d51303448646282b5e8c7a93357fa02b525d0a63))

## [0.1.4](https://github.com/gchiesa/drl/compare/v0.1.3...v0.1.4) (2026-05-06)


### Features

* **mx017:** internal api update to support v1 prefix and swagger docs ([61c1829](https://github.com/gchiesa/drl/commit/61c1829a29f492a40d5e57d5e2d885541ff3c36d))
* switched to go-fiber scalar for rendering ([5fb726f](https://github.com/gchiesa/drl/commit/5fb726f2eb1004602cfa2e6f7001a720875e5971))


### Docs

* fix typo ([e01445e](https://github.com/gchiesa/drl/commit/e01445edd1afc4c2b84253abd6e811e1df175f8d))

## [0.1.3](https://github.com/gchiesa/drl/compare/v0.1.2...v0.1.3) (2026-04-30)


### Bug Fixes

* amd64 build flag with docker v1 support ([d720a59](https://github.com/gchiesa/drl/commit/d720a59bbe767e8d13bf460a2af2e0da08ddf3d4))

## [0.1.2](https://github.com/gchiesa/drl/compare/v0.1.1...v0.1.2) (2026-04-30)


### Bug Fixes

* amd64 build flag with better compatibility ([5597eba](https://github.com/gchiesa/drl/commit/5597eba1cf7143a9616528899b2b1a4a5619a7ee))

## [0.1.1](https://github.com/gchiesa/drl/compare/v0.1.0...v0.1.1) (2026-04-29)


### Bug Fixes

* wrong k8s lookup mode ([71b9bc8](https://github.com/gchiesa/drl/commit/71b9bc8f44f71e81a7d6fb67896585a270af0fff))

## 0.1.0 (2026-04-29)


### Features

* **ms0010:** move ristretto to otter cache ([e6f318b](https://github.com/gchiesa/drl/commit/e6f318b3e4271007c1b2fb194f966a31d47e29ba))
* **ms001:** scaffolding manual test ([10e6332](https://github.com/gchiesa/drl/commit/10e63328f765696f6c3477493008e6644b0ef099))
* **ms002:** implement membership discovery ([a129e33](https://github.com/gchiesa/drl/commit/a129e3395828074a0d3434463e194942a9daea91))
* **ms003:** implement KDL configuration language ([ccb7a9f](https://github.com/gchiesa/drl/commit/ccb7a9f76536eb1ae8b441659a3615a677a80221))
* **ms004:** implement scram authentication ([7db12f6](https://github.com/gchiesa/drl/commit/7db12f662a1cd52e52b2937a39b413a2ed186c30))
* **ms004v2:** implement digest authentication instead scram ([2aa921e](https://github.com/gchiesa/drl/commit/2aa921e75dc075095e276051f48cbd4faa9d5ef2))
* **ms005:** implement distribute in memory caching for accounting and blocklist ([3854e19](https://github.com/gchiesa/drl/commit/3854e19573469074e7f33892042617d822e1f17c))
* **ms006:** implement private api endpoint to block/unblock ([bb82c36](https://github.com/gchiesa/drl/commit/bb82c36aa157ad3f3d24df0730c58abac33e92b5))
* **ms006v2:** implement expiration and get all ([b4f74e9](https://github.com/gchiesa/drl/commit/b4f74e918f5d3da34e092d518377aba82198cf80))
* **ms007:** implemented envoy integration with grpc ([89a14ab](https://github.com/gchiesa/drl/commit/89a14abba32293f002e13182ce4ad37c83a9b115))
* **ms008:** implemented accounting and peer propagation ([7f2235a](https://github.com/gchiesa/drl/commit/7f2235a84e63cab953fe85ffa6a102a77dff7c5b))
* **ms009:** implement blocklist enforcement and propagation ([759d3dc](https://github.com/gchiesa/drl/commit/759d3dc61182076bc873849478490f7702af9198))
* **ms011:** moved from native udp transfer to membership/serf ([2bb6917](https://github.com/gchiesa/drl/commit/2bb6917a93eb47057b8f467b7273142f732ce893))
* **ms012:** implement membership encryption in transit ([73c6eaf](https://github.com/gchiesa/drl/commit/73c6eaf4520ff4747c3ad51ad1ed893a42637eed))
* **ms013:** implement cache handover on sigterm ([d36e4b4](https://github.com/gchiesa/drl/commit/d36e4b4d667ebcfb7dd22d4dd973224c046bd697))
* **ms014:** endpoint for bulkLoad ([c38bb4b](https://github.com/gchiesa/drl/commit/c38bb4b33749dc50b1d6b9c95a36966d6d535d65))
* **ms015:** implement token bucket rate limiter and config api endpoint ([a3c28c7](https://github.com/gchiesa/drl/commit/a3c28c74f724bb6502387153267065d0445bb30e))
* **ms016:** ui single page implementation ([ad805d2](https://github.com/gchiesa/drl/commit/ad805d283b96fdfba0eda4235f8f7946154365d6))
* **ms016v2:** prepared docs ([1ad8ad3](https://github.com/gchiesa/drl/commit/1ad8ad3485941c2860445ec2425cd6b8019cb29b))
* **ms016v3:** minor cosmetic fix ([96c124c](https://github.com/gchiesa/drl/commit/96c124c56303f2f14fea499ac76df0dc25a365be))
* **ms016v3:** separate authentication and fix on E2EE ([524aa9a](https://github.com/gchiesa/drl/commit/524aa9a0403d4ba891fa73577b5ccab35632ab98))


### Bug Fixes

* blocking engine retry after ([fde3c3b](https://github.com/gchiesa/drl/commit/fde3c3b7d353ad8c2d4ea9e20b82d10c46f0ad27))
* bug on synchronization ([d9bfc40](https://github.com/gchiesa/drl/commit/d9bfc40717f9ba20ecb34d14a374c4b29a70c7f2))
* minor fixes ([4c65a5d](https://github.com/gchiesa/drl/commit/4c65a5d9d1610ba3ba3906f307fa4fe4bd7e44a2))
* **security:** update vulnerable dependencies ([1047f2f](https://github.com/gchiesa/drl/commit/1047f2fa88089521582e1a57a32824ef7b3b2a87))
* tests for membership pkg ([5edf2ee](https://github.com/gchiesa/drl/commit/5edf2ee62e685e70287cc16f97a61d7b2d94a483))
* tracking issue on blocking engine ([909f7d3](https://github.com/gchiesa/drl/commit/909f7d399e7fbf7ec5ec962dd3e12acf3ce44fe6))


### Performance

* implement radix tree and performance test config ([49de87a](https://github.com/gchiesa/drl/commit/49de87a74b610d1ad19e87b6b92e3890a4179ad1))
* improved caching updates and converged on consistent types ([4e1c9d6](https://github.com/gchiesa/drl/commit/4e1c9d673573eaaa9f2fd706a035794e5f88ee75))


### Docs

* fix on documentation ([13c1223](https://github.com/gchiesa/drl/commit/13c12235c869d98abee031fab1260779da1f2ba6))
* implement static documentation building ([7dc79b6](https://github.com/gchiesa/drl/commit/7dc79b6b2b2bf060b157ee7687b0571699e54718))
* migrated static doc building to Hugo ([3ff153c](https://github.com/gchiesa/drl/commit/3ff153c45690a91cda8831c932e69b7296500fae))
* publishing test reports ([14c3010](https://github.com/gchiesa/drl/commit/14c3010aa728693267cb8b8cc163e8f726a9c304))
* update documentation and architecture diagrams ([2eea7f7](https://github.com/gchiesa/drl/commit/2eea7f78fc3125b0fa49aa64c316d08f04bc041d))


### Refactoring

* **api:** standardize blocklist method signatures ([1de05f7](https://github.com/gchiesa/drl/commit/1de05f70d3340977b034c07feb2016645011753c))
* cleanup code for api ([7ab0eff](https://github.com/gchiesa/drl/commit/7ab0effd4d0ff82fe0e897619d2d200489e8c691))
* cleanup code for config ([29207b7](https://github.com/gchiesa/drl/commit/29207b7aa2313884522d6ec862a64b8f97d7dcd4))
* cleanup code for memberlist ([e1045a0](https://github.com/gchiesa/drl/commit/e1045a0aba8a9922bfee5807c8bd787cf65e428d))
* cleanup unnecessary codebase ([85535ac](https://github.com/gchiesa/drl/commit/85535ac41489e7045149c44c74719eed019b4d9b))
* consolidate entity model methods ([8a42950](https://github.com/gchiesa/drl/commit/8a42950acc808a46fa7109ea73d50d4882283ceb))
* fix linting ([2f63a71](https://github.com/gchiesa/drl/commit/2f63a716d73b788f7b114d0f39b67805ddd9a401))
* improved naming to match envoy protocol ([72d5bf1](https://github.com/gchiesa/drl/commit/72d5bf1fb456cab68b85611575e64428cc8d1eb2))
* refactor config kdl ([9ff9ade](https://github.com/gchiesa/drl/commit/9ff9ade8bad28816061e54f164338b76aea3e438))
* refactor main entrypoint ([6ec74e9](https://github.com/gchiesa/drl/commit/6ec74e90d5e5e2da893fa132907f1a2c2940961a))


### Other

* cleanup untracked files ([52770dd](https://github.com/gchiesa/drl/commit/52770dd5e5600a36f6c4e3e59b3d330111d203fa))
* create FUNDING.yml ([130cecd](https://github.com/gchiesa/drl/commit/130cecd4450d973bac6d21ec7f503d6f7de3ebec))
* **main:** release 1.0.0 ([76e3330](https://github.com/gchiesa/drl/commit/76e33305ab35d7d83e64f76df0a7e9de49f89b81))
* **main:** release 1.0.1 ([74cf56d](https://github.com/gchiesa/drl/commit/74cf56de22939d583686602421461fcf3d15f77c))
* reset release version to 0.0.0 ([b26f2e5](https://github.com/gchiesa/drl/commit/b26f2e5ac0f4c733ddfeeb634eccc997323eb669))
* update FUNDING.yml file ([706ea22](https://github.com/gchiesa/drl/commit/706ea2243f00f6a513ca203f3595be71c516a769))
