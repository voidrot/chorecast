# Changelog

## [0.3.0](https://github.com/voidrot/chorecast/compare/android-v0.2.0...android-v0.3.0) (2026-11-14)


### Features

* **android:** add a settings screen with a System, Light or Dark theme choice ([#149](https://github.com/voidrot/chorecast/issues/149)) ([8e38640](https://github.com/voidrot/chorecast/commit/8e38640b69590c0bf41be7bd90d6e0ca4fd3ca6c))
* **android:** move Devices, Change server and Sign out from Home into Settings ([#150](https://github.com/voidrot/chorecast/issues/150)) ([15cec8c](https://github.com/voidrot/chorecast/commit/15cec8cdf15ef4bf61eb322d40ff1e97de12d5cb))
* **android:** use Chorecast's emerald colors when wallpaper colors are off ([#151](https://github.com/voidrot/chorecast/issues/151)) ([70f9f1a](https://github.com/voidrot/chorecast/commit/70f9f1a13659aee86198ee3687f160aaba503735))

## [0.2.0](https://github.com/voidrot/chorecast/compare/android-v0.1.0...android-v0.2.0) (2026-11-06)


### Features

* **android:** add an in-app inbox for kids and parents ([#136](https://github.com/voidrot/chorecast/issues/136)) ([429c210](https://github.com/voidrot/chorecast/commit/429c210030c3c2838b1db352843b59db25b6129f))
* **android:** add parent mode, a PIN pad and avatars to the shared tablet ([#137](https://github.com/voidrot/chorecast/issues/137)) ([1e7ec78](https://github.com/voidrot/chorecast/commit/1e7ec78708b5ea97190f4f995f1b495ab2ab913c)), closes [#33](https://github.com/voidrot/chorecast/issues/33)
* **android:** complete a chore with CameraX photo evidence ([#115](https://github.com/voidrot/chorecast/issues/115)) ([864525a](https://github.com/voidrot/chorecast/commit/864525a2d45cce0f6d4f5cff48f1d4a5f387a9f4)), closes [#101](https://github.com/voidrot/chorecast/issues/101)
* **android:** free uploaded photos when a kid removes them or drops a draft ([#141](https://github.com/voidrot/chorecast/issues/141)) ([fe1dad1](https://github.com/voidrot/chorecast/commit/fe1dad135c603bb410b1160730dee3a366cf65b6)), closes [#118](https://github.com/voidrot/chorecast/issues/118)
* **android:** let parents adjust points, excuse, reassign and reverse approvals ([#132](https://github.com/voidrot/chorecast/issues/132)) ([07fe517](https://github.com/voidrot/chorecast/commit/07fe5176988d27aa5cf204e1073e590673732016)), closes [#104](https://github.com/voidrot/chorecast/issues/104)
* **android:** let parents pair a device by showing a QR code ([#140](https://github.com/voidrot/chorecast/issues/140)) ([a41522f](https://github.com/voidrot/chorecast/commit/a41522f87648abba8c938069589b1b65134bca63)), closes [#133](https://github.com/voidrot/chorecast/issues/133)
* **android:** show a kid's chores for today with points and streak ([#107](https://github.com/voidrot/chorecast/issues/107)) ([6923732](https://github.com/voidrot/chorecast/commit/6923732279fc12df6f3a5f19240361c506ddd5e5)), closes [#100](https://github.com/voidrot/chorecast/issues/100)
* **android:** show a kid's past chores with their evidence ([#126](https://github.com/voidrot/chorecast/issues/126)) ([36a1a28](https://github.com/voidrot/chorecast/commit/36a1a28e185c224a4fa0ec08689d6acf29a1f1a0)), closes [#102](https://github.com/voidrot/chorecast/issues/102)
* **android:** show each kid's day and review chores from the parent home ([#120](https://github.com/voidrot/chorecast/issues/120)) ([763e9d9](https://github.com/voidrot/chorecast/commit/763e9d90d568854dbdb4f727d91bc782226eb556)), closes [#103](https://github.com/voidrot/chorecast/issues/103)
* **android:** sync in the background and remind kids before chores are due ([#139](https://github.com/voidrot/chorecast/issues/139)) ([23e9f99](https://github.com/voidrot/chorecast/commit/23e9f997094102091fd696296436b0b9a9f256c3))


### Bug Fixes

* **android:** decode unknown enum values instead of throwing ([#127](https://github.com/voidrot/chorecast/issues/127)) ([4b29a63](https://github.com/voidrot/chorecast/commit/4b29a63e6b6105b632fb72e9dfef1121dffbcd19)), closes [#119](https://github.com/voidrot/chorecast/issues/119)
* **android:** decode URI fields in API responses so photo uploads can be ([864525a](https://github.com/voidrot/chorecast/commit/864525a2d45cce0f6d4f5cff48f1d4a5f387a9f4))

## 0.1.0 (2026-11-01)


### Features

* chore assignment modes with a deterministic planner ([#22](https://github.com/voidrot/chorecast/issues/22)) ([#57](https://github.com/voidrot/chorecast/issues/57)) ([5610a16](https://github.com/voidrot/chorecast/commit/5610a16611c991967d12b35de623c494f9d75102))
* device pairing, profile picker and parent PIN elevation ([#18](https://github.com/voidrot/chorecast/issues/18)) ([#53](https://github.com/voidrot/chorecast/issues/53)) ([abb8eea](https://github.com/voidrot/chorecast/commit/abb8eea27be86256f403d60e51ce016c68430d24))
* devices page with revoke and remote lock ([#19](https://github.com/voidrot/chorecast/issues/19)) ([#54](https://github.com/voidrot/chorecast/issues/54)) ([b74dfe6](https://github.com/voidrot/chorecast/commit/b74dfe67e08763efed9d1ce2f1f6ca9a5f13d7fe))
* household settings page ([#17](https://github.com/voidrot/chorecast/issues/17)) ([#52](https://github.com/voidrot/chorecast/issues/52)) ([4b39e7e](https://github.com/voidrot/chorecast/commit/4b39e7e08f0ed46c6441b0e5aeebcd573c699ad1))
* optional OIDC single sign-on for parents ([#14](https://github.com/voidrot/chorecast/issues/14)) ([#49](https://github.com/voidrot/chorecast/issues/49)) ([62067fd](https://github.com/voidrot/chorecast/commit/62067fd4be3c883f9fa2978b169b1acb60dde9ed))
* parent invites, owner roles and admin recovery ([#15](https://github.com/voidrot/chorecast/issues/15)) ([#50](https://github.com/voidrot/chorecast/issues/50)) ([e1b2519](https://github.com/voidrot/chorecast/commit/e1b2519542f0ce6cf66f9c639edc2eb79f92f61b))
* parent login, TOTP, recovery codes and sessions ([#13](https://github.com/voidrot/chorecast/issues/13)) ([#48](https://github.com/voidrot/chorecast/issues/48)) ([3574c8f](https://github.com/voidrot/chorecast/commit/3574c8fee0268a2004d66fc86264150d00fe13a4))
