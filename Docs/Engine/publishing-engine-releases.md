# Publishing Engine Releases
Borealis maintainers use this runbook to publish qualification builds for cluster testing, then promote tested source as stable Engine release. Every published release is immutable: Git tag and uploaded assets become permanent release identity.

## Choose Release Channel

| Channel | Tag format | GitHub status | Intended use | Engine installer assets |
| --- | --- | --- | --- | --- |
| Stable | `YYYY.MM.REVISION` or `YYYY.MM.REVISION.HOTFIX` | Normal release | Supported standalone installs and cluster updates | Required |
| Qualification | `YYYY.MM.REVISION-rc.N` or `YYYY.MM.REVISION.HOTFIX-rc.N` | Pre-release | Unsupported cluster testing before stable publication | No standalone installer; node bootstrap and production images required for SSH onboarding |
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

Setting is not retroactive. Releases published before enablement remain mutable and cannot satisfy stable `Install-Engine.sh` or Cluster Management immutable-release checks. Publish a new version for verified installation or cluster update.

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

Stable releases require a reviewed commit reachable from `main`. Qualification releases normally use the same source path. Confirm clean checkout, current source SHA, release manifest, and successful required checks before tagging.

??? note "Qualification before merge"
    When an issue requires live acceptance before merge, an operator may approve an immutable qualification release from that issue's unmerged PR. Record approval, the PR, exact full commit SHA, successful required checks, completed review and live acceptance criteria in the tracking issue before creating the draft. Verify the candidate descends from the cluster baseline. Use a separate implementation checkout; do not move the deployed checkout to the PR branch.

    This exception permits lab qualification only. Keep the PR open until its live acceptance passes. Tag and assets remain immutable, rollout still uses Cluster Management, and stable publication still requires `main` ancestry. A source change requires checks and review appropriate to that change plus a new candidate tag. Never borrow validation from another commit or merge early to make a release eligible.

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

Qualify the exact K3s baseline declared by that source commit. Fresh installs currently default to `v1.36.3+k3s1`; earlier lab evidence on `v1.36.4+k3s1` does not qualify a release declaring `v1.36.3+k3s1`. An upgraded cluster requires a manifest matching its verified current version. Engine redeploy preserves installed K3s and never downgrades it to the fresh-install default.

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

# Package source, helper and production images before publication.
gh workflow run publish-engine-release-assets.yml \
  --repo "${REPOSITORY}" --ref "${RELEASE}" -f "release=${RELEASE}"
gh run list --repo "${REPOSITORY}" \
  --workflow publish-engine-release-assets.yml \
  --event workflow_dispatch --branch "${RELEASE}" --limit 5

# Publication locks tag. Do this only after draft review.
gh release edit "${RELEASE}" --repo "${REPOSITORY}" \
  --draft=false --prerelease
gh api "repos/${REPOSITORY}/releases/tags/${RELEASE}" \
  --jq '{tag_name,draft,prerelease,immutable}'
```

Expected final values are exact tag, `draft: false`, `prerelease: true`, and `immutable: true`. Cluster Management then lists candidate only when it is same or newer than pinned baseline, descends from pinned commit, and passes release-manifest compatibility checks.

Before publication, wait for packaging to pass. Inspect bootstrap archive/manifest, `borealis-node-images-linux-amd64.json` and all nine `borealis-node-image-<service>-linux-amd64.oci.tar` assets. Verify GitHub asset digests and matching release/source identities. Packaging requires Docker Buildx and builds production images once in an isolated builder. These assets are delivered by the existing Engine; operators do not download or execute them on joining hosts. Packaging alone does not qualify SSH enrollment.

??? note "Older qualification source"
    Source predating central SSH packaging can still use the existing source-only rolling-update qualification path. Omit the packaging commands for that source; it cannot supply the node bootstrap required by SSH onboarding. Never substitute a bundle from another source commit.

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

# Maintainer check above verifies setting. Workflow checks draft and exact tag.
gh workflow run publish-engine-release-assets.yml \
  --repo "${REPOSITORY}" --ref main -f "release=${RELEASE}"
gh run list --repo "${REPOSITORY}" \
  --workflow publish-engine-release-assets.yml \
  --event workflow_dispatch --limit 5
```

Wait for matching workflow run to pass. Current-source draft must contain these generated assets:

- `Install-Engine.sh`
- `Engine.sh`
- `borealis-engine-install-manifest.json`
- `SHA256SUMS`
- `borealis-node-bootstrap-linux-amd64.tar.gz`
- `borealis-node-bootstrap-linux-amd64.json`
- `borealis-node-images-linux-amd64.json`
- Nine `borealis-node-image-<service>-linux-amd64.oci.tar` production images

