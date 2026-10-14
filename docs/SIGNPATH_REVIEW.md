# Deepzora independent SignPath review materials

[中文](SIGNPATH_REVIEW.zh-CN.md) · [Code signing policy](../CODE_SIGNING_POLICY.md) · [Privacy policy](../PRIVACY.md)

Prepared: 2026-10-14. Source baseline: `a1ccceef537b34e52b572d685eb2a9baefd2cee4`
on `SivanCola/deepzora` main. This packet supports the already submitted independent
preliminary application; it does not constitute approval or a new application.

## Project facts and public review links

| Item | Review information |
| --- | --- |
| Project / license | Deepzora; AGPL-3.0-only for the combined work, upstream MIT preserved, no commercial dual-license in this repository |
| Homepage / source | <https://github.com/SivanCola/deepzora> |
| Download / release page | <https://github.com/SivanCola/deepzora/releases> |
| Maintainer and roles | SivanCola: author, reviewer and proposed signing approver; currently one maintainer |
| Product | AI coding assistant with CLI, Electron desktop UI, local/remote agent and integration interfaces |
| Application status | Independent preliminary application submitted; Foundation review and service provisioning pending |
| Release / adoption evidence | No public GitHub Release at preparation time; no independent usage figures claimed |

The application contact details remain in the private application record, not
this public packet. Upstream stars, downloads or adoption must not be presented
as Deepzora's reputation. A first downloadable release and additional maintenance
evidence may be required before acceptance.

## Provenance and license

[NOTICE](../NOTICE) and both [README](../README.md) versions visibly identify the
DeepSeek-Reasonix origin, initial source commit
`bddb48202a92e4a20b728d1b4485242137ea615d`, preserved MIT copyrights and independent
release identity. GitHub reports this repository as `fork=false`; it was created
by source/history migration, not GitHub's fork feature. Whether this provenance
and visible derivative identification satisfy the Foundation's fork requirements
must be decided by the reviewer. No approval is inferred from the software licenses.

This packet's source baseline predates the license change on 2026-10-14.
The current combined-work license is [AGPL-3.0-only](../LICENSE); the original
upstream MIT text remains in [LICENSE-MIT](../LICENSE-MIT). See
[LICENSING.md](../LICENSING.md) and the [brand policy](../TRADEMARK_POLICY.md).
The changed license must be reflected in review materials before signing;
this file does not update the previously submitted external application.

The derivative-code review also needs evidence of signed upstream releases,
the relationship between Deepzora's release branch and upstream branches that
normally produce signed builds, and review of changes to the inherited codebase.
These conditions remain to be confirmed; visible attribution alone is insufficient.

Before a signed release, provide the exact source commit, dependency/license
inventory and artifact contents for that candidate, including upstream binary
origins and available upstream signatures. These are per-release evidence, not
claims that all bundled dependencies have already been approved.

## Proposed dedicated service configuration

| Setting | Proposed requirement / outstanding work |
| --- | --- |
| Project | A new Deepzora project, proposed slug `deepzora`; never submit through Reasonix's project |
| Service IDs / certificate | Not yet issued or authorized; record actual IDs and restrictions after provisioning |
| Source repository | Only `https://github.com/SivanCola/deepzora.git` |
| Trusted build | GitHub App access to this repository; GitHub-hosted artifact-producing jobs; upload before signing |
| Production policy | Protected `main`, verified source origin, manual approval on every production signing request |
| Test policy | Separate test certificate/policy and visibly non-production artifacts |
| CI identity | New token/submitter restricted to Deepzora and its permitted policies; separate secret from Reasonix |
| Payload configuration | Explicitly approved project-produced launchers, service, CLI and update helper; product name `Deepzora`, candidate version and architecture checked |
| Installer configuration | Build NSIS installer from returned verified signed payload; sign final installer and eligible project-generated uninstaller with approved paths/metadata |
| Third-party binaries | Preserve upstream signatures; exclude unmodified runtime binaries from claims of project-authored code |
| Publication evidence | Commit/run/origin, hashes, signing IDs, manual approvals, signature/timestamp verification and native acceptance in the sealed candidate record |

This is a configuration proposal, not a ready-to-use SignPath integration.
Signed Windows releases use Certum, and no `.signpath` configuration exists.
The maintainer approved a temporary unsigned Windows v0.0.1 release, disclosed
in the signing policy and release notes. It retains Minisign integrity and native
acceptance; it is not a Foundation-signed build.
Its recursive all-PE signing list must not be reused unchanged for Foundation
signing: Electron and other upstream files need separate treatment. After approval,
implement dedicated artifact configurations and a two-stage payload/installer
workflow while preserving the existing candidate and publication gates.

## Implementation evidence for policy statements

| Area | Source evidence |
| --- | --- |
| Protected build/publication and native acceptance | [.github/workflows/release-desktop.yml](../.github/workflows/release-desktop.yml), [release candidates](RELEASE_CANDIDATES.md) |
| Product identity and Windows artifact layout | [packaging/lib.mjs](../desktop/packaging/lib.mjs), [desktop-build.sh](../scripts/desktop-build.sh) |
| Existing PE enumeration and Certum signing | [signing-files.mjs](../desktop/packaging/signing-files.mjs), [sign-certum.ps1](../scripts/sign-certum.ps1) |
| Local paths and settings defaults | [config/paths.go](../internal/config/paths.go), [config/config.go](../internal/config/config.go) |
| Update destination and controls | [updater.go](../desktop/updater.go), [desktop_preferences.go](../internal/config/desktop_preferences.go) |
| Optional desktop collectors and queued reports | [telemetry_app.go](../desktop/telemetry_app.go), [metrics_app.go](../desktop/metrics_app.go), [crash_app.go](../desktop/crash_app.go), [crash_pending.go](../desktop/crash_pending.go) |
| CLI consent, opt-out and collector | [cli.go](../internal/cli/cli.go), [telemetry/policy.go](../internal/telemetry/policy.go), [telemetry/client.go](../internal/telemetry/client.go) |
| User-reviewed CLI crash reports / support archives | [crashreport/cli.go](../internal/crashreport/cli.go), [Guide](GUIDE.md) |

The source has configurable diagnostic endpoints but no built-in collector URLs.
Desktop update checks default to enabled. Model requests and integrations can
transmit task context to configured recipients. The bilingual privacy policy
describes these flows, persistent installation IDs, local plaintext credential
storage, unredacted support archives, and separate CLI/desktop controls; it makes
no blanket promise that the application never connects or uploads data.

## Evidence and activation still required

- Merge these policy files so canonical `main` links are public. README and the
  release-note renderer link the policies; actual release/download pages still
  require a published release. Add the Foundation sponsorship attribution only
  after approval and actual use.
- Supply the first downloadable release and independent maintenance/reputation
  evidence requested by the reviewer. An unreleased project's acceptance remains
  the Foundation's decision.
- Resolve the derivative-project and `fork=false` provenance question with the
  reviewer before signing modified upstream code.
- Verify MFA, least-privilege membership and documented contribution review/manual
  signing approval. Current branch protection requires PR/checks but zero approving
  reviews; it does not supply independent reviewer evidence.
- Obtain dedicated service-side IDs, certificate restrictions and approved artifact
  configurations, integrate CI, then verify returned signatures and native behavior.
- If a collector is enabled for an official build, identify its operator/retention
  and update privacy/install disclosures before distribution.

Review references: [Foundation terms](https://signpath.org/terms),
[application](https://signpath.org/apply),
[trusted GitHub builds](https://docs.signpath.io/trusted-build-systems/github),
[project and signing policies](https://docs.signpath.io/projects).
