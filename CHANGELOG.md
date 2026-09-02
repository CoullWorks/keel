# Changelog

## [1.2.4](https://github.com/CoullWorks/keel/compare/v1.2.3...v1.2.4) (2026-09-02)


### Bug Fixes

* **security:** command-injection barrier + scan scoping + nit ([#35](https://github.com/CoullWorks/keel/issues/35)) ([dc04639](https://github.com/CoullWorks/keel/commit/dc04639e24008625d2910afaf7ccfe522e9628d2)), closes [#31](https://github.com/CoullWorks/keel/issues/31)
* **security:** finish sql-injection + flask empty-password ([#34](https://github.com/CoullWorks/keel/issues/34)) ([de91e08](https://github.com/CoullWorks/keel/commit/de91e0883fcf848e2689591f66393a05f10c29fd)), closes [#31](https://github.com/CoullWorks/keel/issues/31)
* **security:** sql-injection barrier + nits ([#32](https://github.com/CoullWorks/keel/issues/32)) ([7c49183](https://github.com/CoullWorks/keel/commit/7c49183df56a2d411fc04171b1755e09f079f5ac)), closes [#31](https://github.com/CoullWorks/keel/issues/31)

## [1.2.3](https://github.com/CoullWorks/keel/compare/v1.2.2...v1.2.3) (2026-09-02)


### Bug Fixes

* **security:** confine engine writes to the project dir (HasPrefix) ([#25](https://github.com/CoullWorks/keel/issues/25)) ([7208de2](https://github.com/CoullWorks/keel/commit/7208de20bde1cea864306c9f24952cfa99675b26)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** confine path sinks repo-wide + pin actions + Close handling ([#29](https://github.com/CoullWorks/keel/issues/29)) ([5ea216b](https://github.com/CoullWorks/keel/commit/5ea216b8f34c3312b9db8a02db107ca24cd47ea7)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** confine recipe file paths under the project directory ([#22](https://github.com/CoullWorks/keel/issues/22)) ([8c73c1f](https://github.com/CoullWorks/keel/commit/8c73c1f372c0301e8784164d35db70422b920cab)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** confine the remaining engine path sinks ([#27](https://github.com/CoullWorks/keel/issues/27)) ([00b9b67](https://github.com/CoullWorks/keel/commit/00b9b67e4227dc01b02424b276840774c1799c13)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** confine the remaining engine path sinks ([#28](https://github.com/CoullWorks/keel/issues/28)) ([4336aea](https://github.com/CoullWorks/keel/commit/4336aeaced17c1c127bccabba7b1fe74bed65c02)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** confine the residual path sinks ([#30](https://github.com/CoullWorks/keel/issues/30)) ([1f24ac2](https://github.com/CoullWorks/keel/commit/1f24ac26116079447f474438b6b5cdf6a67723e2)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** engine path confinement via official Abs+HasPrefix ([#26](https://github.com/CoullWorks/keel/issues/26)) ([874b169](https://github.com/CoullWorks/keel/commit/874b169cbd85cd27a5ab203d1f59e81504959bf0)), closes [#21](https://github.com/CoullWorks/keel/issues/21)
* **security:** inline IsLocal guards at engine path sinks ([#24](https://github.com/CoullWorks/keel/issues/24)) ([4627468](https://github.com/CoullWorks/keel/commit/46274688512e5c1191567138262c0034c542d888)), closes [#21](https://github.com/CoullWorks/keel/issues/21)

## [1.2.2](https://github.com/CoullWorks/keel/compare/v1.2.1...v1.2.2) (2026-09-01)


### Bug Fixes

* hermetic coverage tests + Dependabot security bumps ([#16](https://github.com/CoullWorks/keel/issues/16)) ([d1e8c38](https://github.com/CoullWorks/keel/commit/d1e8c3850b7b965f6cafe63811c9b6740a0dc236)), closes [#15](https://github.com/CoullWorks/keel/issues/15)
* **recipes:** opt-in Playwright E2E addons for the other frameworks ([#14](https://github.com/CoullWorks/keel/issues/14)) ([13d4626](https://github.com/CoullWorks/keel/commit/13d4626dccf4dfe4e7991d36b9120487fefa188f))
* **security:** CodeQL + govulncheck scanning, clear all CVEs, issue forms ([#19](https://github.com/CoullWorks/keel/issues/19)) ([bfde9e1](https://github.com/CoullWorks/keel/commit/bfde9e160e213ea33268cfc34c6fe049a5de7b25)), closes [#18](https://github.com/CoullWorks/keel/issues/18)

## [1.2.1](https://github.com/CoullWorks/keel/compare/v1.2.0...v1.2.1) (2026-09-01)


### Bug Fixes

* **recipes:** add Python AI, agent and data-science addons ([#12](https://github.com/CoullWorks/keel/issues/12)) ([d278cc7](https://github.com/CoullWorks/keel/commit/d278cc7ba727e1e90bd51134d02730cf1c4d7cc0))

## [1.2.0](https://github.com/CoullWorks/keel/compare/v1.1.0...v1.2.0) (2026-08-31)


### Features

* **laravel:** add Playwright E2E addon ([#10](https://github.com/CoullWorks/keel/issues/10)) ([01fb9aa](https://github.com/CoullWorks/keel/commit/01fb9aa052117b5da409866c5ca7b3b4803aff8e))

## [1.1.0](https://github.com/CoullWorks/keel/compare/v1.0.2...v1.1.0) (2026-08-31)


### Features

* Mailpit + Ollama services, release guide, README demo GIFs ([#7](https://github.com/CoullWorks/keel/issues/7)) ([4d4a92f](https://github.com/CoullWorks/keel/commit/4d4a92fb605c2a7f9f7b94e2828d9175d8a75905))


### Bug Fixes

* **verify:** green the verify-stacks matrix ([#6](https://github.com/CoullWorks/keel/issues/6)) ([30d4dbd](https://github.com/CoullWorks/keel/commit/30d4dbdb40dcdd3d464f5ebcf61a976388567258))
