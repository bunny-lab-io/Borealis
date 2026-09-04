# Publishing Engine Releases
Borealis maintainers use this runbook to publish qualification builds for cluster testing, then promote tested source as stable Engine release. Every published release is immutable: Git tag and uploaded assets become permanent release identity.

## Choose Release Channel

| Channel | Tag format | GitHub status | Intended use | Engine installer assets |
| --- | --- | --- | --- | --- |
| Stable | `YYYY.MM.REVISION` or `YYYY.MM.REVISION.HOTFIX` | Normal release | Supported standalone installs and cluster updates | Required |
| Qualification | `YYYY.MM.REVISION-rc.N` or `YYYY.MM.REVISION.HOTFIX-rc.N` | Pre-release | Unsupported cluster testing before stable publication | Not required |
| Development | `dev-<first-12-commit-characters>` | Not a GitHub release | Initial cluster baseline and HMR restoration | Not applicable |

`REVISION` counts normal releases published during calendar month. `HOTFIX` counts focused corrections based on one normal release. `N` starts at `1` and increases for each qualification candidate built for same intended stable version. These values express publication order, not semantic major, minor, or patch scope. See [Security Policy](https://github.com/bunny-lab-io/Borealis/blob/main/SECURITY.md).

!!! tip "Normal development path"
    Merge reviewed changes to `main`, publish `-rc.1` from resulting commit, and test it through Cluster Management. Fix failures through another reviewed commit and publish `-rc.2`. When candidate passes, create stable tag from same tested commit.

## Understand Immutability

Repository immutable releases setting applies when draft becomes published release. Publication locks:

- Associated Git tag to exact commit.
- Every uploaded release asset against replacement or deletion.
- Release identity through GitHub-generated release attestation.

GitHub still permits edits to release title, release notes, latest designation, and pre-release designation. Borealis requires tag shape and GitHub pre-release status to agree, so do not turn `*-rc.N` into normal release or stable dotted tag into pre-release.

Setting is not retroactive. Releases published before enablement remain mutable and cannot satisfy stable `Install-Engine.sh` immutable-release check. New version must be published for verified curl deployment.

GitHub documents locked fields, generated attestation, and draft-first publication under [Immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases).

!!! danger "Published version cannot be repaired in place"
    After publication, do not move tag, replace asset, or delete and recreate same release. GitHub prevents tag-name reuse even after immutable release is deleted. Publish next `-rc.N`, monthly revision, or hotfix instead.

Confirm repository protection before creating release:

```sh
REPOSITORY="bunny-lab-io/Borealis"
gh api "repos/${REPOSITORY}/immutable-releases" --jq '.enabled'
```

Expected output is `true`.

## Prepare Source

Release only reviewed commit reachable from `main`. Confirm clean checkout, current source SHA, release manifest, and successful required checks before tagging.

```sh
REPOSITORY="bunny-lab-io/Borealis"
git switch main
git pull --ff-only
test -z "$(git status --porcelain)"
SOURCE_SHA="$(git rev-parse HEAD)"
printf '%s\n' "${SOURCE_SHA}"
git show HEAD:Data/Engine/release-manifest.json
```

`Data/Engine/release-manifest.json` must declare `cluster_compatible: true` and include intended channel under `allowed_release_channels`. Review minimum rolling version, version-skew window, database migration phase, K3s baseline, and required probe conformance for every release.

## Publish Qualification Candidate

Qualification release exercises same immutable Git source consumed by cluster rolling update. It remains unsupported and never becomes stable by changing GitHub pre-release checkbox.

```sh
REPOSITORY="bunny-lab-io/Borealis"
RELEASE="YYYY.MM.REVISION-rc.1"
SOURCE_SHA="$(git rev-parse HEAD)"

git tag -a "${RELEASE}" "${SOURCE_SHA}" -m "Borealis ${RELEASE}"
git push origin "refs/tags/${RELEASE}"

# Draft first. Draft remains unpublished while metadata is inspected.
gh release create "${RELEASE}" --repo "${REPOSITORY}" \
  --verify-tag --draft --prerelease \
  --title "${RELEASE}" --generate-notes
gh release view "${RELEASE}" --repo "${REPOSITORY}" \
  --json tagName,isDraft,isPrerelease,targetCommitish,url

# Publication locks tag. Do this only after draft review.
gh release edit "${RELEASE}" --repo "${REPOSITORY}" \
  --draft=false --prerelease
gh api "repos/${REPOSITORY}/releases/tags/${RELEASE}" \
  --jq '{tag_name,draft,prerelease,immutable}'
```

Expected final values are exact tag, `draft: false`, `prerelease: true`, and `immutable: true`. Cluster Management then lists candidate only when it is same or newer than pinned baseline, descends from pinned commit, and passes release-manifest compatibility checks.

Deploy through **Admin > Cluster Management > Updates > Qualification Engine Version**. Qualification action updates whole cluster one node at time, requires `DEPLOY QUALIFICATION`, preserves unsupported warning, and defers contract-phase schema finalization until stable promotion.

If candidate fails, merge correction and publish next candidate number from corrected commit. Never retag failed candidate.

## Publish Stable Release

Stable release adds verified curl installer bundle. Packaging workflow must exist on default branch because GitHub accepts manual `workflow_dispatch` only from workflow present there.

```sh
REPOSITORY="bunny-lab-io/Borealis"
RELEASE="YYYY.MM.REVISION"
SOURCE_SHA="<full tested qualification commit SHA>"

git fetch origin main --tags
git cat-file -e "${SOURCE_SHA}^{commit}"
git merge-base --is-ancestor "${SOURCE_SHA}" origin/main
git tag -a "${RELEASE}" "${SOURCE_SHA}" -m "Borealis ${RELEASE}"
git push origin "refs/tags/${RELEASE}"

gh release create "${RELEASE}" --repo "${REPOSITORY}" \
  --verify-tag --draft \
  --title "${RELEASE}" --generate-notes

# Workflow checks setting, draft status, stable tag, and exact checkout.
gh workflow run publish-engine-release-assets.yml \
  --repo "${REPOSITORY}" --ref main -f "release=${RELEASE}"
gh run list --repo "${REPOSITORY}" \
  --workflow publish-engine-release-assets.yml \
  --event workflow_dispatch --limit 5
```

Wait for matching workflow run to pass. Draft must contain exactly these generated assets:

- `Install-Engine.sh`
- `Engine.sh`
- `borealis-engine-install-manifest.json`
- `SHA256SUMS`

Inspect GitHub digests, download assets, verify checksums, and confirm manifest binds repository, release name, source SHA, platform list, asset URLs, sizes, and hashes.

```sh
gh api "repos/${REPOSITORY}/releases/tags/${RELEASE}" \
  --jq '.assets[] | [.name, .size, .digest] | @tsv'

RELEASE_ASSETS="$(mktemp -d)"
gh release download "${RELEASE}" --repo "${REPOSITORY}" \
  --dir "${RELEASE_ASSETS}"
(cd "${RELEASE_ASSETS}" && sha256sum --check SHA256SUMS)
python3 -m json.tool \
  "${RELEASE_ASSETS}/borealis-engine-install-manifest.json"
```

Publish only after workflow and asset inspection pass:

```sh
gh release edit "${RELEASE}" --repo "${REPOSITORY}" \
  --draft=false --latest
gh api "repos/${REPOSITORY}/releases/tags/${RELEASE}" \
  --jq '{tag_name,draft,prerelease,immutable,assets:[.assets[]|{name,size,digest}]}'
```

Expected final values are exact tag, `draft: false`, `prerelease: false`, `immutable: true`, and four assets with `sha256:` digests. Stable `Install-Engine.sh` rejects release when any identity, immutability, digest, manifest, platform, size, URL, or tag-to-commit check fails.

After publication, validate exact release on fresh host using [Deploying Engine](deploying-the-engine.md). Promote qualification cluster through stable whole-cluster action even when stable and qualification tags resolve to same commit; promotion records supported channel and completes pending schema contract phase.

## Correct Release Failure

Before publication, draft and its assets remain editable. Fix packaging workflow or discard incorrect draft before publishing. Never publish partially packaged stable release.

After publication:

- Failed qualification candidate: publish next `-rc.N` from corrected descendant commit.
- Focused stable correction: publish next `.HOTFIX` from corrected descendant commit.
- Normal release correction: publish next monthly revision.
- Incorrect release notes: edit notes or title without changing tag, assets, or channel.

Do not use deletion as version rollback. Clusters reject older or unrelated targets and stable installer requires exact named release.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Security Policy](https://github.com/bunny-lab-io/Borealis/blob/main/SECURITY.md)
    - [Deploying Engine](deploying-the-engine.md)
    - [Updating Engine](updating-the-engine.md)
    - [Managing Engine Clusters](managing-engine-clusters.md)
    - [Security Whitepaper](../Reference/security-whitepaper.md)
    - [Testing and Regression History](../Reference/testing-regressions.md)

    ### Source map

    - `.github/workflows/publish-engine-release-assets.yml` packages stable draft only and refuses mutable-release repository setting, non-stable tag, published release, or GitHub pre-release.
    - `Tests/tools/build_engine_release_assets.py` copies exact tag's `Install-Engine.sh` and `Engine.sh`, writes installer manifest, and generates `SHA256SUMS`.
    - `Install-Engine.sh` accepts only published, non-prerelease, immutable stable release and validates GitHub plus manifest identities before invoking `Engine.sh`.
    - `Data/Engine/release-manifest.json` controls cluster release compatibility independently from standalone installer manifest.
    - `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go` classifies stable versus qualification tags, checks GitHub release metadata, resolves exact SHA, verifies ancestry, and evaluates cluster manifest.

    ### Immutable boundary

    GitHub locks tag and uploaded assets only when draft is published. Title, notes, latest flag, and pre-release flag remain editable at GitHub layer. Borealis adds stricter channel identity: stable tag must remain normal release and `-rc.N` tag must remain pre-release. Existing releases published before repository setting was enabled remain mutable because setting is prospective.

    GitHub immutable releases generate release attestation covering tag, commit SHA, and release assets. Borealis stable bootstrap also verifies GitHub asset digest metadata and deterministic installer manifest so runtime fails closed without depending on operator's local release inspection.
