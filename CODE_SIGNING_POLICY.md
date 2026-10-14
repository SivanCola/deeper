# Code signing policy

[中文](CODE_SIGNING_POLICY.zh-CN.md) · [Privacy policy](PRIVACY.md) · [SignPath review materials](docs/SIGNPATH_REVIEW.md)

Last updated: 2026-10-14.

## Project and authorization

Deepzora is an independently maintained MIT-licensed AI coding assistant at
<https://github.com/SivanCola/deepzora>. Its CLI, desktop application and release
channels belong to this project. It is derived from DeepSeek-Reasonix; upstream
copyrights and provenance are retained in [NOTICE](NOTICE). A Deepzora signature
does not designate an official Reasonix release.

An independent SignPath Foundation application has been submitted and is pending
review. Submission is not signing authorization. Deepzora has no provisioned
SignPath project or integrated SignPath release workflow at this policy's review
date. Signed Windows releases use Certum and require their own valid
credentials. Reasonix's SignPath project, token and authorization must not be
used for Deepzora.

The maintainer explicitly approved Windows v0.0.1 as a temporary unsigned release.
It carries no Deepzora Authenticode publisher signature and must be labeled as
such on download/release pages. Payload/download integrity signatures, provenance
and native acceptance remain required. This exception does not apply to later
versions or to macOS signing and notarization.

Only after approval and actual use of the service will the relevant release and
download pages carry this attribution: “Free code signing provided by SignPath.io,
certificate by SignPath Foundation.” A signature identifies its publisher and
protects signed bytes; it does not guarantee that software is defect-free.

## Roles and review

| Role | Responsibility | Current maintainer |
| --- | --- | --- |
| Committer / author | Prepare source changes and candidate builds | [SivanCola](https://github.com/SivanCola) |
| Reviewer | Review contributions, dependencies, provenance and release contents | SivanCola |
| Signing approver | Manually approve each eligible production signing request | SivanCola |

One maintainer currently performs these duties. This is not a claim of independent
two-person review. External contributions must be reviewed before release.
GitHub and SignPath accounts participating in signing must have multi-factor
authentication enabled; membership, permissions and MFA must be checked before
activating the service. New maintainers and signing approvers must be documented
here before receiving production access.

## Signing and publication

1. Obtain authorization for a dedicated Deepzora project, certificate, artifact
   configurations and signing policies. Use a CI submitter scoped to this project.
2. Build from a recorded commit on protected `main`, after required checks and
   contribution review. GitHub's current zero-required-reviewer setting does not
   itself prove human review. Record the review and release approval separately.
3. Use GitHub-hosted runners for all artifact-producing jobs, upload artifacts to
   GitHub Actions, and submit them with verified repository, workflow, commit and
   branch provenance. Require manual approval of every production signing request.
4. Sign only approved Deepzora artifacts whose product name, version and build
   source match the candidate. Eligible project code includes the launchers,
   service, CLI and update helper, and the final Windows installer and eligible
   project-generated uninstaller. Configure the exact artifact paths and product
   metadata with SignPath before first use.
5. Preserve upstream signatures on unmodified third-party binaries, including
   Electron runtime files. Do not sign those binaries as Deepzora's own code.
   Unsigned dependencies may be bundled in the installer subject to the approved
   artifact configuration. Modified upstream code requires provenance, licensing
   and Foundation review; it is not automatically authorized by this policy.
6. Package the returned, verified signed payload bytes into the final installer,
   then sign the installer in a separate request. Verify signatures, timestamps,
   hashes and native installation/update behavior before publication. Never
   replace a verified payload with a rebuilt copy.
7. Retain source SHA, workflow run, artifact digests, signing request IDs, approval
   evidence, verification results and the sealed release candidate record. Publish
   only the approved candidate through the [release process](docs/RELEASE_CANDIDATES.md).

Failures in authorization, source verification, signing or acceptance block
publication as a signed release. Test certificates and development builds must
be clearly distinguished from production releases. Current recursive Certum PE
signing is not an approved Foundation artifact configuration.

## Privacy and incidents

The [Privacy policy](PRIVACY.md) explains local storage, model providers, update
checks and optional diagnostics. Material changes to data collection require an
updated policy and applicable installation disclosure before distributing the
affected release.

Report suspected credential compromise or unauthorized signing through
[SECURITY.md](SECURITY.md). Suspend affected signing and publication while
investigating; rotate credentials or revoke certificates when warranted, and
document affected versions and replacement releases. Do not post credentials in
issues or build logs.

This policy is maintained against the [Foundation terms](https://signpath.org/terms),
[GitHub origin requirements](https://docs.signpath.io/trusted-build-systems/github)
and [SignPath project policies](https://docs.signpath.io/projects). Approval and
service-side restrictions remain subject to SignPath's review.
