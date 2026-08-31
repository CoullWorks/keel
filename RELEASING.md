# Releasing keel

keel ships a single static binary. A release builds it for six OS/arch targets and
publishes them as the assets `install.sh` and `keel selfupdate` fetch. The version
string is baked into the binary at build time.

## The pipeline

```
conventional commits on main
  release-please.yml  -> opens/updates a "release PR" (bumps version + CHANGELOG.md)
    merge the release PR -> release-please tags vX.Y.Z
      release.yml (on tag v*) -> GoReleaser (.goreleaser.yaml)
        builds keel_{os}_{arch} (+ per-asset .sha256) for
          linux/darwin/windows x amd64/arm64
        creates the GitHub Release with the auto changelog
```

Then `install.sh` (`curl | sh`) and `keel selfupdate` download the raw
`keel_<os>_<arch>` asset plus its `.sha256`, not a tarball, and they refuse to
install without the per-asset checksum. Don't change the asset naming or the
split-checksum config in `.goreleaser.yaml` without updating `internal/selfupdate`.

## The four workflows

| Workflow | Trigger | Does |
|---|---|---|
| `ci.yml` | push / PR | unit + BDD + dry e2e, studio dist build. The gate on every PR. |
| `verify.yml` (verify-stacks) | weekly cron + manual dispatch | boots every recipe stack for real (native/ddev/full tiers). Not a PR gate; dispatch it after recipe changes. |
| `release-please.yml` | push to main + manual dispatch | maintains the release PR from conventional commits. |
| `release.yml` | tag `v*` + manual dispatch | GoReleaser build + GitHub Release. |

## Cutting a release

Normal path: merge conventional-commit PRs, then merge the release PR that
release-please opens. The tag and the GitHub Release happen automatically.

Manual fallback (when release-please can't tag, see gotchas):

```sh
git checkout main && git pull
git tag -a v1.2.0 -m "v1.2.0"      # annotated
git push origin v1.2.0             # fires release.yml -> GoReleaser
# or: gh workflow run release.yml --ref main
```

## Descriptive release notes (the standard)

GoReleaser's `changelog: use: github` produces a raw list of commit subjects. That
tells a reader what commits landed, not what the release gives them or why it
matters. Every keel release must lead with a short, human summary.

The release body is:

1. a 2 to 4 sentence "What's in it" intro in plain language: the change a user
   actually feels, and why it's worth updating for; then
2. Highlights: 3 to 6 bullets grouped by theme (New / Fixed / Changed), each a full
   sentence a non-contributor understands; then
3. the auto-generated commit changelog below (keep it; it's the audit trail).

Voice: same as the rest of keel's copy. Plain, specific, no typographic dashes (the
`TestRecipeUserFacingTextUsesNoTypographicDashes` guard and house style both forbid
them), no marketing. Say what broke and what now works.

### Template

```md
keel <version> <one-line theme>.

<2 to 4 sentences: what changed for the user and why they'd want it. Name the
stacks/commands affected. If a bug bit people, say what the symptom was.>

## New
- <feature, in a sentence a user understands>

## Fixed
- <bug: the symptom, then the fix>

## Changed
- <behaviour change + any migration note>

---
<!-- auto changelog below -->
```

### How to apply the notes

GoReleaser creates the release with the raw changelog first, so curate the notes
after the release exists:

```sh
gh release view v1.1.0 --json body -q .body > /tmp/rel.md   # grab the auto changelog
$EDITOR /tmp/rel.md                                          # prepend intro + Highlights
gh release edit v1.1.0 --notes-file /tmp/rel.md
```

The same `gh release edit` backfills real summaries onto past releases.

## Gotchas

- **release-please can't open PRs until the org allows it. This is an org-level
  setting and it overrides the repo.** The workflow already grants
  `pull-requests: write`, so the block is purely the org toggle. Fix, as an org
  owner: the org's Settings, Actions, "Workflow permissions", tick "Allow GitHub
  Actions to create and approve pull requests", Save. Setting it on the repo first
  returns `409: The organization does not allow GitHub Actions to create or approve
  pull requests`. Once the org allows it, confirm the repo with
  `gh api -X PUT /repos/coullworks/keel/actions/permissions/workflow -F can_approve_pull_request_reviews=true`,
  then run release-please: `gh workflow run release-please.yml --ref main`.
- **Assets are bare binaries, not tarballs**, with one `.sha256` per asset.
  `install.sh` and `selfupdate` depend on both. Don't switch to `checksums.txt`.
- **Force-moving a tag** to fix a release re-triggers `release.yml`; GoReleaser
  `--clean` recreates the release. Prefer a new patch version over re-tagging.
- **Recipe changes:** CI does not boot stacks. After changing recipes, dispatch
  `verify.yml` (`gh workflow run verify.yml --ref main`) before releasing, so a
  broken recipe doesn't ship.
