# Changelog

## [0.3.0](https://github.com/voidrot/chorecast/compare/server-v0.2.0...server-v0.3.0) (2026-11-06)


### Features

* **android:** add an in-app inbox for kids and parents ([#136](https://github.com/voidrot/chorecast/issues/136)) ([429c210](https://github.com/voidrot/chorecast/commit/429c210030c3c2838b1db352843b59db25b6129f))
* **android:** add parent mode, a PIN pad and avatars to the shared tablet ([#137](https://github.com/voidrot/chorecast/issues/137)) ([1e7ec78](https://github.com/voidrot/chorecast/commit/1e7ec78708b5ea97190f4f995f1b495ab2ab913c)), closes [#33](https://github.com/voidrot/chorecast/issues/33)
* **android:** complete a chore with CameraX photo evidence ([#115](https://github.com/voidrot/chorecast/issues/115)) ([864525a](https://github.com/voidrot/chorecast/commit/864525a2d45cce0f6d4f5cff48f1d4a5f387a9f4)), closes [#101](https://github.com/voidrot/chorecast/issues/101)
* **android:** let parents pair a device by showing a QR code ([#140](https://github.com/voidrot/chorecast/issues/140)) ([a41522f](https://github.com/voidrot/chorecast/commit/a41522f87648abba8c938069589b1b65134bca63)), closes [#133](https://github.com/voidrot/chorecast/issues/133)
* **android:** show a kid's chores for today with points and streak ([#107](https://github.com/voidrot/chorecast/issues/107)) ([6923732](https://github.com/voidrot/chorecast/commit/6923732279fc12df6f3a5f19240361c506ddd5e5)), closes [#100](https://github.com/voidrot/chorecast/issues/100)
* **android:** show a kid's past chores with their evidence ([#126](https://github.com/voidrot/chorecast/issues/126)) ([36a1a28](https://github.com/voidrot/chorecast/commit/36a1a28e185c224a4fa0ec08689d6acf29a1f1a0)), closes [#102](https://github.com/voidrot/chorecast/issues/102)
* **android:** sync in the background and remind kids before chores are due ([#139](https://github.com/voidrot/chorecast/issues/139)) ([23e9f99](https://github.com/voidrot/chorecast/commit/23e9f997094102091fd696296436b0b9a9f256c3))
* **server:** add an in-app notification inbox ([#109](https://github.com/voidrot/chorecast/issues/109)) ([6848bf9](https://github.com/voidrot/chorecast/commit/6848bf9815dac722ad1583aa1b3298c246329056)), closes [#98](https://github.com/voidrot/chorecast/issues/98)
* **server:** add points ledger with reversals, adjustments and balances ([cf2910b](https://github.com/voidrot/chorecast/commit/cf2910b8335ce5169101fe11e2de36bade301239))
* **server:** add scheduled backups and backup and restore commands ([#110](https://github.com/voidrot/chorecast/issues/110)) ([b3f7ea5](https://github.com/voidrot/chorecast/commit/b3f7ea548ec77de6d8138cd31d8b04968e4b69b7)), closes [#36](https://github.com/voidrot/chorecast/issues/36)
* **server:** add the chore instance state machine with approvals, rejections, excuses and reassignment ([#84](https://github.com/voidrot/chorecast/issues/84)) ([8248235](https://github.com/voidrot/chorecast/commit/82482359b382e56bc71f901c0e7cd6450789a1ff)), closes [#25](https://github.com/voidrot/chorecast/issues/25)
* **server:** add uploaded photo avatars for kids ([#125](https://github.com/voidrot/chorecast/issues/125)) ([70cff8e](https://github.com/voidrot/chorecast/commit/70cff8e6d50c8440b79be6e8e9892fe7ad259575)), closes [#88](https://github.com/voidrot/chorecast/issues/88)
* **server:** auto-approve submitted chores after the household or per-chore timeout ([#86](https://github.com/voidrot/chorecast/issues/86)) ([75518cd](https://github.com/voidrot/chorecast/commit/75518cd25d83450b85ef7ff76902ab3225e9dfc8)), closes [#29](https://github.com/voidrot/chorecast/issues/29)
* **server:** close kids' days and weeks, marking open chores missed with their penalty ([#82](https://github.com/voidrot/chorecast/issues/82)) ([f10dce0](https://github.com/voidrot/chorecast/commit/f10dce09974544694cec36ecdf0f9fef840cbef8)), closes [#24](https://github.com/voidrot/chorecast/issues/24)
* **server:** delete evidence photos media_retention_days after the chore resolves, and abandoned uploads after 24 hours ([8f7d3c4](https://github.com/voidrot/chorecast/commit/8f7d3c4fdde06efb36f6aa17d6944c0b241aab99))
* **server:** enforce chore evidence on completion and keep each submission's checklist, note and photos ([#87](https://github.com/voidrot/chorecast/issues/87)) ([8f7d3c4](https://github.com/voidrot/chorecast/commit/8f7d3c4fdde06efb36f6aa17d6944c0b241aab99))
* **server:** generate chore instances for a rolling 7-day horizon ([#77](https://github.com/voidrot/chorecast/issues/77)) ([6f881e5](https://github.com/voidrot/chorecast/commit/6f881e5fead7dc71f7e266734886340f6f2481da)), closes [#23](https://github.com/voidrot/chorecast/issues/23)
* **server:** include chore points, steps and evidence on instances ([#116](https://github.com/voidrot/chorecast/issues/116)) ([6c3f93b](https://github.com/voidrot/chorecast/commit/6c3f93b4cc6039dd048a1a58dabf37721afad90b)), closes [#108](https://github.com/voidrot/chorecast/issues/108)
* **server:** include the chore description on instances ([#128](https://github.com/voidrot/chorecast/issues/128)) ([e43c2b0](https://github.com/voidrot/chorecast/commit/e43c2b0c50a2e506ea572af35746e3e09bf5e8a9))
* **server:** let a client discard its own pending photo upload ([#124](https://github.com/voidrot/chorecast/issues/124)) ([f788b25](https://github.com/voidrot/chorecast/commit/f788b25487cd0b172689683e460e4d6d5185e14a))
* **server:** list submitted chores for review and approve them in bulk ([#90](https://github.com/voidrot/chorecast/issues/90)) ([7da58cd](https://github.com/voidrot/chorecast/commit/7da58cd928d40f15522eb9456e6b310e626ed41c)), closes [#28](https://github.com/voidrot/chorecast/issues/28)
* **server:** record an owner-only audit log of household changes ([#117](https://github.com/voidrot/chorecast/issues/117)) ([2684427](https://github.com/voidrot/chorecast/commit/26844275cac904a6686501021a63aaf719d17a92)), closes [#37](https://github.com/voidrot/chorecast/issues/37)
* **server:** record backups and restores in the audit log ([#135](https://github.com/voidrot/chorecast/issues/135)) ([c9a076a](https://github.com/voidrot/chorecast/commit/c9a076ab10f0a7365a8926f0c1e2d6eb1d821ee1)), closes [#121](https://github.com/voidrot/chorecast/issues/121)
* **server:** refuse to start while another server uses the data dir ([b3f7ea5](https://github.com/voidrot/chorecast/commit/b3f7ea548ec77de6d8138cd31d8b04968e4b69b7))
* **server:** return a kid's still-pending upload when they repeat it, and cap pending uploads at 20 per kid ([8f7d3c4](https://github.com/voidrot/chorecast/commit/8f7d3c4fdde06efb36f6aa17d6944c0b241aab99))
* **server:** track daily and per-chore streaks with milestone bonuses ([#85](https://github.com/voidrot/chorecast/issues/85)) ([1f4535e](https://github.com/voidrot/chorecast/commit/1f4535eb6ea9131f49fca11cbcb2daa8462e5481)), closes [#30](https://github.com/voidrot/chorecast/issues/30)
* **server:** upload photos with duplicate detection and signed media URLs ([#78](https://github.com/voidrot/chorecast/issues/78)) ([0d3027c](https://github.com/voidrot/chorecast/commit/0d3027c2e7addab11b7c55f29524bc0316f3edcb)), closes [#75](https://github.com/voidrot/chorecast/issues/75)
* **web:** add a review queue screen with bulk approve and reject ([#106](https://github.com/voidrot/chorecast/issues/106)) ([286e4b4](https://github.com/voidrot/chorecast/commit/286e4b4d15e20731b16c0591112bae8c94e4f52c)), closes [#94](https://github.com/voidrot/chorecast/issues/94)
* **web:** add a today screen for chore status and parent actions ([#112](https://github.com/voidrot/chorecast/issues/112)) ([51364b9](https://github.com/voidrot/chorecast/commit/51364b9bc0ce95cf940efba6294f862591ec413d)), closes [#96](https://github.com/voidrot/chorecast/issues/96)
* **web:** add an owner-only audit log screen with filters ([2684427](https://github.com/voidrot/chorecast/commit/26844275cac904a6686501021a63aaf719d17a92))
* **web:** edit a chore's assignment mode, kids, rotation order and shared options ([#105](https://github.com/voidrot/chorecast/issues/105)) ([7cbb1c9](https://github.com/voidrot/chorecast/commit/7cbb1c95478e91f0e3d530a5d19b3cb777538f30)), closes [#93](https://github.com/voidrot/chorecast/issues/93)
* **web:** edit streak milestones in settings and make the nav phone-friendly ([#113](https://github.com/voidrot/chorecast/issues/113)) ([80ef894](https://github.com/voidrot/chorecast/commit/80ef894283e252f3115ef1bdf665437f4d9cf3ec)), closes [#97](https://github.com/voidrot/chorecast/issues/97)
* **web:** show a kid's points ledger, adjustments and streaks ([#111](https://github.com/voidrot/chorecast/issues/111)) ([e6a25ae](https://github.com/voidrot/chorecast/commit/e6a25ae50feed41bd0c51edf70bf8d096a952c66)), closes [#95](https://github.com/voidrot/chorecast/issues/95)
* **web:** upload and remove a kid's photo avatar ([70cff8e](https://github.com/voidrot/chorecast/commit/70cff8e6d50c8440b79be6e8e9892fe7ad259575))


### Bug Fixes

* **android:** decode URI fields in API responses so photo uploads can be ([864525a](https://github.com/voidrot/chorecast/commit/864525a2d45cce0f6d4f5cff48f1d4a5f387a9f4))
* **deploy:** make the default image's /data volume writable by the server ([#129](https://github.com/voidrot/chorecast/issues/129)) ([3cbbfb9](https://github.com/voidrot/chorecast/commit/3cbbfb91292c07171977324c58b8bb6902b8f43a))
* **server:** date ledger reversals to the entry they undo ([#79](https://github.com/voidrot/chorecast/issues/79)) ([98d0378](https://github.com/voidrot/chorecast/commit/98d037886301a2e9f34108845389355f41f8728f)), closes [#26](https://github.com/voidrot/chorecast/issues/26)
* **server:** find JPEG markers past stray bytes like Go's decoder ([#123](https://github.com/voidrot/chorecast/issues/123)) ([2960e01](https://github.com/voidrot/chorecast/commit/2960e01175b300346ba93d53b19a31b86d510358)), closes [#83](https://github.com/voidrot/chorecast/issues/83)
* **server:** limit kids to reading the chores they are on ([6c3f93b](https://github.com/voidrot/chorecast/commit/6c3f93b4cc6039dd048a1a58dabf37721afad90b))
* **server:** read points balance and available in one transaction ([98d0378](https://github.com/voidrot/chorecast/commit/98d037886301a2e9f34108845389355f41f8728f))
* **server:** refuse video evidence on chores until video is supported ([8f7d3c4](https://github.com/voidrot/chorecast/commit/8f7d3c4fdde06efb36f6aa17d6944c0b241aab99))
* **server:** stop keyed POSTs panicking after their handler ran ([#91](https://github.com/voidrot/chorecast/issues/91)) ([e6c96f4](https://github.com/voidrot/chorecast/commit/e6c96f41ece0f9154f011adaf01ee48f1ee7b653))

## [0.2.0](https://github.com/voidrot/chorecast/compare/server-v0.1.0...server-v0.2.0) (2026-11-01)


### Miscellaneous Chores

* **server:** Synchronize server-web versions

## 0.1.0 (2026-11-01)


### Features

* chore assignment modes with a deterministic planner ([#22](https://github.com/voidrot/chorecast/issues/22)) ([#57](https://github.com/voidrot/chorecast/issues/57)) ([5610a16](https://github.com/voidrot/chorecast/commit/5610a16611c991967d12b35de623c494f9d75102))
* chore definitions with location and equipment catalogs ([#20](https://github.com/voidrot/chorecast/issues/20)) ([#55](https://github.com/voidrot/chorecast/issues/55)) ([a590fcb](https://github.com/voidrot/chorecast/commit/a590fcbaa42a3792431120270eed6beb1d3ee933))
* device pairing, profile picker and parent PIN elevation ([#18](https://github.com/voidrot/chorecast/issues/18)) ([#53](https://github.com/voidrot/chorecast/issues/53)) ([abb8eea](https://github.com/voidrot/chorecast/commit/abb8eea27be86256f403d60e51ce016c68430d24))
* devices page with revoke and remote lock ([#19](https://github.com/voidrot/chorecast/issues/19)) ([#54](https://github.com/voidrot/chorecast/issues/54)) ([b74dfe6](https://github.com/voidrot/chorecast/commit/b74dfe67e08763efed9d1ce2f1f6ca9a5f13d7fe))
* first-run setup token and web setup wizard ([#12](https://github.com/voidrot/chorecast/issues/12)) ([#46](https://github.com/voidrot/chorecast/issues/46)) ([201ad32](https://github.com/voidrot/chorecast/commit/201ad32a3dd1cdf402d52c53cd8d8f03d0c9fa06))
* household settings page ([#17](https://github.com/voidrot/chorecast/issues/17)) ([#52](https://github.com/voidrot/chorecast/issues/52)) ([4b39e7e](https://github.com/voidrot/chorecast/commit/4b39e7e08f0ed46c6441b0e5aeebcd573c699ad1))
* kid profiles with avatars, PIN, day end and quiet times ([#16](https://github.com/voidrot/chorecast/issues/16)) ([#51](https://github.com/voidrot/chorecast/issues/51)) ([5861b2b](https://github.com/voidrot/chorecast/commit/5861b2ba354ec0b98544329fb132a2fbc7c186eb))
* optional OIDC single sign-on for parents ([#14](https://github.com/voidrot/chorecast/issues/14)) ([#49](https://github.com/voidrot/chorecast/issues/49)) ([62067fd](https://github.com/voidrot/chorecast/commit/62067fd4be3c883f9fa2978b169b1acb60dde9ed))
* parent invites, owner roles and admin recovery ([#15](https://github.com/voidrot/chorecast/issues/15)) ([#50](https://github.com/voidrot/chorecast/issues/50)) ([e1b2519](https://github.com/voidrot/chorecast/commit/e1b2519542f0ce6cf66f9c639edc2eb79f92f61b))
* parent login, TOTP, recovery codes and sessions ([#13](https://github.com/voidrot/chorecast/issues/13)) ([#48](https://github.com/voidrot/chorecast/issues/48)) ([3574c8f](https://github.com/voidrot/chorecast/commit/3574c8fee0268a2004d66fc86264150d00fe13a4))
* schedule rules with validation and live occurrence preview ([#21](https://github.com/voidrot/chorecast/issues/21)) ([#56](https://github.com/voidrot/chorecast/issues/56)) ([4646004](https://github.com/voidrot/chorecast/commit/4646004314b40079f0702b86dcb2c4d5f0c77d28))
* **server:** add domain event outbox and dispatcher ([#44](https://github.com/voidrot/chorecast/issues/44)) ([6625fb2](https://github.com/voidrot/chorecast/commit/6625fb2e7ae787687869a36122dfbe83f0078be5)), closes [#5](https://github.com/voidrot/chorecast/issues/5)
* **server:** API conventions for errors, pagination, idempotency and kid slugs ([#43](https://github.com/voidrot/chorecast/issues/43)) ([ddd0415](https://github.com/voidrot/chorecast/commit/ddd0415a767c18d451b49d8c10eea4daf11a346f)), closes [#7](https://github.com/voidrot/chorecast/issues/7)
* **server:** auth core with token store and scope-aware middleware ([#41](https://github.com/voidrot/chorecast/issues/41)) ([5c461dc](https://github.com/voidrot/chorecast/commit/5c461dc7db4a9b3f1f11aec966526557d321ba52)), closes [#6](https://github.com/voidrot/chorecast/issues/6)
* **server:** clock abstraction and household calendar helpers ([#40](https://github.com/voidrot/chorecast/issues/40)) ([c58a34f](https://github.com/voidrot/chorecast/commit/c58a34f81079ae2904d54be34db5f8f6fabc7897)), closes [#3](https://github.com/voidrot/chorecast/issues/3)
* **server:** SQLite-backed job runner and scheduler ([#42](https://github.com/voidrot/chorecast/issues/42)) ([ebcd4fb](https://github.com/voidrot/chorecast/commit/ebcd4fb12aabe0c0875b29e435850186109e3e0a)), closes [#4](https://github.com/voidrot/chorecast/issues/4)