Inspect GitHub digests, download assets, verify installer checksums, and confirm manifests bind repository, release name, source SHA, platform, asset URLs, sizes, and hashes. The node bootstrap manifest records its archive and node-manager hashes separately from installer `SHA256SUMS`. Older source without the node packager retains the four-file standalone installer bundle; it cannot supply SSH onboarding assets.

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

Expected final values are exact tag, `draft: false`, `prerelease: false`, `immutable: true`, and all expected assets with `sha256:` digests. Stable `Install-Engine.sh` rejects release when any identity, immutability, digest, manifest, platform, size, URL, or tag-to-commit check fails.

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

    - `.github/workflows/publish-engine-release-assets.yml` requires an unpublished draft and matching stable/qualification tag channel. Stable source retains installer packaging; source containing the node packager also builds SSH bootstrap assets. Qualification packaging requires the node packager and omits standalone installer assets. GitHub's workflow token cannot query immutable-release setting because endpoint requires repository Administration permission; maintainer performs that check before dispatch. Qualification from a reviewed unmerged PR dispatches the existing workflow at its exact release tag before publication.
    - `Tests/tools/build_engine_release_assets.py` copies exact tag's `Install-Engine.sh` and `Engine.sh`, writes installer manifest, and generates `SHA256SUMS`.
    - `Tests/tools/build_node_bootstrap_assets.py` verifies the local tag resolves to the requested full SHA, makes a separate shallow Git clone through Git's protocol, and builds that clone's node manager using Go1.25.12 with Linux/AMD64, CGO disabled, readonly modules and trimmed build paths. Bundle contains clean `source/`, `bin/borealis-node-manager` and `identity.json`; the outer manifest binds archive hash/size and inner source-tree/binary identity. No operator runtime, untracked file, local Git configuration, credential helper, historical revision or working-tree edit is included. Current contract rejects tracked symlinks/submodules before checkout/build. Normalized archive metadata and zero-stat Git index make repeated packaging reproducible. Existing assets are never overwritten; compressed bundle limit256MiB. SBOM records compiler/runtime use.
    - `Tests/tools/build_node_image_assets.py` builds all nine production roles from a separate exact-tag checkout using a private Buildx docker-container builder. It neither selects the operator's builder nor prunes operator Docker state. Linux/AMD64 OCI exports disable attestations, use gzip layers and carry exact source/service labels. Normalization changes only outer tar metadata and import-reference annotations; manifest/config/layer blobs retain their hashes. Existing output directories are never overwritten. Failed builds remove their own output and builder; the inventory is written only after all images pass the source-built node-manager verifier.
    - `internal/clusterbootstrap/image_archive.go` scans without extraction or execution. It checks every outer byte, blob digest/length, config platform/source/role, image reference, layer DiffID and measured compressed/expanded/file/entry counts. Limits are2GiB minus one byte per archive,32GiB expanded tar and one million entries per image,128layers and512KiB inventory. Unsupported OCI forms, duplicate/case-aliased JSON, invalid UTF-8, extra blobs, unsafe paths, special/sparse layers and concatenated gzip fail. Container symlinks remain inert archive data. `image_inventory.go` requires the exact sorted nine-role set and compares all measured fields after download.
    - `cluster_ssh_images.go` resolves fresh immutable publication and asset identities, downloads the small inventory before capacity observation, and binds its measured application-image demand to original source/cohort checks. `image_stage.go` then stages a complete image generation in private scratch, hashes incoming streams and independently measures archives. Failed sets are removed; export rehashes bytes before and during synchronous transfer, brackets it with authority checks and exposes no paths. The preparation-source scope retains joined lease cancellation and owns cleanup. No archive is imported by this code, and the current receiver remains verify-only. Application-image demand is conditional: OS packages, K3s/external images, filesystem allocation/inodes and runtime reserves still require complete evidence before readiness.
    - `cluster_ssh_bootstrap.go` refreshes published immutable GitHub release/channel/tag/source identity independently of picker cache. It requires unique uploaded manifest/archive asset IDs, exact repository URLs, positive bounded sizes and GitHub SHA256 digests. Strict metadata rejects duplicate keys, case aliases and null authorization fields; strict manifests additionally reject unknown/missing fields, invalid UTF-8 and trailing JSON. Qualification requires explicit operation opt-in. Release compatibility/ancestry and controller authority remain caller requirements.
    - [GitHub release asset API](https://docs.github.com/en/rest/releases/assets) returns binary200 or302. Bootstrap downloads use authenticated API asset IDs, then at most one fresh unauthenticated request to the exact HTTPS `release-assets.githubusercontent.com/github-production-release-asset/` destination. No Authorization, cookie or Referer reaches CDN; signed URL and remote error text stay out of diagnostics. Redirects from metadata endpoints, second asset redirects, content encoding, changed Content-Length, sizes or digests fail closed. Existing API-base override remains trusted deployment/test configuration; manifest URLs never choose download authority.
    - `internal/clusterbootstrap` hashes compressed input before scanning. It bounds manifests16KiB, archive256MiB, expansion1GiB and entries20000, rejects links/special files, unsafe paths, duplicates, unsupported Git metadata, gzip concatenation/trailing bytes and checksum errors. Complete scan verifies inner identity and node-manager digest before any extraction. New private0700 scratch contains regular files only; failed scratch is removed and successful caller must close it after transfer.
    - `image_transfer.go` frames one bounded inventory, its nine exact-length archives and an inventory-digest trailer. The target independently verifies the expected release and every archive into fresh private scratch; interrupted sets never reach the consumer. It leaves later protocol frames unread. Transport owns cancellation of blocked IO and must retain host-key/lease supervision. `RuntimeManifest` emits existing schema1 services with digest-pinned workload references and a separate release-tag archive reference; the legacy hash field contains the image manifest digest, not a local-build cache key.
    - `image_install_linux.go` publishes verified archives to the fixed K3s image directory and writes the existing node image manifest under a supervised `stage_images` journal intent. It requires recorded source, shared identity and host preparation, rejects joined hosts, uses owned non-symlink directories, fsyncs files/directories and never replaces existing names. It retains existing image generations. A failed partial publication remains outcome_unknown; a fresh executor may reconcile success only by reading all archive bytes and exact manifest after predecessor quiescence. This proves file publication, not containerd readiness. K3s's [native importer](https://github.com/k3s-io/k3s/blob/v1.36.3%2Bk3s1/pkg/agent/containerd/containerd.go) supplies pin/distribution labels and repository-digest aliases when loading archives. The current remote dispatcher remains verify-only; paired readiness, write-boundary capacity, native import evidence and admission must be wired before enabling this action.
    - Source verification checks fixed Git config and exact HEAD/shallow boundaries before invoking trusted host Git with inherited `GIT_*` settings removed, hooks/fsmonitor disabled, network protocols disabled, bounded output and45second command deadlines. `fsck`, original tag, source tree and single commit must match; every tracked regular file/mode/blob is compared without executing source filters. ELF/Go build metadata must match LinuxAMD64/CGO0/Go1.25.12 and node-manager module path. Archive and source verification run under the download caller's five-minute context. Git already ships in API runtime; callers in other runtime images must supply it before enabling this path.
    - The packaging tool does not attest publication or authorize a host mutation. Download/verification functions are available for the unfinished S01 worker; no queue or SSH preparation caller is enabled yet. Archive availability does not enable the current connection-check dialog to join nodes. [S01](https://github.com/bunny-lab-io/Borealis/issues/521) still owns fixed remote transfer/execution, lease fencing, identity delivery and admission.
    - `Install-Engine.sh` accepts only published, non-prerelease, immutable stable release and validates GitHub plus manifest identities before invoking `Engine.sh`.
    - `Data/Engine/release-manifest.json` controls cluster release compatibility independently from standalone installer manifest.
    - `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go` requires published `immutable: true` metadata and matching channel, resolves the tag once, reads the compatibility manifest through that full SHA, and verifies ancestry. Queue requests refresh publication metadata independently of the picker cache. Redirects, oversized or trailing JSON, unavailable metadata, and missing/mismatched manifests fail closed.
    - `cluster_release_identity.go`, `server_cluster_store.go`, and `cluster_controller.go` bind the verified manifest to durable K3s configuration, recheck configuration while queueing under the cluster-state lock, persist the proof for retries, and observe every active Kubernetes node before mutation. The node manager independently rejects moved tags against the recorded SHA.

    ### Immutable boundary

    GitHub locks tag and uploaded assets only when draft is published. Title, notes, latest flag, and pre-release flag remain editable at GitHub layer. Borealis adds stricter channel identity: stable tag must remain normal release and `-rc.N` tag must remain pre-release. Existing releases published before repository setting was enabled remain mutable because setting is prospective.

    GitHub immutable releases generate release attestation covering tag, commit SHA, and release assets. Borealis stable bootstrap also verifies GitHub asset digest metadata and deterministic installer manifest so runtime fails closed without depending on operator's local release inspection.
